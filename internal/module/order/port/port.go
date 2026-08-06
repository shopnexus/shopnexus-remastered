// Package port: the interface the order adapter must satisfy.
//
// It speaks in raw int64 keys and domain entities — opaque ids stop at the api boundary.
package port

import (
	"context"
	"time"

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
	// PartyID matches an order the account is on *either* side of, which is what a caller
	// that named no role is asking for. Not expressible with the two above: they are ANDed,
	// so setting both would answer only the orders somebody sold to themselves.
	PartyID int64
	State   string
	Cursor  CursorFilter
}

// SummaryFilter is one side of the sale over one window. Exactly one of the two ids is set, the same
// way OrderFilter names the side asking.
type SummaryFilter struct {
	BuyerID  int64
	SellerID int64
	From     time.Time
	To       time.Time
	// TZ is an IANA zone name the daily buckets are cut on, already validated by the service — the
	// adapter hands it to Postgres, which is the only thing that can bucket by local date.
	TZ string
}

// OrderCounts is how a window's orders stand now, and what the finished ones came to per currency.
type OrderCounts struct {
	Open      int64
	Completed int64
	Cancelled int64
	Totals    map[string]int64
}

// OrderDay is one day's counts, keyed by the local date the adapter computed.
type OrderDay struct {
	Date      string
	Placed    int64
	Completed int64
}

// OfferFilter pages a party's negotiations.
type OfferFilter struct {
	AccountID int64
	Status    string
	Cursor    CursorFilter
}

// RefundFilter pages a party's refunds; Statuses is the `status=` query a client filters on.
type RefundFilter struct {
	BuyerID  int64
	SellerID int64
	// PartyID matches a refund the account is on either side of — see OrderFilter.PartyID.
	PartyID  int64
	Statuses []string
	Cursor   CursorFilter
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
	ListOffers(ctx context.Context, f OfferFilter) ([]domain.Offer, error)
	// SaveOffer writes the terms and the status, guarded by `from` — the statuses the write may
	// replace, i.e. the one the entity moved out of. A stale read then loses instead of
	// overwriting an acceptance somebody else already recorded.
	SaveOffer(ctx context.Context, o domain.Offer, from []string) error
	// ClaimOfferCheckout takes agreed terms off the table so the buyer can pay for them, before
	// the payment session exists: `WHERE status = 'accepted'` is what makes two concurrent
	// presses of "create order now" open one checkout rather than two.
	ClaimOfferCheckout(ctx context.Context, offerID int64, now time.Time) error
	// ReleaseOfferCheckout hands the claim back when the checkout could not be opened, so the
	// buyer retries rather than renegotiates.
	ReleaseOfferCheckout(ctx context.Context, offerID int64) error
	// AttachOfferSession records which checkout the claim became.
	AttachOfferSession(ctx context.Context, offerID, sessionID int64) error
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
	// InsertTransport opens the shipment with the delivery charge the buyer already paid, frozen
	// on the row: it is what the courier is owed, and it is not the seller's to receive.
	InsertTransport(ctx context.Context, option string, fee int64) (int64, error)
	FindTransport(ctx context.Context, id int64) (domain.Transport, error)
	// BookTransport records the carrier's own reference for a parcel it accepted. Guarded on
	// there being none yet, so a retry cannot overwrite a booking that stands.
	BookTransport(ctx context.Context, transportID int64, data []byte) error
	// FindTransportByRef answers the shipment a carrier's reference belongs to: a webhook
	// carries the courier's id, never ours.
	FindTransportByRef(ctx context.Context, ref string) (domain.Transport, error)
	// UnbookedTransports answers the orders whose parcel no carrier has accepted, which is what
	// makes a failed booking a slower one rather than a lost one.
	UnbookedTransports(ctx context.Context, before time.Time, limit int) ([]int64, error)
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
	// CountOrders and ListOrderDays are the two halves of a summary: one aggregate row and one row
	// per day. Two statements rather than one, because a per-day money column would have to join the
	// lines and then count orders through that join.
	CountOrders(ctx context.Context, f SummaryFilter) (OrderCounts, error)
	ListOrderDays(ctx context.Context, f SummaryFilter) ([]OrderDay, error)
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

	// --- refunds ---
	// InsertRefund opens a case under the same advisory lock ClaimPayout takes, and refuses
	// one on an order that has already been paid out or cancelled: the escrow the refund is
	// about has to still be there when the row lands.
	InsertRefund(ctx context.Context, r *domain.Refund) error
	FindRefund(ctx context.Context, id int64) (domain.Refund, error)
	// LiveRefundOnOrder is the one unsettled refund on an order, if there is one. Callers outside
	// this module name the sale, not the case: one live refund per order is an index, so which row
	// that is stays here.
	LiveRefundOnOrder(ctx context.Context, orderID int64) (domain.Refund, error)
	ListRefunds(ctx context.Context, f RefundFilter) ([]domain.Refund, error)
	// SaveRefund writes the transition, guarded by `from` — the status the entity moved out
	// of. A stale read then loses instead of writing over a move it never saw: an escalation
	// that landed while a sweep held an older copy of the row must not be settled away.
	SaveRefund(ctx context.Context, r domain.Refund, from string) error
	// SaveRefundOutcome writes a refund transition together with the order it closes, in one
	// transaction. Apart, a commit that failed halfway leaves "accepted refund over an open
	// order", which the payout sweep reads as money to hand the seller. Guarded by `from` for
	// the same reason SaveRefund is.
	SaveRefundOutcome(ctx context.Context, r domain.Refund, o *domain.Order, from string) error
	// OverdueRefunds is the timeout pass: every live refund whose deadline has passed. One
	// query advances all three windows, which is what naming a status for the party it
	// waits on buys.
	OverdueRefunds(ctx context.Context, now time.Time, limit int) ([]domain.Refund, error)
	// UnconfirmedOrders is the seller-confirmation timeout pass: paid orders nobody has accepted
	// whose window has closed and which staff have not been asked about yet. The escalation
	// marker is in the WHERE rather than a time window, so a healthy marketplace reads nothing
	// instead of re-reading every sale it ever made.
	UnconfirmedOrders(ctx context.Context, before time.Time, limit int) ([]domain.Order, error)
}
