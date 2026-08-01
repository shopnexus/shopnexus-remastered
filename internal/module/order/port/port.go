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

// CursorFilter pages a list newest first on a (created_at, id) cursor. Not an offset: these
// lists move under the reader, and an offset would skip or repeat a row when they do. Both
// halves, compared as a tuple: rows written in one transaction share `created_at` to the
// microsecond, so the timestamp alone puts part of a group out of reach.
type CursorFilter struct {
	Before   time.Time
	BeforeID int64
	Limit    int
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
	// UnpaidItems is the checkout-expiry sweep: lines older than the checkout window that
	// no order covers. Their reserved units are what would otherwise be lost — nothing else
	// in the schema ever looks at a reservation again.
	UnpaidItems(ctx context.Context, before time.Time, limit int) ([]domain.Item, error)

	// --- transport ---
	InsertTransport(ctx context.Context, option string) (int64, error)
	FindTransport(ctx context.Context, id int64) (domain.Transport, error)
	// SaveTransport advances a shipment. `from` is the status it moves out of, which is the
	// conditional write: a shipment has no version, and two carrier reports arriving at once
	// must not both land.
	SaveTransport(ctx context.Context, t domain.Transport, from string) error

	// --- orders ---
	// CreateOrder writes the order and links the lines to it in one transaction. The
	// unique origin is what makes a redelivered webhook idempotent rather than a second
	// order.
	CreateOrder(ctx context.Context, o *domain.Order, itemIDs []int64) error
	// LinkItems attaches lines to an order that already exists — the half of settling that
	// is still outstanding when an earlier attempt wrote the order and then failed. Its own
	// method rather than a second CreateOrder: that one would re-run the INSERT and lose on
	// the origin constraint, which rolls back the link it was there to make.
	LinkItems(ctx context.Context, orderID int64, itemIDs []int64) error
	FindOrder(ctx context.Context, id int64) (domain.Order, error)
	FindOrderByOrigin(ctx context.Context, origin domain.Origin) (domain.Order, error)
	ListOrders(ctx context.Context, f OrderFilter) ([]domain.Order, error)
	SaveOrder(ctx context.Context, o domain.Order) error
	// OrderItems are the lines an order covers, which is what its total is summed from.
	OrderItems(ctx context.Context, orderID int64) ([]domain.Item, error)
	// PayoutDue is the orders whose escrow window has passed with no refund live. A
	// durable timer per order does this too; the sweep is the safety net.
	PayoutDue(ctx context.Context, now time.Time, limit int) ([]domain.Order, error)
	// ClaimPayout takes the order's advisory lock, re-checks under it that nothing is
	// disputing the money, and completes the order — all in one transaction. PayoutDue's
	// `NOT EXISTS` is a read, so without the lock a refund committed between the select and
	// the write is invisible to both sides and the seller is paid what the buyer is owed.
	// Answers domain.ErrOrderSettled when the order is no longer the payout's to take.
	ClaimPayout(ctx context.Context, o *domain.Order) error
	// ClaimedPayouts is the stranded releases: an order whose outcome was written but whose
	// money never moved. Exactly that set, so a healthy platform reads nothing.
	ClaimedPayouts(ctx context.Context, limit int) ([]domain.Order, error)
	// MarkPayoutReleased records that the escrow reached the seller, which is what takes an
	// order off the stranded list. Its own write rather than part of the claim: the claim has
	// to commit before the money moves, or two passes could both decide they own the escrow.
	MarkPayoutReleased(ctx context.Context, o domain.Order) error

	// --- refunds and disputes ---
	// InsertRefund opens a case under the same advisory lock ClaimPayout takes, and refuses
	// one on an order that has already been paid out or cancelled: the escrow the refund is
	// about has to still be there when the row lands.
	InsertRefund(ctx context.Context, r *domain.Refund) error
	FindRefund(ctx context.Context, id int64) (domain.Refund, error)
	FindOpenRefundByOrder(ctx context.Context, orderID int64) (domain.Refund, error)
	ListRefunds(ctx context.Context, f RefundFilter) ([]domain.Refund, error)
	SaveRefund(ctx context.Context, r domain.Refund) error
	// SaveRefundOutcome writes a refund transition together with the rows it decides — the
	// dispute round that ruled it, the order it closes — in one transaction. Apart, a commit
	// that failed halfway leaves "accepted refund over an open order", which the payout sweep
	// reads as money to hand the seller, or "ruled dispute over a disputed refund", which no
	// path can reach.
	SaveRefundOutcome(ctx context.Context, r domain.Refund, d *domain.Dispute, o *domain.Order) error
	// OverdueRefunds is the timeout pass: every live refund whose deadline has passed. One
	// query advances all three windows, which is what naming a status for the party it
	// waits on buys.
	OverdueRefunds(ctx context.Context, now time.Time, limit int) ([]domain.Refund, error)

	InsertDispute(ctx context.Context, d *domain.Dispute) error
	FindDispute(ctx context.Context, id int64) (domain.Dispute, error)
	ListOpenDisputes(ctx context.Context, f CursorFilter) ([]domain.Dispute, error)

	// FindResources reads this module's own uploaded evidence — receipt photos, refund
	// attachments.
	FindResources(ctx context.Context, ids []int64) ([]common.Resource, error)
}
