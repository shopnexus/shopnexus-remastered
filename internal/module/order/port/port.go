// Package port: the interface the order adapter must satisfy.
//
// It speaks in raw int64 keys and domain entities — opaque ids stop at the api boundary.
package port

import (
	"context"
	"time"

	"shopnexus/internal/module/common"
	"shopnexus/internal/module/order/domain"
)

// CursorFilter pages a list newest first on a created_at cursor. Not an offset: these
// lists move under the reader, and an offset would skip or repeat a row when they do.
type CursorFilter struct {
	Before time.Time
	Limit  int
}

// ItemFilter is "my purchases", or a seller's paid-but-unordered lines.
type ItemFilter struct {
	BuyerID  int64
	SellerID int64
	// PendingOnly is the window between the money landing and the order being written —
	// the retry list, not an inbox.
	PendingOnly bool
	Cursor      CursorFilter
}

// OrderFilter pages orders as buyer or as seller. State is derived from the two outcome
// timestamps, so it is a predicate here rather than a column.
type OrderFilter struct {
	BuyerID  int64
	SellerID int64
	State    string
	Cursor   CursorFilter
}

// OfferFilter pages a party's negotiations.
type OfferFilter struct {
	AccountID int64
	Status    string
	Cursor    CursorFilter
}

// RefundFilter pages a party's refunds; Statuses is how the moderator queue is read.
type RefundFilter struct {
	BuyerID  int64
	SellerID int64
	Statuses []string
	Cursor   CursorFilter
}

// Options is the carrier registry this module reads from its own schema. dbx.Options
// satisfies it; a test fakes it, which is the second caller that earns the interface.
type Options interface {
	ListEnabled(ctx context.Context, optionType string) ([]common.Option, error)
}

type Repository interface {
	// --- cart ---
	// UpsertCartItem adds or tops up: the row is keyed by (account, variant), so adding
	// the same variant twice changes the quantity rather than stacking.
	UpsertCartItem(ctx context.Context, c *domain.CartItem) error
	FindCartItem(ctx context.Context, id, accountID int64) (domain.CartItem, error)
	ListCartItems(ctx context.Context, accountID int64) ([]domain.CartItem, error)
	SaveCartItem(ctx context.Context, c domain.CartItem) error
	DeleteCartItem(ctx context.Context, id, accountID int64) error

	// --- drafts ---
	InsertDraft(ctx context.Context, d *domain.Draft) error
	FindDraft(ctx context.Context, id, buyerID int64) (domain.Draft, error)
	ListDrafts(ctx context.Context, buyerID int64, f CursorFilter) ([]domain.Draft, error)
	SaveDraft(ctx context.Context, d domain.Draft) error
	// ExpiredDrafts is what the expiry pass reads. A durable timer per draft would do the
	// same job; this exists so a sweep can catch anything a timer lost.
	ExpiredDrafts(ctx context.Context, now time.Time, limit int) ([]domain.Draft, error)

	// --- offers ---
	InsertOffer(ctx context.Context, o *domain.Offer) error
	FindOffer(ctx context.Context, id int64) (domain.Offer, error)
	// FindActiveOffer answers the one active negotiation on a variant, which is what makes
	// opening a second one a conflict rather than a duplicate.
	FindActiveOffer(ctx context.Context, buyerID, variantID int64) (domain.Offer, error)
	ListOffers(ctx context.Context, f OfferFilter) ([]domain.Offer, error)
	SaveOffer(ctx context.Context, o domain.Offer) error
	ExpiredOffers(ctx context.Context, now time.Time, limit int) ([]domain.Offer, error)

	// --- items ---
	// InsertItems writes a checkout's lines in one transaction: a partial checkout is a
	// buyer charged for something they did not get a row for.
	InsertItems(ctx context.Context, items []*domain.Item) error
	FindItem(ctx context.Context, id int64) (domain.Item, error)
	ListItems(ctx context.Context, f ItemFilter) ([]domain.Item, error)
	SaveItem(ctx context.Context, i domain.Item) error
	// ItemsByPaymentSession is the webhook's first lookup: which lines did this session
	// pay for.
	ItemsByPaymentSession(ctx context.Context, sessionID int64) ([]domain.Item, error)

	// --- transport ---
	InsertTransport(ctx context.Context, option string) (int64, error)
	FindTransport(ctx context.Context, id int64) (domain.Transport, error)
	SaveTransport(ctx context.Context, t domain.Transport) error

	// --- orders ---
	// CreateOrder writes the order and links the lines to it in one transaction. The
	// unique origin is what makes a redelivered webhook idempotent rather than a second
	// order.
	CreateOrder(ctx context.Context, o *domain.Order, itemIDs []int64) error
	FindOrder(ctx context.Context, id int64) (domain.Order, error)
	FindOrderByOrigin(ctx context.Context, origin domain.Origin) (domain.Order, error)
	ListOrders(ctx context.Context, f OrderFilter) ([]domain.Order, error)
	SaveOrder(ctx context.Context, o domain.Order) error
	// OrderItems are the lines an order covers, which is what its total is summed from.
	OrderItems(ctx context.Context, orderID int64) ([]domain.Item, error)
	// PayoutDue is the orders whose escrow window has passed with no refund live. A
	// durable timer per order does this too; the sweep is the safety net.
	PayoutDue(ctx context.Context, now time.Time, limit int) ([]domain.Order, error)

	// --- refunds and disputes ---
	InsertRefund(ctx context.Context, r *domain.Refund) error
	FindRefund(ctx context.Context, id int64) (domain.Refund, error)
	FindOpenRefundByOrder(ctx context.Context, orderID int64) (domain.Refund, error)
	ListRefunds(ctx context.Context, f RefundFilter) ([]domain.Refund, error)
	SaveRefund(ctx context.Context, r domain.Refund) error
	// OverdueRefunds is the timeout pass: every live refund whose deadline has passed. One
	// query advances all three windows, which is what naming a status for the party it
	// waits on buys.
	OverdueRefunds(ctx context.Context, now time.Time, limit int) ([]domain.Refund, error)

	InsertDispute(ctx context.Context, d *domain.Dispute) error
	FindDispute(ctx context.Context, id int64) (domain.Dispute, error)
	ListOpenDisputes(ctx context.Context, f CursorFilter) ([]domain.Dispute, error)
	SaveDispute(ctx context.Context, d domain.Dispute) error

	// FindResources reads this module's own uploaded evidence — receipt photos, refund
	// attachments.
	FindResources(ctx context.Context, ids []int64) ([]common.Resource, error)
}
