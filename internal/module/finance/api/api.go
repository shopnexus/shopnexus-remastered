// Package financeapi is the published contract of the finance service: payment
// sessions and their rail legs, wallets and their ledger, bank accounts,
// withdrawals and tax registrations.
//
// All money primitives live in this module so an escrow move stays atomic. Order
// calls the four service-to-service methods at the bottom; everything above them is
// a route.
package financeapi

import (
	"context"
	"encoding/json/jsontext"
	"time"

	"shopnexus/internal/module/common"
	"shopnexus/internal/shared/id"
)

type Session struct {
	ID          id.ID[id.PaymentSession] `json:"id"`
	Kind        string                   `json:"kind"`
	Status      string                   `json:"status"`
	Currency    string                   `json:"currency"`
	TotalAmount int64                    `json:"total_amount"`
	// Outstanding is the total less what has already settled on a rail: what a further
	// payment may still tender. Computed, because a stored copy is a second fact to
	// keep in step with every leg.
	Outstanding int64  `json:"outstanding"`
	Note        string `json:"note"`
	// Data is the checkout context whoever opened the session wrote — the draft or offer it came
	// from, and the delivery charge included in the total. Read back rather than kept only by the
	// opener, because the session is what the buyer paid against and the module that settles it
	// is handed nothing but the session id.
	//
	// Never serialised (`json:"-"`): it is the opener's own shape, holding raw row ids rather
	// than opaque ones, and a client that reads it would be reading another module's internals
	// off the wire. It travels between modules in-process, like `order`'s carrier payload.
	Data      jsontext.Value `json:"-"`
	CreatedAt time.Time      `json:"created_at"`
	PaidAt    *time.Time     `json:"paid_at"`
	ExpiredAt time.Time      `json:"expired_at"`
}

type SessionPage struct {
	Data []Session `json:"data"`
	Meta PageInfo  `json:"meta"`
}

// Transaction is one leg on an external rail. Append-only: a reversal is another leg
// with a negative amount, never an edit of this one.
type Transaction struct {
	ID            id.ID[id.Transaction]    `json:"id"`
	SessionID     id.ID[id.PaymentSession] `json:"session_id"`
	Status        string                   `json:"status"`
	PaymentOption string                   `json:"payment_option"`
	Amount        int64                    `json:"amount"`
	Currency      string                   `json:"currency"`
	// Note is whatever the rail or the platform recorded about this leg — the failure the
	// provider gave, the reason a reversal was made.
	Note       string                 `json:"note"`
	ReversesID *id.ID[id.Transaction] `json:"reverses_id"`
	// CheckoutURL is the gateway's redirect, present only while the leg is pending and
	// only for a rail that redirects. Not a receipt: the webhook settles the leg.
	CheckoutURL string     `json:"checkout_url"`
	Error       string     `json:"error"`
	CreatedAt   time.Time  `json:"created_at"`
	SettledAt   *time.Time `json:"settled_at"`
	ExpiredAt   *time.Time `json:"expired_at"`
}

type Wallet struct {
	AccountID        id.ID[id.Account] `json:"account_id"`
	Currency         string            `json:"currency"`
	AvailableBalance int64             `json:"available_balance"`
	HeldBalance      int64             `json:"held_balance"`
	// CreatedAt is when the account first held this currency. A wallet is not registered:
	// it exists from the first movement, so this is that movement's moment.
	CreatedAt time.Time `json:"created_at"`
}

// WalletMovement is one row of the ledger, with the balances it produced — which is
// what makes the history auditable without replaying it.
type WalletMovement struct {
	Seq            int64     `json:"seq"`
	Currency       string    `json:"currency"`
	Kind           string    `json:"kind"`
	AvailableDelta int64     `json:"available_delta"`
	HeldDelta      int64     `json:"held_delta"`
	AvailableAfter int64     `json:"available_after"`
	HeldAfter      int64     `json:"held_after"`
	RefType        string    `json:"ref_type"`
	RefID          string    `json:"ref_id"`
	Note           string    `json:"note"`
	CreatedAt      time.Time `json:"created_at"`
}

type WalletMovementPage struct {
	Data []WalletMovement `json:"data"`
	Meta PageInfo         `json:"meta"`
}

// BankAccount is a payout destination. The number is masked on the way out: the full
// value leaves the system only towards the bank.
type BankAccount struct {
	ID                  id.ID[id.BankAccount] `json:"id"`
	BankCode            string                `json:"bank_code"`
	AccountNumberMasked string                `json:"account_number_masked"`
	AccountHolder       string                `json:"account_holder"`
	IsDefault           bool                  `json:"is_default"`
	CreatedAt           time.Time             `json:"created_at"`
}

// How a cash-out ended, stated rather than left to the reader. Five session statuses collapse
// onto four outcomes, and that mapping is the platform's to own: a client that had to learn it
// would be one release behind every time a status is added.
const (
	WithdrawalAwaitingReview = "awaiting-review"
	WithdrawalApproved       = "approved"
	WithdrawalRejected       = "rejected"
	WithdrawalCancelled      = "cancelled"
)

// Withdrawal is a cash-out: a payment session of its own kind, plus where the money is
// going and what an admin decided.
type Withdrawal struct {
	ID      id.ID[id.PaymentSession] `json:"id"`
	Outcome string                   `json:"outcome"`
	// Status is the underlying session's, kept because a withdrawal *is* a payment session and
	// hiding that would make its id unexplainable. Outcome is what a client renders.
	Status   string `json:"status"`
	Currency string `json:"currency"`
	Amount   int64  `json:"amount"`
	// BankAccount is where the money went, carried whole rather than as an id: it is the one
	// thing a payee checks, and a second round trip for it would be a read per row. Resolved
	// even after they delete the account, because the destination of a settled cash-out is a
	// historical fact and a list that dropped it could not be rendered at all.
	BankAccount BankAccount `json:"bank_account"`
	// ResolvedByID is the admin who decided, and null on one the payee called off themselves.
	ResolvedByID   *id.ID[id.Account] `json:"resolved_by_id"`
	ResolvedAt     *time.Time         `json:"resolved_at"`
	ResolutionNote *string            `json:"resolution_note"`
	CreatedAt      time.Time          `json:"created_at"`
}

type WithdrawalPage struct {
	Data []Withdrawal `json:"data"`
	Meta PageInfo     `json:"meta"`
}

type TaxInfo struct {
	TaxCode            string     `json:"tax_code"`
	TaxCodeType        string     `json:"tax_code_type"`
	LegalName          string     `json:"legal_name"`
	VerificationStatus string     `json:"verification_status"`
	VerifiedAt         *time.Time `json:"verified_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

// PageInfo is the page-paginated meta every finance list answers with. Field for
// field identical to httpx.PageMeta, so a handler converts rather than maps.
type PageInfo struct {
	Page       int    `json:"page"`
	Limit      int    `json:"limit"`
	TotalCount *int64 `json:"total_count"`
}

// --- requests ---

type ListSessionsRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
	// Admin lists every account's sessions. The service refuses it for a caller without
	// the role rather than quietly filtering it away.
	Admin bool `json:"-"`
	// Role is which side of the money the caller is asking about: `payer` is owed or sent,
	// `payee` is received — a seller payout and a refund land on that side. Absent is both.
	Role   string `json:"-" validate:"omitempty,oneof=payer payee"`
	Kind   string `json:"-" validate:"omitempty,oneof=buyer-checkout seller-payout withdrawal"`
	Status string `json:"-" validate:"omitempty,oneof=pending processing success cancelled failed"`
	// AccountID, From and To are the staff reconciliation filters. They are read only on
	// the admin route: on the caller's own list the answer is already their sessions, and
	// an account_id there would be a way to ask about somebody else's money.
	AccountID id.ID[id.Account] `json:"-"`
	From      *time.Time        `json:"-"`
	To        *time.Time        `json:"-"`
	Page      int               `json:"-" validate:"required,min=1"`
	Limit     int               `json:"-" validate:"required,min=1,max=100"`
}

type GetSessionRequest struct {
	ActorID id.ID[id.Account]        `json:"-" validate:"required"`
	ID      id.ID[id.PaymentSession] `json:"-" validate:"required"`
}

// StartPaymentRequest tenders one rail. Amount omitted is the whole outstanding
// balance; passing it splits the session across rails, one call each.
type StartPaymentRequest struct {
	ActorID       id.ID[id.Account]        `json:"-" validate:"required"`
	ID            id.ID[id.PaymentSession] `json:"-" validate:"required"`
	PaymentOption string                   `json:"payment_option" validate:"required,max=100"`
	Amount        *int64                   `json:"amount" validate:"omitempty,gt=0"`
	ReturnURL     string                   `json:"return_url" validate:"omitempty,url,max=2048"`
}

type ListWalletsRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
}

// AdminListWalletsRequest is every currency another account holds. Its own request rather
// than GetWallet with an empty currency, so "all of them" is not a missing field.
type AdminListWalletsRequest struct {
	ActorID   id.ID[id.Account] `json:"-" validate:"required"`
	AccountID id.ID[id.Account] `json:"-" validate:"required"`
}

type GetWalletRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
	// AccountID is whose wallet to read. An admin may name another account; anybody
	// else reading somebody else's balance is refused.
	AccountID id.ID[id.Account] `json:"-" validate:"required"`
	Currency  string            `json:"-" validate:"required,len=3,uppercase"`
}

type ListMovementsRequest struct {
	ActorID  id.ID[id.Account] `json:"-" validate:"required"`
	Currency string            `json:"-" validate:"required,len=3,uppercase"`
	// Kind narrows the ledger to one movement type, which is how "where did my payouts go"
	// is asked without reading every row.
	Kind  string `json:"-" validate:"omitempty,oneof=topup escrow-hold escrow-release payout refund withdrawal fee adjustment"`
	Page  int    `json:"-" validate:"required,min=1"`
	Limit int    `json:"-" validate:"required,min=1,max=100"`
}

type AdjustWalletRequest struct {
	ActorID   id.ID[id.Account] `json:"-" validate:"required"`
	AccountID id.ID[id.Account] `json:"-" validate:"required"`
	Currency  string            `json:"currency" validate:"required,len=3,uppercase"`
	// The deltas are signed and at least one must move, which is what makes a
	// correction in either direction one request rather than two endpoints.
	AvailableDelta int64 `json:"available_delta"`
	HeldDelta      int64 `json:"held_delta"`
	// Reason is mandatory: an adjustment is the only movement with no order behind it,
	// so the note is the whole explanation an audit will ever have.
	Reason string `json:"reason" validate:"required,min=1,max=2000"`
	// IdempotencyKey is mandatory for the same reason: this is the one balance change with
	// nothing else to lose a replay to, so a double-clicked correction would credit twice.
	// Sending the same key again answers the wallet as it stands rather than posting again.
	IdempotencyKey string `json:"idempotency_key" validate:"required,max=200"`
}

type CreateBankAccountRequest struct {
	ActorID       id.ID[id.Account] `json:"-" validate:"required"`
	BankCode      string            `json:"bank_code" validate:"required,max=20"`
	AccountNumber string            `json:"account_number" validate:"required,max=50"`
	AccountHolder string            `json:"account_holder" validate:"required,max=100"`
	IsDefault     bool              `json:"is_default"`
}

type UpdateBankAccountRequest struct {
	ActorID id.ID[id.Account]     `json:"-" validate:"required"`
	ID      id.ID[id.BankAccount] `json:"-" validate:"required"`
	// IsDefault is the only mutable field: a number that changed is a different
	// destination, and a withdrawal already pointing at this row must not follow it.
	IsDefault bool `json:"is_default" validate:"required,eq=true"`
}

type DeleteBankAccountRequest struct {
	ActorID id.ID[id.Account]     `json:"-" validate:"required"`
	ID      id.ID[id.BankAccount] `json:"-" validate:"required"`
}

type ListBankAccountsRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
}

type CreateWithdrawalRequest struct {
	ActorID       id.ID[id.Account]     `json:"-" validate:"required"`
	BankAccountID id.ID[id.BankAccount] `json:"bank_account_id" validate:"required"`
	Currency      string                `json:"currency" validate:"required,len=3,uppercase"`
	Amount        int64                 `json:"amount" validate:"required,gt=0"`
}

type ListWithdrawalsRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
	Admin   bool              `json:"-"`
	Status  string            `json:"-" validate:"omitempty,oneof=pending processing success cancelled failed"`
	Page    int               `json:"-" validate:"required,min=1"`
	Limit   int               `json:"-" validate:"required,min=1,max=100"`
}

type WithdrawalRequest struct {
	ActorID id.ID[id.Account]        `json:"-" validate:"required"`
	ID      id.ID[id.PaymentSession] `json:"-" validate:"required"`
}

// ResolveWithdrawalRequest is the admin's decision. A rejection needs a reason —
// somebody's money did not move and they are owed the why.
type ResolveWithdrawalRequest struct {
	ActorID id.ID[id.Account]        `json:"-" validate:"required"`
	ID      id.ID[id.PaymentSession] `json:"-" validate:"required"`
	Reason  string                   `json:"reason" validate:"max=500"`
	// ProviderRef is the bank's own reference for the transfer, recorded on approval so
	// a payout can be traced outside the platform.
	ProviderRef string `json:"provider_ref" validate:"max=200"`
}

type GetTaxInfoRequest struct {
	ActorID id.ID[id.Account] `json:"-" validate:"required"`
}

type PutTaxInfoRequest struct {
	ActorID     id.ID[id.Account] `json:"-" validate:"required"`
	TaxCode     string            `json:"tax_code" validate:"required,max=14"`
	TaxCodeType string            `json:"tax_code_type" validate:"required,oneof=individual business household"`
	LegalName   string            `json:"legal_name" validate:"required,max=200"`
}

type VerifyTaxInfoRequest struct {
	ActorID   id.ID[id.Account] `json:"-" validate:"required"`
	AccountID id.ID[id.Account] `json:"-" validate:"required"`
	// Status is a verdict, so `pending` is not a choice. A named status rather than a bool
	// because a rejection is a different fact from "not verified yet".
	Status string `json:"status" validate:"required,oneof=verified rejected"`
	// Source is what the verdict was based on — the registry that answered, the document
	// that was read. Mandatory: a verdict nobody can trace is one nobody can revisit.
	Source string `json:"source" validate:"required,max=200"`
	Note   string `json:"note" validate:"max=2000"`
}

// --- service-to-service: what order calls, with no route of its own ---

// OpenCheckoutRequest opens the session that pays for a purchase. Order supplies the
// parties and the total; finance owns everything after that.
type OpenCheckoutRequest struct {
	BuyerID  id.ID[id.Account] `validate:"required"`
	SellerID id.ID[id.Account] `validate:"required"`
	Currency string            `validate:"required,len=3,uppercase"`
	Total    int64             `validate:"required,gt=0"`
	Note     string            `validate:"max=500"`
	// Data is the checkout context order wants back when the money lands — the draft or
	// the offer the sale came from — stored on the session and carried by the event.
	Data []byte
}

// EscrowRequest moves money for one order. IdempotencyKey is the caller's: a retried
// escrow move reuses it and is refused rather than posted twice.
type EscrowRequest struct {
	BuyerID  id.ID[id.Account] `validate:"required"`
	SellerID id.ID[id.Account] `validate:"required"`
	OrderID  id.ID[id.Order]   `validate:"required"`
	Currency string            `validate:"required,len=3,uppercase"`
	// Amount is the goods. Only this is ever held for, released to, or refunded from the
	// seller — it is the sale.
	Amount int64 `validate:"required,gt=0"`
	// ShippingFee is what the buyer also paid, and it is not the seller's: the platform owes it
	// to the carrier. Held with the same movement as the escrow so the pair is one fact, and
	// left out of the release entirely, or a payout would hand the seller the courier's money.
	// Zero on a release. On a refund it is carriage the buyer paid for and did not get — the
	// caller sends it only when the parcel never moved, because a delivery that happened was
	// still bought.
	ShippingFee    int64  `validate:"gte=0"`
	IdempotencyKey string `validate:"required,max=200"`
}

type Service interface {
	// --- payment sessions ---
	// ListOptions is the payment rails, and AdminSaveOption the operator's edit of one. Both are
	// the shared registry surface (`GET /options?category=payment`), served by this module because
	// the rows live in its schema.
	ListOptions(ctx context.Context, req common.ListOptionsRequest) (common.OptionList, error)
	AdminSaveOption(ctx context.Context, req common.SaveOptionRequest) (common.OptionDTO, error)
	ListSessions(ctx context.Context, req ListSessionsRequest) (SessionPage, error)
	GetSession(ctx context.Context, req GetSessionRequest) (Session, error)
	ListSessionTransactions(ctx context.Context, req GetSessionRequest) ([]Transaction, error)
	// StartPayment opens a leg on one rail. The response is not a receipt: the leg is
	// pending until the provider's webhook settles it.
	StartPayment(ctx context.Context, req StartPaymentRequest) (Transaction, error)
	CancelSession(ctx context.Context, req GetSessionRequest) (Session, error)

	// --- wallets ---
	ListWallets(ctx context.Context, req ListWalletsRequest) ([]Wallet, error)
	GetWallet(ctx context.Context, req GetWalletRequest) (Wallet, error)
	ListWalletMovements(ctx context.Context, req ListMovementsRequest) (WalletMovementPage, error)
	// AdminAdjustWallet is the correction of last resort, and the only movement with no
	// order or session behind it.
	// AdminListWallets is every currency an account holds: a support agent looking at a
	// balance dispute does not know which one it is in.
	AdminListWallets(ctx context.Context, req AdminListWalletsRequest) ([]Wallet, error)
	AdminAdjustWallet(ctx context.Context, req AdjustWalletRequest) (Wallet, error)

	// --- bank accounts ---
	ListBankAccounts(ctx context.Context, req ListBankAccountsRequest) ([]BankAccount, error)
	CreateBankAccount(ctx context.Context, req CreateBankAccountRequest) (BankAccount, error)
	UpdateBankAccount(ctx context.Context, req UpdateBankAccountRequest) (BankAccount, error)
	DeleteBankAccount(ctx context.Context, req DeleteBankAccountRequest) error

	// --- withdrawals ---
	CreateWithdrawal(ctx context.Context, req CreateWithdrawalRequest) (Withdrawal, error)
	ListWithdrawals(ctx context.Context, req ListWithdrawalsRequest) (WithdrawalPage, error)
	GetWithdrawal(ctx context.Context, req WithdrawalRequest) (Withdrawal, error)
	CancelWithdrawal(ctx context.Context, req WithdrawalRequest) error
	// AdminApproveWithdrawal releases the money to the bank; AdminRejectWithdrawal
	// returns it to the available balance with a reason.
	AdminApproveWithdrawal(ctx context.Context, req ResolveWithdrawalRequest) (Withdrawal, error)
	AdminRejectWithdrawal(ctx context.Context, req ResolveWithdrawalRequest) (Withdrawal, error)

	// --- tax registration ---
	GetTaxInfo(ctx context.Context, req GetTaxInfoRequest) (TaxInfo, error)
	PutTaxInfo(ctx context.Context, req PutTaxInfoRequest) (TaxInfo, error)
	AdminVerifyTaxInfo(ctx context.Context, req VerifyTaxInfoRequest) (TaxInfo, error)

	// --- called by order, not by a route ---

	// OpenCheckout creates the buyer-checkout session a purchase is paid through.
	OpenCheckout(ctx context.Context, req OpenCheckoutRequest) (Session, error)
	// HoldEscrow moves the buyer's money into the seller's held balance: paid, but not
	// the seller's to spend until the buyer confirms receipt.
	HoldEscrow(ctx context.Context, req EscrowRequest) error
	// ReleaseEscrow makes it spendable — the payout at the end of a completed order.
	ReleaseEscrow(ctx context.Context, req EscrowRequest) error
	// RefundEscrow sends it back to the buyer instead.
	RefundEscrow(ctx context.Context, req EscrowRequest) error
}
