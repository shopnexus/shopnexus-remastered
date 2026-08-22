// Package smtp delivers this API's transactional email over SMTP.
//
// SMTP rather than a vendor's HTTP API on purpose: the same client works against SES,
// Resend, Mailgun, Postmark or a company relay, so choosing a mail provider stays a
// configuration change instead of a code change.
package smtp

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"html"
	"mime"
	"net"
	"net/smtp"
	"net/url"
	"strconv"
	"strings"
	"time"

	"shopnexus/internal/provider/notify"
	"shopnexus/internal/shared/lang"
	"shopnexus/templates"
)

// Name is the EMAIL_PROVIDER value that selects this sender.
const Name = "smtp"

// implicitTLSPort is the submission port that expects TLS from the first byte. Every
// other port negotiates it with STARTTLS.
const implicitTLSPort = 465

type Config struct {
	Host string
	Port int
	// Username and Password authenticate the submission. PlainAuth refuses to hand
	// them over on an unencrypted connection, which is why TLS is not optional below.
	Username string
	Password string
	// From is the envelope and header sender, e.g. "ShopNexus <no-reply@shopnexus.vn>".
	From string
	// VerifyEmailURL and ResetPasswordURL are the *client's* pages, and the token is
	// appended as a query parameter. The API never renders those pages, so the link
	// cannot be derived from a request path.
	VerifyEmailURL   string
	ResetPasswordURL string
	// AppBaseURL is the client application's root, from which this package builds an order
	// page at /account/orders/<id> and the help centre at /help. Both are routes the client
	// serves; this used to build /orders/<id>, which it never has, so every order mail's
	// button landed on a 404.
	//
	// One base plus a path this package owns, rather than a configured URL per kind: the
	// two above are configured because a token has to be handed to a page whose shape only
	// the client knows, while "an order is at /orders/<id>" is a route this platform does
	// define. Eight more required config values would be eight more ways for a deployment
	// to link somewhere that does not exist.
	AppBaseURL string
	// Timeout bounds the whole SMTP conversation — dial, handshake, auth, DATA. Required:
	// net/smtp has no deadline of its own, so without it a relay that stops reading
	// holds the request goroutine forever.
	Timeout time.Duration
}

var _ notify.EmailSender = (*Client)(nil)

type Client struct {
	cfg  Config
	addr string
	auth smtp.Auth
	// mails is every kind × language, parsed once at construction.
	mails map[notify.Kind]map[string]*mail
}

func NewClient(cfg Config) (*Client, error) {
	switch {
	case cfg.Host == "":
		return nil, errors.New("smtp config: host is required")
	case cfg.Port <= 0:
		return nil, errors.New("smtp config: port is required")
	case cfg.Username == "":
		return nil, errors.New("smtp config: username is required")
	case cfg.Password == "":
		return nil, errors.New("smtp config: password is required")
	case cfg.From == "":
		return nil, errors.New("smtp config: from is required")
	case cfg.Timeout <= 0:
		return nil, errors.New("smtp config: timeout is required")
	}
	if err := checkLink("verify email url", cfg.VerifyEmailURL); err != nil {
		return nil, err
	}
	if err := checkLink("reset password url", cfg.ResetPasswordURL); err != nil {
		return nil, err
	}
	if err := checkLink("app base url", cfg.AppBaseURL); err != nil {
		return nil, err
	}
	mails, err := loadMails(templates.Mail())
	if err != nil {
		return nil, err
	}
	return &Client{
		cfg:   cfg,
		addr:  net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)),
		auth:  smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host),
		mails: mails,
	}, nil
}

// checkLink fails at startup rather than at the first sign-up: a malformed base URL
// produces a mail whose button goes nowhere, and nobody reports that as a bug.
func checkLink(name, raw string) error {
	if raw == "" {
		return fmt.Errorf("smtp config: %s is required", name)
	}
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || u.Host == "" {
		return fmt.Errorf("smtp config: %s must be an absolute URL, got %q", name, raw)
	}
	return nil
}

func (c *Client) SendEmail(ctx context.Context, m notify.Message) error {
	if m.Email == "" {
		return notify.ErrNoChannel
	}
	subject, body, err := c.render(m)
	if err != nil {
		return err
	}
	msg := c.message(m.Email, subject, body)

	ctx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()
	if err := c.deliver(ctx, m.Email, msg); err != nil {
		return fmt.Errorf("send email over smtp: %w", err)
	}
	return nil
}

// render turns the message into a subject and an HTML body, with the link the recipient
// clicks already built.
func (c *Client) render(m notify.Message) (subject string, body []byte, err error) {
	byLang, ok := c.mails[m.Kind]
	if !ok {
		return "", nil, fmt.Errorf("smtp: no email template for kind %q", m.Kind)
	}
	l := lang.Of(m.Locale)
	t, ok := byLang[l]
	if !ok {
		return "", nil, fmt.Errorf("smtp: no email template for kind %q in %s", m.Kind, l)
	}
	link, err := c.link(m)
	if err != nil {
		return "", nil, err
	}
	data := c.decorate(mailData{Lang: l, Link: link, Params: m.Params}, m)

	var head strings.Builder
	if err := t.set.ExecuteTemplate(&head, "subject", data); err != nil {
		return "", nil, fmt.Errorf("render email subject: %w", err)
	}
	var buf bytes.Buffer
	if err := t.set.ExecuteTemplate(&buf, t.frame, data); err != nil {
		return "", nil, fmt.Errorf("render email body: %w", err)
	}
	// The subject is a header, not markup, so the HTML escaping the template applied on
	// the way out has to come back off: an '&' in a subject line belongs there, and
	// "&amp;" in an inbox is a bug the sender never sees.
	return html.UnescapeString(strings.TrimSpace(head.String())), buf.Bytes(), nil
}

// decorate fills in what the frame draws that is not copy: tone, beat, escrow box and order
// reference. One place to read when a mail comes out the wrong colour.
func (c *Client) decorate(data mailData, m notify.Message) mailData {
	lk, ok := looks[m.Kind]
	if !ok {
		// An empty look renders a blank card rather than an obvious bug.
		lk = look{tone: toneMoving}
	}
	data.Tone = lk.tone
	data.Step = lk.step
	data.Escrow = lk.escrow
	if total, sent := m.Params["total"]; sent {
		data.Amount = lang.Money(data.Lang, total, m.Params["currency"])
	}
	data.OrderRef, _ = m.Params["order_id"].(string)
	data.HelpLink = c.helpLink()
	return data
}

// helpLink is the footer's help centre. Not its own config value: it is a fixed route on the
// client AppBaseURL already points at.
func (c *Client) helpLink() string {
	link, err := url.JoinPath(c.cfg.AppBaseURL, "help")
	if err != nil {
		// AppBaseURL parsed at construction; a footer link is not worth failing a send over.
		return c.cfg.AppBaseURL
	}
	return link
}

// link builds the page this mail sends the recipient to: the client's own verify/reset
// page for the two secret-carrying kinds, and the order's page for everything else.
func (c *Client) link(m notify.Message) (string, error) {
	switch m.Kind {
	case notify.KindEmailVerification:
		return withToken(c.cfg.VerifyEmailURL, m.Token)
	case notify.KindPasswordReset:
		return withToken(c.cfg.ResetPasswordURL, m.Token)
	default:
		orderID, _ := m.Params["order_id"].(string)
		if orderID == "" {
			return "", fmt.Errorf("smtp: kind %q needs an order_id parameter to link to", m.Kind)
		}
		link, err := url.JoinPath(c.cfg.AppBaseURL, "account", "orders", orderID)
		if err != nil {
			return "", fmt.Errorf("build order link: %w", err)
		}
		return link, nil
	}
}

// withToken appends the token to a client page. url.Values encodes it, so a token
// containing '-' or '_' from base64url survives intact, and a base that already carries a
// query parameter keeps it.
func withToken(base, token string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("parse link base: %w", err)
	}
	q := u.Query()
	q.Set("token", token)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// message assembles the RFC 5322 bytes. The subject goes through Q-encoding because it
// is Vietnamese: a raw UTF-8 header is not legal and renders as mojibake.
func (c *Client) message(to, subject string, body []byte) []byte {
	var b strings.Builder
	b.WriteString("From: " + c.cfg.From + "\r\n")
	b.WriteString("To: " + to + "\r\n")
	b.WriteString("Subject: " + mime.QEncoding.Encode("utf-8", subject) + "\r\n")
	b.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
	// Transactional mail must not end up in a promotions tab or a bulk digest.
	b.WriteString("Auto-Submitted: auto-generated\r\n")
	b.WriteString("\r\n")
	b.Write(body)
	return []byte(b.String())
}

// deliver runs the conversation by hand rather than through smtp.SendMail, for two
// reasons: SendMail takes no context, and the connection needs a deadline of its own
// because net/smtp will otherwise wait on a silent relay indefinitely.
func (c *Client) deliver(ctx context.Context, to string, msg []byte) error {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", c.addr)
	if err != nil {
		return fmt.Errorf("dial %s: %w", c.addr, err)
	}
	defer conn.Close()

	// One deadline for the whole exchange, taken from the context so a caller with a
	// shorter budget still wins.
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			return fmt.Errorf("set connection deadline: %w", err)
		}
	}

	tlsCfg := &tls.Config{ServerName: c.cfg.Host}
	if c.cfg.Port == implicitTLSPort {
		tlsConn := tls.Client(conn, tlsCfg)
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			return fmt.Errorf("tls handshake: %w", err)
		}
		conn = tlsConn
	}

	client, err := smtp.NewClient(conn, c.cfg.Host)
	if err != nil {
		return fmt.Errorf("smtp greeting: %w", err)
	}
	defer func() { _ = client.Close() }()

	if c.cfg.Port != implicitTLSPort {
		ok, _ := client.Extension("STARTTLS")
		if !ok {
			// Refused rather than downgraded: PlainAuth would hand the password to a
			// plaintext session, and a "best effort" here is how credentials leak.
			return errors.New("server does not offer STARTTLS")
		}
		if err := client.StartTLS(tlsCfg); err != nil {
			return fmt.Errorf("starttls: %w", err)
		}
	}
	if err := client.Auth(c.auth); err != nil {
		return fmt.Errorf("authenticate: %w", err)
	}
	if err := client.Mail(senderAddress(c.cfg.From)); err != nil {
		return fmt.Errorf("mail from: %w", err)
	}
	if err := client.Rcpt(to); err != nil {
		return fmt.Errorf("rcpt to: %w", err)
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("data: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("write message: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("close message: %w", err)
	}
	return client.Quit()
}

// senderAddress strips the display name: the envelope takes a bare address, and
// "ShopNexus <no-reply@x>" is rejected by a strict relay.
func senderAddress(from string) string {
	if i := strings.LastIndex(from, "<"); i >= 0 {
		if j := strings.LastIndex(from, ">"); j > i {
			return from[i+1 : j]
		}
	}
	return from
}
