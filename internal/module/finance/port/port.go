// Package port: interface the finance adapter must satisfy.
//
// It speaks in raw int64 keys and domain entities — opaque ids stop at the api
// boundary. Money is the reason several of these take a callback: a balance change
// and the ledger row recording it are one write, and a port that handed the wallet
// out and took it back would let a caller forget the second half.
package port

import (
	"context"
	"time"

	"shopnexus/internal/module/finance/domain"
)

// SessionFilter pages a party's sessions, or the admin's view of all of them.
type SessionFilter struct {
	// AccountID restricts to sessions the account is a party to. Zero is the admin
	// view: every session, whoever it belongs to.
	AccountID int64
	// PayerID and PayeeID narrow to one side of the money. Two fields rather than a role
	// string, because the adapter is the wrong place to know which column a role names.
	PayerID int64
	PayeeID int64
	Kind    string
	Status  string
	// From and To bound `created_at`: inclusive lower, exclusive upper. Nil is unbounded.
	// A reconciliation screen is asking about a period, so the period is the filter.
	From   *time.Time
	To     *time.Time
	Offset int
	Limit  int
}

// MovementFilter pages one wallet's ledger, newest first.
type MovementFilter struct {
	AccountID int64
	Currency  string
	// Kind narrows to one movement type; empty is every kind.
	Kind   string
	Offset int
	Limit  int
}

type Repository interface {
	// --- payment sessions and their legs ---

	// NextSessionID reserves a key before the INSERT: a provider redirect URL embeds
	// the session id, so the app has to know it first.
	NextSessionID(ctx context.Context) (int64, error)
	NextTransactionID(ctx context.Context) (int64, error)
	InsertSession(ctx context.Context, s *domain.Session) error
	// InsertWithdrawal opens a cash-out and takes the money out of reach in one transaction.
	// Two writes would leave a crash window with a pending request nobody funded — which an
	// admin would then approve, sending real money for a balance that was never reduced.
	InsertWithdrawal(ctx context.Context, s *domain.Session, debit Leg) error
	FindSessionByID(ctx context.Context, id int64) (domain.Session, error)
	// SaveSession writes the status and the data, guarded by the statuses the transition is
	// legal from — a session has no version, so that WHERE clause is the only thing stopping
	// two concurrent resolutions from both landing. It answers ErrSessionSettled when the row
	// has moved on, which is what makes a redelivered webhook a no-op rather than a rewrite.
	SaveSession(ctx context.Context, s domain.Session, from []string) error
	ListSessions(ctx context.Context, f SessionFilter) ([]domain.Session, int64, error)

	InsertTransaction(ctx context.Context, t *domain.Transaction) error
	SaveTransaction(ctx context.Context, t domain.Transaction) error
	ListTransactions(ctx context.Context, sessionID int64) ([]domain.Transaction, error)
	// FindTransactionByID is how a webhook finds the leg it is about: the reference the
	// gateway was handed is this id, so it is always in the notification. The provider's own
	// ref is not a lookup key — it is only unique per payment_option, so a shared string
	// across two rails would settle the wrong leg.
	FindTransactionByID(ctx context.Context, id int64) (domain.Transaction, error)

	// --- wallets ---

	FindWallet(ctx context.Context, accountID int64, currency string) (domain.Wallet, error)
	ListWallets(ctx context.Context, accountID int64) ([]domain.Wallet, error)
	ListMovements(ctx context.Context, f MovementFilter) ([]domain.Movement, int64, error)

	// Move applies transfers to wallets inside one transaction, allocating each
	// wallet's next ledger sequence under a row lock and opening a wallet that does
	// not exist yet.
	//
	// A slice rather than one call per leg, because the legs of a checkout — the
	// buyer's debit and the seller's hold — have to land together or not at all. An
	// idempotency key already posted answers domain.ErrMovementAlreadyPosted and
	// nothing moves.
	Move(ctx context.Context, legs []Leg) ([]domain.Movement, error)

	// --- bank accounts ---

	InsertBankAccount(ctx context.Context, b *domain.BankAccount) error
	FindBankAccount(ctx context.Context, id, accountID int64) (domain.BankAccount, error)
	ListBankAccounts(ctx context.Context, accountID int64) ([]domain.BankAccount, error)
	// BankAccountsByIDs resolves the destinations a page of withdrawals names, in one read.
	// Deliberately unscoped and deliberately including soft-deleted rows: an admin's queue spans
	// every payee, and a payee who deletes an account must not make their own settled cash-outs
	// unrenderable — the row is where that money went, whatever they do to it afterwards.
	BankAccountsByIDs(ctx context.Context, ids []int64) (map[int64]domain.BankAccount, error)
	SaveBankAccount(ctx context.Context, b domain.BankAccount) error
	SoftDeleteBankAccount(ctx context.Context, id, accountID int64) error

	// --- tax info ---

	// PutTaxInfo replaces the account's registration: filing again resets the verdict,
	// which is the point of the row being keyed by the account.
	PutTaxInfo(ctx context.Context, t domain.TaxInfo) error
	FindTaxInfo(ctx context.Context, accountID int64) (domain.TaxInfo, error)
	SaveTaxInfo(ctx context.Context, t domain.TaxInfo) error
}

// Leg is one wallet's part of a movement: whose wallet, in which currency, and what
// to do to it.
type Leg struct {
	AccountID int64
	Currency  string
	Transfer  domain.Transfer
}
