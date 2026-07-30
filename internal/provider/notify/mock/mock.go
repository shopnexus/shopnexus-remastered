// Package mock is a dev-only notify provider: it logs the message instead of
// sending it, so a developer copies the token out of the gateway's log and
// finishes the flow without an email or SMS account.
//
// It logs the token on purpose, which is exactly why it must never be wired in
// an environment real people sign in to.
package mock

import (
	"context"
	"log/slog"

	"shopnexus/internal/provider/notify"
)

var _ notify.Client = (*Client)(nil)

type Client struct {
	log *slog.Logger
}

func NewClient(log *slog.Logger) *Client {
	return &Client{log: log.With("provider", "notify-mock")}
}

// SendEmail and SendSMS make the mock usable as either half of notify.Route, so a
// developer can run real SMTP against a mock SMS aggregator — the usual case, since
// email costs nothing to test and an OTP costs money per attempt.
func (c *Client) SendEmail(ctx context.Context, m notify.Message) error { return c.Send(ctx, m) }

func (c *Client) SendSMS(ctx context.Context, m notify.Message) error { return c.Send(ctx, m) }

func (c *Client) Send(_ context.Context, m notify.Message) error {
	c.log.Warn("notification not sent — mock provider",
		"kind", string(m.Kind),
		"email", m.Email,
		"phone", m.Phone,
		"locale", m.Locale,
		"token", m.Token,
	)
	return nil
}

var (
	_ notify.EmailSender = (*Client)(nil)
	_ notify.SMSSender   = (*Client)(nil)
)
