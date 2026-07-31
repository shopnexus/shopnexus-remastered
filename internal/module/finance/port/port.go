// Package port: interface the finance adapter must satisfy.
//
// It speaks in raw int64 keys and domain entities — opaque ids stop at the api
// boundary. Money is the reason several of these take a callback: a balance change
// and the ledger row recording it are one write, and a port that handed the wallet
// out and took it back would let a caller forget the second half.
package port

import (
	"context"

	"shopnexus/internal/module/common"
	"shopnexus/internal/module/finance/domain"
)

// SessionFilter pages a party's sessions, or the admin's view of all of them.
type SessionFilter struct {
	// AccountID restricts to sessions the account is a party to. Zero is the admin
	// view: every session, whoever it belongs to.
	AccountID int64
	Kind      string
	Status    string
	Offset    int
	Limit     int
}

// MovementFilter pages one wallet's ledger, newest first.
type MovementFilter struct {
	AccountID int64
	Currency  string
	Offset    int
	Limit     int
}

type Repository interface {
	// --- payment sessions and their legs ---

	// NextSessionID reserves a key before the INSERT: a provider redirect URL embeds
	// the session id, so the app has to know it first.
	NextSessionID(ctx context.Context) (int64, error)
	NextTransactionID(ctx context.Context) (int64, error)
	InsertSession(ctx context.Context, s *domain.Session) error
	FindSessionByID(ctx context.Context, id int64) (domain.Session, error)
	// SaveSession writes the status and the data. A session has no version: every
	// transition is guarded by the status it moves from, checked in the WHERE clause.
	SaveSession(ctx context.Context, s domain.Session) error
	ListSessions(ctx context.Context, f SessionFilter) ([]domain.Session, int64, error)

	InsertTransaction(ctx context.Context, t *domain.Transaction) error
	SaveTransaction(ctx context.Context, t domain.Transaction) error
	ListTransactions(ctx context.Context, sessionID int64) ([]domain.Transaction, error)
	FindTransactionByID(ctx context.Context, id int64) (domain.Transaction, error)
	// FindTransactionByProviderRef is how a webhook finds the leg it is about, and
	// how a redelivery is recognised as one it has already booked.
	FindTransactionByProviderRef(ctx context.Context, ref string) (domain.Transaction, error)

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
	SaveBankAccount(ctx context.Context, b domain.BankAccount) error
	SoftDeleteBankAccount(ctx context.Context, id, accountID int64) error

	// --- tax info ---

	// PutTaxInfo replaces the account's registration: filing again resets the verdict,
	// which is the point of the row being keyed by the account.
	PutTaxInfo(ctx context.Context, t domain.TaxInfo) error
	FindTaxInfo(ctx context.Context, accountID int64) (domain.TaxInfo, error)
	SaveTaxInfo(ctx context.Context, t domain.TaxInfo) error
}

// Options is the payment-rail registry this module reads from its own schema.
// dbx.Options satisfies it; a test fakes it, which is the second caller that earns the
// interface — the alternative is a database in every service test.
type Options interface {
	ListEnabled(ctx context.Context, optionType string) ([]common.Option, error)
}

// Leg is one wallet's part of a movement: whose wallet, in which currency, and what
// to do to it.
type Leg struct {
	AccountID int64
	Currency  string
	Transfer  domain.Transfer
}
