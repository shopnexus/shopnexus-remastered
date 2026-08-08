package domain

import (
	"time"

	"shopnexus/internal/shared/validation"
)

// Transaction is one leg of a session on an **external rail** — a card charge, a
// bank transfer, a refund leg. Wallet-only movements are not here: they are
// Movement rows, because a wallet debit has no gateway and no provider reference.
//
// Append-only. A reversal is a new row with a negative amount pointing at what it
// reverses, so the ledger is never rewritten and the sum is the truth.
type Transaction struct {
	ID        int64  `validate:"required"`
	SessionID int64  `validate:"required"`
	Status    string `validate:"required,oneof=pending success failed"`
	Note      string
	Error     *string
	// PaymentOption is the rail: a kebab-case slug naming one of the payment
	// `option` rows. Every leg has one, since wallet movements are not legs.
	PaymentOption string `validate:"required"`
	// ProviderRef is the gateway's own id for this leg, and the thing that stops a
	// redelivered webhook being booked as a second charge. NULL only while the leg is
	// pending and the gateway has not assigned one.
	ProviderRef *string
	// CheckoutURL is the gateway page this leg sent the payer to. Nil for a direct-debit
	// rail, which decides on the spot and has nowhere to send anybody.
	CheckoutURL *string
	Data        []byte
	// Amount is signed: positive is the charge, negative is the reversal.
	Amount     int64  `validate:"required"`
	Currency   string `validate:"required,len=3"`
	ReversesID *int64
	CreatedAt  time.Time
	SettledAt  *time.Time
	ExpiredAt  *time.Time
}

// NewCharge starts a pending leg. The id is pre-allocated for the same reason a
// session's is: a gateway is handed the reference before the row exists.
func NewCharge(id, sessionID int64, option, currency string, amount int64, data []byte) (Transaction, error) {
	if amount <= 0 {
		return Transaction{}, ErrChargeAmountInvalid
	}
	t := Transaction{
		ID: id, SessionID: sessionID, Status: StatusPending, PaymentOption: option,
		Currency: currency, Amount: amount, Data: jsonOrEmpty(data), Note: "charge",
	}
	if err := validation.Default().Struct(t); err != nil {
		return Transaction{}, validation.AsError(err)
	}
	return t, nil
}

// Resumable reports whether the payer can be sent back to this leg's gateway page.
//
// A redirect rail reports nothing when the payer closes its tab, so an abandoned attempt is
// indistinguishable from one still in progress — and it is the same attempt either way. That
// is what makes reopening the right answer and a second leg the wrong one: two live pages on
// one session can both be paid, and the second payment has no outstanding balance to land on.
func (t Transaction) Resumable(now time.Time) bool {
	if t.Status != StatusPending || t.CheckoutURL == nil || *t.CheckoutURL == "" {
		return false
	}
	return t.ExpiredAt == nil || now.Before(*t.ExpiredAt)
}

// NewReversal is the refund leg: a negative row pointing at the charge it undoes.
// The original has to have settled — reversing something that never happened is a
// bookkeeping error, not a refund.
func (t Transaction) NewReversal(id int64, amount int64) (Transaction, error) {
	if t.Status != StatusSuccess {
		return Transaction{}, ErrReversalNeedsSuccess
	}
	if amount <= 0 || amount > t.Amount {
		return Transaction{}, ErrChargeAmountInvalid
	}
	return Transaction{
		ID: id, SessionID: t.SessionID, Status: StatusPending,
		PaymentOption: t.PaymentOption, Currency: t.Currency,
		Amount: -amount, ReversesID: &t.ID, Data: []byte("{}"), Note: "reversal",
	}, nil
}

// Settle is what a gateway's notification does to a leg: success is terminal, and
// so is failure — a leg is never retried in place, a new one is opened instead.
func (t *Transaction) Settle(status string, providerRef string, failure string) error {
	if t.Status != StatusPending {
		return ErrTransactionSettled
	}
	switch status {
	case StatusSuccess:
		t.Status = StatusSuccess
		t.SettledAt = new(time.Now())
	case StatusFailed:
		t.Status = StatusFailed
		if failure != "" {
			t.Error = &failure
		}
	default:
		return ErrTransactionStatusInvalid
	}
	if providerRef != "" {
		t.ProviderRef = &providerRef
	}
	return nil
}

func jsonOrEmpty(b []byte) []byte {
	if len(b) == 0 {
		return []byte("{}")
	}
	return b
}
