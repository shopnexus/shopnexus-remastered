// Package financeapi is the published contract of the finance service: payment
// sessions, wallet balances, and tax info. All money primitives live in this
// module so an escrow move stays atomic.
package financeapi

import (
	"context"
	"time"

	"shopnexus/internal/shared/id"
)

type Session struct {
	ID          id.ID[id.PaymentSession] `json:"id"`
	Kind        string                   `json:"kind"`
	Status      string                   `json:"status"`
	Currency    string                   `json:"currency"`
	TotalAmount int64                    `json:"total_amount"`
	Note        string                   `json:"note,omitempty"`
	CreatedAt   time.Time                `json:"created_at"`
	PaidAt      *time.Time               `json:"paid_at,omitempty"`
	ExpiredAt   time.Time                `json:"expired_at"`
}

type Wallet struct {
	AccountID        id.ID[id.Account] `json:"account_id"`
	Currency         string            `json:"currency"`
	AvailableBalance int64             `json:"available_balance"`
	HeldBalance      int64             `json:"held_balance"`
}

type CreateSessionRequest struct {
	Kind        string            `json:"kind" validate:"required,oneof=buyer-checkout seller-confirmation-fee seller-payout withdrawal"`
	FromID      id.ID[id.Account] `json:"-"`               // taken from the token
	ToID        id.ID[id.Account] `json:"to_id,omitempty"` // zero = system
	Note        string            `json:"note,omitempty"`
	Currency    string            `json:"currency" validate:"required,len=3"`
	TotalAmount int64             `json:"total_amount" validate:"required,gt=0"`
	Data        []byte            `json:"data,omitempty"`
}

type GetSessionRequest struct {
	ID id.ID[id.PaymentSession] `validate:"required"`
}

// A wallet is keyed by account *and* currency, so the currency is part of the
// request: an account can hold several balances.
type GetWalletRequest struct {
	AccountID id.ID[id.Account] `validate:"required"`
	Currency  string            `validate:"required,len=3"`
}

type Service interface {
	CreateSession(ctx context.Context, req CreateSessionRequest) (Session, error)
	GetSession(ctx context.Context, req GetSessionRequest) (Session, error)
	GetWallet(ctx context.Context, req GetWalletRequest) (Wallet, error)
}
