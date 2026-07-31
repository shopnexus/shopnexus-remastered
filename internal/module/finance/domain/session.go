// Package domain: payment entities + pure business rules.
package domain

import (
	"time"

	"shopnexus/internal/shared/validation"
)

// Session kinds (kebab-case). Stored in a TEXT column: the enum lives here, in
// the app layer, because the set changes with product rules. A cash-out is just
// another kind — there is no separate withdrawal table; its destination bank
// account and admin resolution live in the session's Data.
const (
	KindBuyerCheckout = "buyer-checkout"
	KindSellerPayout  = "seller-payout"
	KindWithdrawal    = "withdrawal"
)

// Lifecycle status shared by sessions and ledger transactions (status enum).
const (
	StatusPending    = "pending"
	StatusProcessing = "processing"
	StatusSuccess    = "success"
	StatusCancelled  = "cancelled"
	StatusFailed     = "failed"
)

// Session is one logical money flow (a checkout, a fee, a payout). It carries
// the expected total; the ledger transactions under it record what actually
// moved, so split tender across rails stays auditable.
type Session struct {
	ID          int64  `validate:"required"`
	Kind        string `validate:"required,oneof=buyer-checkout seller-payout withdrawal"`
	Status      string `validate:"required,oneof=pending processing success cancelled failed"`
	FromID      int64  // zero = system
	ToID        int64  // zero = system
	Note        string
	Currency    string `validate:"required,len=3"`
	TotalAmount int64  `validate:"gt=0"`
	FXSnapshot  []byte // JSON, nil when no conversion was needed
	Data        []byte // JSON checkout context; defaults to {}
	CreatedAt   time.Time
	PaidAt      *time.Time
	ExpiredAt   time.Time `validate:"required"`
}

// NewSession starts a pending session. The id is allocated by the caller — from
// the table's identity sequence, before the INSERT — so the payment provider can
// be handed a known id before the row exists.
func NewSession(id int64, kind string, fromID, toID int64, note, currency string, totalAmount int64, data []byte, expiresIn time.Duration) (Session, error) {
	if len(data) == 0 {
		data = []byte("{}")
	}
	s := Session{
		ID:          id,
		Kind:        kind,
		Status:      StatusPending,
		FromID:      fromID,
		ToID:        toID,
		Note:        note,
		Currency:    currency,
		TotalAmount: totalAmount,
		Data:        data,
		ExpiredAt:   time.Now().Add(expiresIn),
	}
	if expiresIn <= 0 {
		return Session{}, ErrSessionExpiryInvalid
	}
	if err := validation.Default().Struct(s); err != nil {
		return Session{}, validation.AsError(err)
	}
	return s, nil
}

// Expired reports whether a still-pending session has passed its deadline.
func (s Session) Expired(now time.Time) bool {
	return s.Status == StatusPending && now.After(s.ExpiredAt)
}
