// Package notify is the outbound seam for a one-off message to a person: an
// email verification link, a password reset link, an SMS code.
//
// It is deliberately narrower than "send any notification". The in-app feed is a
// table in the account module, and fanning a notification out over push, email
// and SMS is a durable workflow Restate journals; what is left — a single
// transactional message carrying a secret the caller just minted — is this.
//
// The Message carries the *token*, never a rendered body: which words go out, in
// which language, is the provider's business, and a template id plus data keeps
// the account service from owning copy.
package notify

import (
	"context"
	"errors"
)

// Kind selects the template. Kebab-case, like every other enum-ish string value
// in this codebase. A kind names one template file per language under templates/mail,
// and the comment beside each is the contract for what Params must carry.
type Kind string

const (
	// The secret-carrying messages: the token is in Token, and no Params are read.
	KindEmailVerification Kind = "email-verification"
	KindPasswordReset     Kind = "password-reset"
	KindPhoneCode         Kind = "phone-code"

	// The transactional messages about a sale. Every one of them takes order_id — the
	// opaque id, because it is what the recipient sees everywhere else — and links to
	// that order's page.
	//
	// A sale has two sides, so a fact reaches them as two kinds wherever the words
	// differ: a buyer whose order is confirmed and a seller who has one to pack are not
	// reading the same mail. Where the fact is the same for both, one kind serves both.

	// KindOrderPlaced goes to the buyer. Params: order_id, total, currency.
	KindOrderPlaced Kind = "order-placed"
	// KindOrderReceived is the same fact told to the seller. Params: order_id, total, currency.
	KindOrderReceived Kind = "order-received"
	// KindOrderCompleted goes to both. Params: order_id.
	KindOrderCompleted Kind = "order-completed"
	// KindOrderCancelled goes to both. Params: order_id.
	KindOrderCancelled Kind = "order-cancelled"
	// KindRefundResolved goes to both. Params: order_id, buyer_wins, note.
	KindRefundResolved Kind = "refund-resolved"
	// KindOrderUnconfirmed goes to the buyer alone. Params: order_id.
	KindOrderUnconfirmed Kind = "order-unconfirmed"
)

// Message is one message to one recipient. Exactly one of Email and Phone is set,
// which follows from Kind: the phone code is the only one that goes over SMS.
type Message struct {
	Kind  Kind
	Email string
	Phone string
	// Token is the secret the recipient sends back — a verification token or a
	// numeric code. The provider embeds it in a link or in the text.
	Token string
	// Locale is BCP 47, from the recipient's profile, so the message is written in
	// the language they read.
	Locale string
	// Params are the template's variables, and the caller sends *facts* — an order id, a
	// total, a moderator's note — never a sentence or a formatted number. Which words go
	// out, and how an amount reads in a given locale, stays the template's business, which
	// is the same reason this struct has never carried a rendered body.
	//
	// A template that names a key the caller did not send fails to render rather than
	// mailing a hole, so the set is a contract: it is listed beside each Kind above and
	// exercised for every kind by the smtp package's tests.
	Params map[string]any
}

type Client interface {
	// Send delivers the message. It is called on the request path, so a provider
	// applies its own per-operation timeout (see the outbound-deadlines rule) and
	// keeps the budget small.
	Send(ctx context.Context, m Message) error
}

// Email and SMS are separate seams under Client because no vendor is good at both:
// mail goes out over SMTP, an OTP goes through a local aggregator with a registered
// brandname. Route composes the two into the single Client everything above uses.
type EmailSender interface {
	SendEmail(ctx context.Context, m Message) error
}

type SMSSender interface {
	SendSMS(ctx context.Context, m Message) error
}

// ErrNoChannel is a message with no usable recipient. It is a programming mistake
// rather than a provider failure — the account service decides the channel — so it
// is not one of the coded API errors.
var ErrNoChannel = errors.New("notify: message has neither an email nor a phone")

// Route sends over the channel the message actually has a recipient for. The Kind
// implies the channel (a phone code is SMS, a link is email), but the recipient is
// what settles it: a password reset goes by SMS for an account that signs in with a
// phone and has no address on file.
func Route(email EmailSender, sms SMSSender) Client {
	return &router{email: email, sms: sms}
}

type router struct {
	email EmailSender
	sms   SMSSender
}

func (r *router) Send(ctx context.Context, m Message) error {
	switch {
	case m.Email != "" && r.email != nil:
		return r.email.SendEmail(ctx, m)
	case m.Phone != "" && r.sms != nil:
		return r.sms.SendSMS(ctx, m)
	default:
		return ErrNoChannel
	}
}
