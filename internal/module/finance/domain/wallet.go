package domain

import (
	"time"

	"shopnexus/internal/shared/validation"
)

// Wallet transaction kinds (kebab-case, mirrors the wallet_txn_kind enum).
const (
	WalletKindTopup         = "topup"
	WalletKindEscrowHold    = "escrow-hold"
	WalletKindEscrowRelease = "escrow-release"
	WalletKindPayout        = "payout"
	WalletKindRefund        = "refund"
	WalletKindWithdrawal    = "withdrawal"
	WalletKindFee           = "fee"
	WalletKindAdjustment    = "adjustment"
)

// Wallet holds one account's balances in one currency: available is
// spendable/withdrawable, held is locked in escrow. A checkout moves money from the
// first to the second without either side gaining or losing anything.
type Wallet struct {
	AccountID        int64  `validate:"required"`
	Currency         string `validate:"required,len=3"`
	AvailableBalance int64  `validate:"gte=0"`
	HeldBalance      int64  `validate:"gte=0"`
	CreatedAt        time.Time
}

// Total is what the account owns, spendable or not.
func (w Wallet) Total() int64 { return w.AvailableBalance + w.HeldBalance }

// CanSpend reports whether amount fits in the available balance.
func (w Wallet) CanSpend(amount int64) bool {
	return amount > 0 && w.AvailableBalance >= amount
}

// Movement is one entry in a wallet's append-only ledger: the signed deltas, the
// balances they produced, and what the movement was for.
//
// Seq is the wallet's own total order, which created_at cannot give — two movements
// in the same millisecond tie. It is allocated under the same row lock as the
// balance change, so a gap or a repeat is not a state that can be written.
type Movement struct {
	ID        int64
	AccountID int64  `validate:"required"`
	Currency  string `validate:"required,len=3"`
	Seq       int64  `validate:"required,gt=0"`
	Kind      string `validate:"required,oneof=topup escrow-hold escrow-release payout refund withdrawal fee adjustment"`

	AvailableDelta int64
	HeldDelta      int64
	AvailableAfter int64 `validate:"gte=0"`
	HeldAfter      int64 `validate:"gte=0"`

	// GroupID ties the legs of one logical movement together — a checkout is a buyer
	// debit plus an escrow hold plus a fee — so "does this add up" has an answer.
	GroupID *int64
	RefType *string
	RefID   *int64
	// IdempotencyKey is the caller's, e.g. "order:412:escrow-hold". Retrying a
	// movement reuses it and loses to the unique index rather than double-posting.
	IdempotencyKey *string
	Note           string
	CreatedAt      time.Time
}

// Transfer is a request to move money in one wallet. The deltas are signed, so one
// shape covers a credit, a debit and an escrow move — and the arithmetic deciding
// whether the result is legal lives in Apply rather than in each caller.
type Transfer struct {
	Kind           string
	AvailableDelta int64
	HeldDelta      int64
	GroupID        *int64
	RefType        *string
	RefID          *int64
	IdempotencyKey *string
	Note           string
}

// Apply moves the balances and returns the ledger row recording it. It refuses
// anything that would leave a negative balance — the wallet's CHECK constraints say
// the same, but a caller deserves an error it can act on rather than a driver's, and
// the rule belongs where a test can reach it without a database.
//
// A movement that moves nothing is refused too: a zero row is noise in a ledger that
// exists to be trusted as complete.
func (w *Wallet) Apply(t Transfer, seq int64) (Movement, error) {
	if t.AvailableDelta == 0 && t.HeldDelta == 0 {
		return Movement{}, ErrEmptyMovement
	}
	available := w.AvailableBalance + t.AvailableDelta
	held := w.HeldBalance + t.HeldDelta
	if available < 0 || held < 0 {
		return Movement{}, ErrInsufficientBalance
	}
	w.AvailableBalance, w.HeldBalance = available, held
	m := Movement{
		AccountID:      w.AccountID,
		Currency:       w.Currency,
		Seq:            seq,
		Kind:           t.Kind,
		AvailableDelta: t.AvailableDelta,
		HeldDelta:      t.HeldDelta,
		AvailableAfter: available,
		HeldAfter:      held,
		GroupID:        t.GroupID,
		RefType:        t.RefType,
		RefID:          t.RefID,
		IdempotencyKey: t.IdempotencyKey,
		Note:           t.Note,
	}
	if err := validation.Default().Struct(m); err != nil {
		return Movement{}, validation.AsError(err)
	}
	return m, nil
}

// The movements the marketplace actually makes, as constructors rather than deltas
// spelled out at each call site — the sign of a held delta is exactly the kind of
// thing that gets written backwards once and then trusted.

// Hold moves money out of available and into escrow: the buyer has paid, and the
// seller cannot touch it until the buyer confirms receipt.
func Hold(amount int64, ref Ref, key, note string) Transfer {
	return Transfer{
		Kind: WalletKindEscrowHold, AvailableDelta: -amount, HeldDelta: amount,
		RefType: ref.Type(), RefID: ref.ID(), IdempotencyKey: keyOf(key), Note: note,
	}
}

// Release takes money out of escrow and makes it spendable. Applied to the wallet
// the held money sits in.
func Release(amount int64, ref Ref, key, note string) Transfer {
	return Transfer{
		Kind: WalletKindEscrowRelease, AvailableDelta: amount, HeldDelta: -amount,
		RefType: ref.Type(), RefID: ref.ID(), IdempotencyKey: keyOf(key), Note: note,
	}
}

// Credit adds to available: a top-up, a refund landing back, a payout arriving.
func Credit(kind string, amount int64, ref Ref, key, note string) Transfer {
	return Transfer{
		Kind: kind, AvailableDelta: amount,
		RefType: ref.Type(), RefID: ref.ID(), IdempotencyKey: keyOf(key), Note: note,
	}
}

// Debit takes from available: a withdrawal leaving, a fee charged.
func Debit(kind string, amount int64, ref Ref, key, note string) Transfer {
	return Transfer{
		Kind: kind, AvailableDelta: -amount,
		RefType: ref.Type(), RefID: ref.ID(), IdempotencyKey: keyOf(key), Note: note,
	}
}

// Adjust is the admin's correction, in whichever direction. It is the only movement
// with no order or session behind it, which is why its note carries the reason: a
// balance change nobody can explain later is the one thing an audit cannot survive.
func Adjust(availableDelta, heldDelta int64, key, note string) Transfer {
	return Transfer{
		Kind: WalletKindAdjustment, AvailableDelta: availableDelta, HeldDelta: heldDelta,
		IdempotencyKey: keyOf(key), Note: note,
	}
}

// Ref is what a movement points at. A pair rather than two loose pointers, because a
// ref_id with no ref_type is a row nobody can interpret.
type Ref struct {
	Kind string
	Key  int64
}

// The ref kinds this module records against.
const (
	RefOrder          = "order"
	RefPaymentSession = "payment-session"
)

// OrderRef and SessionRef are the two shapes callers build, so a kind string is
// never spelled at a call site.
func OrderRef(orderID int64) Ref { return Ref{Kind: RefOrder, Key: orderID} }

func SessionRef(sessionID int64) Ref { return Ref{Kind: RefPaymentSession, Key: sessionID} }

func (r Ref) Type() *string {
	if r.Kind == "" {
		return nil
	}
	return &r.Kind
}

func (r Ref) ID() *int64 {
	if r.Key == 0 {
		return nil
	}
	return &r.Key
}

func keyOf(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
