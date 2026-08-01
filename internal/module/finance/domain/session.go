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

// Settled reports whether the session has reached a terminal state. Nothing moves a
// settled session: the ledger is appended to instead.
func (s Session) Settled() bool {
	return s.Status == StatusSuccess || s.Status == StatusCancelled || s.Status == StatusFailed
}

// RailPayable reports whether a payer tenders this kind on a payment rail. Only a
// checkout: a payout and a withdrawal move money the other way, and they are resolved by
// the payout process rather than by the account on the receiving end tendering against
// them. Without this a requester could drive their own cash-out to `success`.
func (s Session) RailPayable() bool { return s.Kind == KindBuyerCheckout }

// Charge is the session accepting a payment attempt. Only a pending session is
// payable — a processing one already has a leg in flight, and paying twice is what
// the status is there to prevent.
func (s *Session) Charge(now time.Time) error {
	if !s.RailPayable() {
		return ErrSessionKindNotPayable
	}
	if s.Status != StatusPending {
		return ErrSessionNotPayable
	}
	if s.Expired(now) {
		return ErrSessionExpired
	}
	s.Status = StatusProcessing
	return nil
}

// MarkPaid is what a settled charge does to the session. It is the moment the money
// is real, and everything downstream — an order appearing, escrow being held — hangs
// off it.
func (s *Session) MarkPaid(now time.Time) error {
	if s.Settled() {
		return ErrSessionSettled
	}
	s.Status = StatusSuccess
	s.PaidAt = &now
	return nil
}

// MarkFailed records that the rail refused. A failed session is terminal: the buyer
// opens a new one rather than retrying this, so there is no path back to pending.
func (s *Session) MarkFailed() error {
	if s.Settled() {
		return ErrSessionSettled
	}
	s.Status = StatusFailed
	return nil
}

// ReopenForRetry puts a session that had a rail refuse it back on the shelf, so the payer
// can tender another. Without this a failed leg strands the session in `processing`, where
// Charge refuses it and nothing else moves it — the split-tender flow the contract
// describes would be unreachable.
func (s *Session) ReopenForRetry(now time.Time) error {
	if s.Settled() {
		return ErrSessionSettled
	}
	if s.Expired(now) {
		return ErrSessionExpired
	}
	s.Status = StatusPending
	return nil
}

// Cancel is the payer walking away, or the expiry job doing it for them. Only
// before the money moves: a paid session is refunded, not cancelled.
func (s *Session) Cancel() error {
	if s.Settled() {
		return ErrSessionSettled
	}
	s.Status = StatusCancelled
	return nil
}

// Involves reports whether the account is a party to this session — which is what
// makes reading it legitimate. A session with no parties is the system's own.
func (s Session) Involves(accountID int64) bool {
	return accountID != 0 && (s.FromID == accountID || s.ToID == accountID)
}
