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
	"mime"
	"net"
	"net/smtp"
	"net/url"
	"strconv"
	"strings"
	"time"

	"shopnexus/internal/provider/notify"
)

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
	return &Client{
		cfg:  cfg,
		addr: net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)),
		auth: smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host),
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

// render turns the message into a subject and an HTML body, with the token already in
// the link the recipient clicks.
func (c *Client) render(m notify.Message) (subject string, body []byte, err error) {
	byLang, ok := mails[m.Kind]
	if !ok {
		return "", nil, fmt.Errorf("smtp: no email template for kind %q", m.Kind)
	}
	build, ok := byLang[language(m.Locale)]
	if !ok {
		return "", nil, fmt.Errorf("smtp: no email template for kind %q in any language", m.Kind)
	}
	link, err := c.link(m)
	if err != nil {
		return "", nil, err
	}
	subject, data := build(link)

	var buf bytes.Buffer
	if err := frame.Execute(&buf, data); err != nil {
		return "", nil, fmt.Errorf("render email body: %w", err)
	}
	return subject, buf.Bytes(), nil
}

// link appends the token to the client page for this kind. url.Values encodes it, so a
// token containing '-' or '_' from base64url survives intact.
func (c *Client) link(m notify.Message) (string, error) {
	var base string
	switch m.Kind {
	case notify.KindEmailVerification:
		base = c.cfg.VerifyEmailURL
	case notify.KindPasswordReset:
		base = c.cfg.ResetPasswordURL
	default:
		return "", fmt.Errorf("smtp: kind %q has no link", m.Kind)
	}
	u, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("parse link base: %w", err)
	}
	q := u.Query()
	q.Set("token", m.Token)
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
