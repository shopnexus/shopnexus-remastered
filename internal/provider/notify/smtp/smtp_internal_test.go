package smtp

import (
	"context"
	"strings"
	"testing"
	"time"

	"shopnexus/internal/provider/notify"
)

// White-box, because what is worth testing here is the message that gets built — the
// conversation with the relay is net/smtp's job, and standing up a TLS-capable fake SMTP
// server would test the standard library rather than this package.

func testConfig() Config {
	return Config{
		Host:             "smtp.example.com",
		Port:             587,
		Username:         "apikey",
		Password:         "secret",
		From:             "ShopNexus <no-reply@shopnexus.vn>",
		VerifyEmailURL:   "https://shopnexus.vn/verify-email",
		ResetPasswordURL: "https://shopnexus.vn/reset-password?src=mail",
		AppBaseURL:       "https://shopnexus.vn",
		Timeout:          10 * time.Second,
	}
}

// paramsByKind mirrors the contract written beside each Kind in the notify package: what a
// caller must send for that mail to render. It is the fixture for every test below, so a
// template that starts naming a new variable fails here rather than in somebody's inbox.
var paramsByKind = map[notify.Kind]map[string]any{
	notify.KindEmailVerification: nil,
	notify.KindPasswordReset:     nil,
	notify.KindOrderPlaced:       {"order_id": "ord_2h9qk4mfx7bd3", "total": int64(1250000), "currency": "VND"},
	notify.KindOrderReceived:     {"order_id": "ord_2h9qk4mfx7bd3", "total": int64(1250000), "currency": "VND"},
	notify.KindOrderCompleted:    {"order_id": "ord_2h9qk4mfx7bd3"},
	notify.KindOrderCancelled:    {"order_id": "ord_2h9qk4mfx7bd3"},
	notify.KindRefundResolved:    {"order_id": "ord_2h9qk4mfx7bd3", "buyer_wins": true, "note": "Ảnh chụp cho thấy hàng không đúng mô tả."},
	notify.KindOrderUnconfirmed:  {"order_id": "ord_2h9qk4mfx7bd3"},
}

// Every kind this package claims to have copy for renders in every language, with a
// subject, a body, and no template variable left unresolved. This is the test that makes
// adding a mail safe: forget a file, a block or a parameter and it fails here.
func TestRender_EveryKindInEveryLanguage(t *testing.T) {
	c, err := NewClient(testConfig())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if len(paramsByKind) != len(mailKinds) {
		t.Fatalf("%d fixtures for %d kinds — one of the two lists has an entry the other does not",
			len(paramsByKind), len(mailKinds))
	}
	for _, kind := range mailKinds {
		params, ok := paramsByKind[kind]
		if !ok {
			t.Fatalf("kind %q has no fixture — add it beside the others when adding the mail", kind)
		}
		for _, lang := range languages {
			subject, body, err := c.render(notify.Message{
				Kind: kind, Email: "a@b.com", Token: "tok", Locale: lang, Params: params,
			})
			if err != nil {
				t.Errorf("render(%s, %s): %v", kind, lang, err)
				continue
			}
			if subject == "" {
				t.Errorf("render(%s, %s): empty subject", kind, lang)
			}
			html := string(body)
			// An unresolved action delimiter means a block was never substituted; "<no value>"
			// is what a nil parameter renders as. Both go out looking like a broken mail.
			if strings.Contains(html, "{{") || strings.Contains(html, "<no value>") {
				t.Errorf("render(%s, %s): body has an unrendered template:\n%s", kind, lang, html)
			}
			if !strings.Contains(html, `lang="`+lang+`"`) {
				t.Errorf("render(%s, %s): body is not in the requested language", kind, lang)
			}
			if !strings.Contains(html, "https://shopnexus.vn") {
				t.Errorf("render(%s, %s): body carries no link", kind, lang)
			}
		}
	}
}

// A mail whose template names a parameter the caller did not send must fail rather than
// mail a hole where the order number should be — which is what missingkey=error buys.
func TestRender_MissingParameterFails(t *testing.T) {
	c, _ := NewClient(testConfig())
	_, _, err := c.render(notify.Message{
		Kind: notify.KindRefundResolved, Locale: "vi-VN",
		Params: map[string]any{"order_id": "ord_x", "buyer_wins": true}, // no note
	})
	if err == nil {
		t.Fatal("expected an error for a template parameter that was not sent")
	}
}

// An order mail with no order to point at has no link, and a button going nowhere is worse
// than no mail: the recipient reports it as the platform being broken.
func TestLink_OrderKindWithoutAnOrderID(t *testing.T) {
	c, _ := NewClient(testConfig())
	if _, err := c.link(notify.Message{Kind: notify.KindOrderPlaced}); err == nil {
		t.Fatal("expected an error for an order mail with no order_id")
	}
}

func TestLink_OrderPageIsBuiltFromTheAppBase(t *testing.T) {
	c, _ := NewClient(testConfig())
	got, err := c.link(notify.Message{
		Kind:   notify.KindOrderCompleted,
		Params: map[string]any{"order_id": "ord_2h9qk4mfx7bd3"},
	})
	if err != nil {
		t.Fatalf("link: %v", err)
	}
	if want := "https://shopnexus.vn/orders/ord_2h9qk4mfx7bd3"; got != want {
		t.Fatalf("link = %q, want %q", got, want)
	}
}

// The amount is stored unscaled and read by a person, so it is grouped — and grouped the
// way the language of the mail does it, which is the whole reason the helper is bound to
// the template set rather than shared.
func TestMoney_GroupsPerLanguage(t *testing.T) {
	c, _ := NewClient(testConfig())
	for lang, want := range map[string]string{
		langVI: "1.250.000 ₫",
		langEN: "1,250,000 ₫",
	} {
		_, body, err := c.render(notify.Message{
			Kind: notify.KindOrderPlaced, Locale: lang, Params: paramsByKind[notify.KindOrderPlaced],
		})
		if err != nil {
			t.Fatalf("render(%s): %v", lang, err)
		}
		if !strings.Contains(string(body), want) {
			t.Errorf("body in %s does not contain %q", lang, want)
		}
	}
}

// A subject is a header, not markup: whatever the template escaped on the way out has to
// be back to plain text before it is Q-encoded into the message.
func TestRender_SubjectIsNotHTMLEscaped(t *testing.T) {
	c, _ := NewClient(testConfig())
	subject, _, err := c.render(notify.Message{
		Kind: notify.KindOrderPlaced, Locale: "vi-VN",
		Params: map[string]any{"order_id": "ord_a&b", "total": int64(1), "currency": "VND"},
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(subject, "&amp;") {
		t.Fatalf("subject = %q, want the escaping undone", subject)
	}
}

func TestRender_TokenTravelsInTheLink(t *testing.T) {
	c, err := NewClient(testConfig())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	// A base64url token: the '-' and '_' have to survive the query encoding, or the link
	// the user clicks is not the token that was stored.
	const token = "ab-cd_ef12"

	for _, tc := range []struct {
		kind        notify.Kind
		locale      string
		wantHost    string
		wantSubject string
	}{
		{kind: notify.KindEmailVerification, locale: "vi-VN", wantHost: "shopnexus.vn", wantSubject: "Xác nhận"},
		{kind: notify.KindEmailVerification, locale: "en-US", wantHost: "shopnexus.vn", wantSubject: "Confirm"},
		{kind: notify.KindPasswordReset, locale: "vi-VN", wantHost: "shopnexus.vn", wantSubject: "Đặt lại"},
	} {
		subject, body, err := c.render(notify.Message{Kind: tc.kind, Email: "a@b.com", Token: token, Locale: tc.locale})
		if err != nil {
			t.Fatalf("render(%s, %s): %v", tc.kind, tc.locale, err)
		}
		if !strings.Contains(subject, tc.wantSubject) {
			t.Errorf("subject = %q, want it to contain %q", subject, tc.wantSubject)
		}
		if !strings.Contains(string(body), "token=ab-cd_ef12") {
			t.Errorf("body does not carry the token intact:\n%s", body)
		}
		if !strings.Contains(string(body), tc.wantHost) {
			t.Errorf("body does not link to %s", tc.wantHost)
		}
	}
}

// The reset base URL already has a query parameter; adding the token must not drop it.
func TestLink_KeepsExistingQueryParameters(t *testing.T) {
	c, _ := NewClient(testConfig())
	got, err := c.link(notify.Message{Kind: notify.KindPasswordReset, Token: "tok"})
	if err != nil {
		t.Fatalf("link: %v", err)
	}
	if !strings.Contains(got, "src=mail") || !strings.Contains(got, "token=tok") {
		t.Fatalf("link = %q, want both parameters", got)
	}
}

// An unknown locale falls back to English rather than sending nothing.
func TestRender_UnknownLocaleFallsBackToEnglish(t *testing.T) {
	c, _ := NewClient(testConfig())
	subject, _, err := c.render(notify.Message{Kind: notify.KindEmailVerification, Token: "t", Locale: "fr-FR"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(subject, "Confirm") {
		t.Fatalf("subject = %q, want the English copy", subject)
	}
}

// The phone code has no email template, and a message that cannot be rendered must fail
// rather than go out blank.
func TestRender_KindWithoutATemplateFails(t *testing.T) {
	c, _ := NewClient(testConfig())
	if _, _, err := c.render(notify.Message{Kind: notify.KindPhoneCode, Token: "123456"}); err == nil {
		t.Fatal("expected an error for a kind with no email template")
	}
}

// A Vietnamese subject is not legal as raw UTF-8 in a header: unencoded it arrives as
// mojibake in half the clients.
func TestMessage_EncodesTheSubjectAndSetsTheHeaders(t *testing.T) {
	c, _ := NewClient(testConfig())
	msg := string(c.message("a@b.com", "Đặt lại mật khẩu", []byte("<p>hi</p>")))

	if strings.Contains(msg, "Subject: Đặt lại") {
		t.Error("subject was not encoded")
	}
	if !strings.Contains(msg, "Subject: =?utf-8?q?") {
		t.Errorf("subject is not Q-encoded:\n%s", msg)
	}
	for _, header := range []string{"From: ShopNexus <no-reply@shopnexus.vn>", "To: a@b.com", "MIME-Version: 1.0", "Content-Type: text/html; charset=UTF-8"} {
		if !strings.Contains(msg, header) {
			t.Errorf("missing header %q", header)
		}
	}
	// Headers and body are separated by a blank line, or the whole thing is one header.
	if !strings.Contains(msg, "\r\n\r\n<p>hi</p>") {
		t.Error("body is not separated from the headers")
	}
}

// The envelope takes a bare address; a strict relay rejects a display name there.
func TestSenderAddress(t *testing.T) {
	for from, want := range map[string]string{
		"ShopNexus <no-reply@shopnexus.vn>": "no-reply@shopnexus.vn",
		"no-reply@shopnexus.vn":             "no-reply@shopnexus.vn",
	} {
		if got := senderAddress(from); got != want {
			t.Errorf("senderAddress(%q) = %q, want %q", from, got, want)
		}
	}
}

func TestNewClient_RequiredFields(t *testing.T) {
	for name, mutate := range map[string]func(*Config){
		"host":               func(c *Config) { c.Host = "" },
		"port":               func(c *Config) { c.Port = 0 },
		"username":           func(c *Config) { c.Username = "" },
		"password":           func(c *Config) { c.Password = "" },
		"from":               func(c *Config) { c.From = "" },
		"timeout":            func(c *Config) { c.Timeout = 0 },
		"verify email url":   func(c *Config) { c.VerifyEmailURL = "" },
		"reset password url": func(c *Config) { c.ResetPasswordURL = "" },
		"app base url":       func(c *Config) { c.AppBaseURL = "" },
		// A relative link produces a mail whose button goes nowhere, and nobody reports
		// that as a bug — so it fails at startup instead.
		"absolute verify url": func(c *Config) { c.VerifyEmailURL = "/verify-email" },
	} {
		cfg := testConfig()
		mutate(&cfg)
		if _, err := NewClient(cfg); err == nil {
			t.Errorf("expected an error for %s", name)
		}
	}
}

func TestSendEmail_WithoutARecipient(t *testing.T) {
	c, _ := NewClient(testConfig())
	if err := c.SendEmail(context.TODO(), notify.Message{Kind: notify.KindEmailVerification, Token: "t"}); err != notify.ErrNoChannel {
		t.Fatalf("err = %v, want ErrNoChannel", err)
	}
}
