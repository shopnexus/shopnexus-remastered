package order_test

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"time"

	"shopnexus/internal/module/common"
	"shopnexus/internal/module/order/domain"
	"shopnexus/internal/module/order/port"
	"shopnexus/internal/provider/storage"
)

// fakeRepo is an in-memory port.Repository. It enforces the constraints the schema does — one
// order per origin, one active negotiation per (buyer, variant), one live refund per order,
// and every transition guarded by the status it moves from — because those are the rules the
// service's behaviour rests on.
type fakeRepo struct {
	nextID    int64
	carts     map[int64]domain.CartItem
	drafts    map[int64]domain.Draft
	offers    map[int64]domain.Offer
	items     map[int64]domain.Item
	orders    map[int64]domain.Order
	shipments map[int64]domain.Transport
	refunds   map[int64]domain.Refund
	disputes  map[int64]domain.Dispute
	options   []common.Option
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		carts: map[int64]domain.CartItem{}, drafts: map[int64]domain.Draft{},
		offers: map[int64]domain.Offer{}, items: map[int64]domain.Item{},
		orders: map[int64]domain.Order{}, shipments: map[int64]domain.Transport{},
		refunds: map[int64]domain.Refund{}, disputes: map[int64]domain.Dispute{},
		// Two carriers so a quote list is a list, and a disabled one so "enabled" means something.
		options: []common.Option{
			{ID: "ghn-express", Name: "GHN Express", Type: common.OptionTypeTransport, IsEnabled: true},
			{ID: "vtp-standard", Name: "Viettel Post", Type: common.OptionTypeTransport, IsEnabled: true},
			{ID: "retired-courier", Name: "Retired", Type: common.OptionTypeTransport},
		},
	}
}

func (f *fakeRepo) id() int64 {
	f.nextID++
	return f.nextID
}

// pastCursor is the bound every newest-first list here applies, as the SQL tuple comparison
// does: `(created_at, id) < (@before, @before_id)`. The pair, not the timestamp alone — rows
// written in one transaction share `created_at` exactly, and a bound on that half put the rest
// of such a group permanently out of reach.
func pastCursor(f port.CursorFilter, at time.Time, id int64) bool {
	if f.Before.IsZero() {
		return true
	}
	if at.Before(f.Before) {
		return true
	}
	return at.Equal(f.Before) && id < f.BeforeID
}

// beyondCursor is the same for a list read oldest first — the moderator queue.
func beyondCursor(f port.CursorFilter, at time.Time, id int64) bool {
	if f.Before.IsZero() {
		return true
	}
	if at.After(f.Before) {
		return true
	}
	return at.Equal(f.Before) && id > f.BeforeID
}

var _ port.Repository = (*fakeRepo)(nil)

// ListEnabled makes the fake its own carrier registry, so a service test needs no second fake.
func (f *fakeRepo) ListEnabled(_ context.Context, optionType string) ([]common.Option, error) {
	var out []common.Option
	for _, o := range f.options {
		if o.Type == optionType && o.IsEnabled {
			out = append(out, o)
		}
	}
	return out, nil
}

// --- cart ---

func (f *fakeRepo) UpsertCartItem(_ context.Context, c *domain.CartItem) error {
	for key, stored := range f.carts {
		if stored.AccountID == c.AccountID && stored.VariantID == c.VariantID {
			// Keyed by (account, variant): adding twice tops the row up.
			stored.Quantity += c.Quantity
			f.carts[key] = stored
			*c = stored
			return nil
		}
	}
	c.ID = f.id()
	c.CreatedAt = time.Now()
	f.carts[c.ID] = *c
	return nil
}

func (f *fakeRepo) FindCartItem(_ context.Context, cartItemID, accountID int64) (domain.CartItem, error) {
	c, ok := f.carts[cartItemID]
	if !ok || c.AccountID != accountID {
		return domain.CartItem{}, domain.ErrCartItemNotFound
	}
	return c, nil
}

func (f *fakeRepo) ListCartItems(_ context.Context, accountID int64) ([]domain.CartItem, error) {
	var out []domain.CartItem
	for _, c := range f.carts {
		if c.AccountID == accountID {
			out = append(out, c)
		}
	}
	slices.SortFunc(out, func(a, b domain.CartItem) int { return int(b.ID - a.ID) })
	return out, nil
}

func (f *fakeRepo) SaveCartItem(_ context.Context, c domain.CartItem) error {
	if _, ok := f.carts[c.ID]; !ok {
		return domain.ErrCartItemNotFound
	}
	f.carts[c.ID] = c
	return nil
}

func (f *fakeRepo) DeleteCartItem(_ context.Context, cartItemID, accountID int64) error {
	c, ok := f.carts[cartItemID]
	if !ok || c.AccountID != accountID {
		return domain.ErrCartItemNotFound
	}
	delete(f.carts, cartItemID)
	return nil
}

// --- drafts ---

func (f *fakeRepo) InsertDraft(_ context.Context, d *domain.Draft) error {
	d.ID = f.id()
	d.CreatedAt = time.Now()
	f.drafts[d.ID] = *d
	return nil
}

func (f *fakeRepo) FindDraft(_ context.Context, draftID, buyerID int64) (domain.Draft, error) {
	d, ok := f.drafts[draftID]
	if !ok || d.BuyerID != buyerID {
		return domain.Draft{}, domain.ErrDraftNotFound
	}
	return d, nil
}

func (f *fakeRepo) ListDrafts(_ context.Context, buyerID int64, filter port.CursorFilter) ([]domain.Draft, error) {
	var out []domain.Draft
	for _, d := range f.drafts {
		if d.BuyerID == buyerID && pastCursor(filter, d.CreatedAt, d.ID) {
			out = append(out, d)
		}
	}
	slices.SortFunc(out, func(a, b domain.Draft) int { return int(b.ID - a.ID) })
	return out[:min(filter.Limit, len(out))], nil
}

// SaveDraft only closes a live one, as `WHERE cancelled_at IS NULL` does.
func (f *fakeRepo) SaveDraft(_ context.Context, d domain.Draft) error {
	stored, ok := f.drafts[d.ID]
	if !ok || stored.CancelledAt != nil {
		return domain.ErrDraftSettled
	}
	f.drafts[d.ID] = d
	return nil
}

func (f *fakeRepo) ExpiredDrafts(_ context.Context, now time.Time, limit int) ([]domain.Draft, error) {
	var out []domain.Draft
	for _, d := range f.drafts {
		if d.CancelledAt == nil && d.ValidUntil.Before(now) {
			out = append(out, d)
		}
	}
	return out[:min(limit, len(out))], nil
}

// --- offers ---

func (f *fakeRepo) InsertOffer(_ context.Context, o *domain.Offer) error {
	// One active negotiation per (buyer, variant): the terms are revised in place.
	for _, stored := range f.offers {
		if stored.BuyerID == o.BuyerID && stored.VariantID == o.VariantID && stored.Status == domain.OfferActive {
			return domain.ErrOfferAlreadyOpen
		}
	}
	o.ID = f.id()
	o.CreatedAt = time.Now()
	f.offers[o.ID] = *o
	return nil
}

func (f *fakeRepo) FindOffer(_ context.Context, offerID int64) (domain.Offer, error) {
	o, ok := f.offers[offerID]
	if !ok {
		return domain.Offer{}, domain.ErrOfferNotFound
	}
	return o, nil
}

func (f *fakeRepo) FindActiveOffer(_ context.Context, buyerID, variantID int64) (domain.Offer, error) {
	for _, o := range f.offers {
		if o.BuyerID == buyerID && o.VariantID == variantID && o.Status == domain.OfferActive {
			return o, nil
		}
	}
	return domain.Offer{}, domain.ErrOfferNotFound
}

func (f *fakeRepo) ListOffers(_ context.Context, filter port.OfferFilter) ([]domain.Offer, error) {
	var out []domain.Offer
	for _, o := range f.offers {
		if !o.Involves(filter.AccountID) {
			continue
		}
		if filter.Status != "" && o.Status != filter.Status {
			continue
		}
		if !pastCursor(filter.Cursor, o.CreatedAt, o.ID) {
			continue
		}
		out = append(out, o)
	}
	slices.SortFunc(out, func(a, b domain.Offer) int { return int(b.ID - a.ID) })
	return out[:min(filter.Cursor.Limit, len(out))], nil
}

// SaveOffer moves a negotiation, guarded by the statuses the caller says it moves out of, exactly
// as `status = ANY(@from)` does: a write from a stale read loses instead of overwriting somebody
// else's acceptance.
func (f *fakeRepo) SaveOffer(_ context.Context, o domain.Offer, from []string) error {
	stored, ok := f.offers[o.ID]
	if !ok || !slices.Contains(from, stored.Status) {
		return domain.ErrOfferSettled
	}
	f.offers[o.ID] = o
	return nil
}

// ClaimOfferCheckout takes the terms off the table before the money is asked for, and the status
// is the claim: the second of two concurrent presses finds it gone.
func (f *fakeRepo) ClaimOfferCheckout(_ context.Context, offerID int64, now time.Time) error {
	stored, ok := f.offers[offerID]
	if !ok || stored.Status != domain.OfferAccepted || !now.Before(stored.ExpiresAt) {
		return domain.ErrOfferSettled
	}
	stored.Status = domain.OfferCheckedOut
	f.offers[offerID] = stored
	return nil
}

func (f *fakeRepo) ReleaseOfferCheckout(_ context.Context, offerID int64) error {
	stored, ok := f.offers[offerID]
	if !ok || stored.Status != domain.OfferCheckedOut || stored.PaymentSessionID != nil {
		return nil
	}
	stored.Status = domain.OfferAccepted
	f.offers[offerID] = stored
	return nil
}

func (f *fakeRepo) AttachOfferSession(_ context.Context, offerID, sessionID int64) error {
	stored, ok := f.offers[offerID]
	if !ok || stored.Status != domain.OfferCheckedOut || stored.PaymentSessionID != nil {
		return domain.ErrOfferSettled
	}
	stored.PaymentSessionID = &sessionID
	f.offers[offerID] = stored
	return nil
}

func (f *fakeRepo) ExpiredOffers(_ context.Context, now time.Time, limit int) ([]domain.Offer, error) {
	var out []domain.Offer
	for _, o := range f.offers {
		// An accepted offer nobody checked out expires too: the frozen price had a short window.
		// A checked-out one does not — its clock is the payment session's.
		if (o.Status == domain.OfferActive || o.Status == domain.OfferAccepted) && o.ExpiresAt.Before(now) {
			out = append(out, o)
		}
	}
	return out[:min(limit, len(out))], nil
}

// --- items ---

func (f *fakeRepo) InsertItems(_ context.Context, items []*domain.Item) error {
	for _, i := range items {
		i.ID = f.id()
		i.CreatedAt = time.Now()
		f.items[i.ID] = *i
	}
	return nil
}

func (f *fakeRepo) FindItem(_ context.Context, itemID int64) (domain.Item, error) {
	i, ok := f.items[itemID]
	if !ok {
		return domain.Item{}, domain.ErrItemNotFound
	}
	return i, nil
}

func (f *fakeRepo) ListItems(_ context.Context, filter port.ItemFilter) ([]domain.Item, error) {
	var out []domain.Item
	for _, i := range f.items {
		if filter.BuyerID != 0 && i.BuyerID != filter.BuyerID {
			continue
		}
		if filter.SellerID != 0 && i.SellerID != filter.SellerID {
			continue
		}
		if filter.PendingOnly && (i.OrderID != nil || !i.Live()) {
			continue
		}
		if !pastCursor(filter.Cursor, i.CreatedAt, i.ID) {
			continue
		}
		out = append(out, i)
	}
	slices.SortFunc(out, func(a, b domain.Item) int { return int(b.ID - a.ID) })
	return out[:min(filter.Cursor.Limit, len(out))], nil
}

// SaveItem only cancels a line no order covers, as the guard does.
func (f *fakeRepo) SaveItem(_ context.Context, i domain.Item) error {
	stored, ok := f.items[i.ID]
	if !ok || stored.OrderID != nil || !stored.Live() {
		return domain.ErrItemNotCancellable
	}
	f.items[i.ID] = i
	return nil
}

func (f *fakeRepo) ItemsByPaymentSession(_ context.Context, sessionID int64) ([]domain.Item, error) {
	var out []domain.Item
	for _, i := range f.items {
		if i.PaymentSessionID == sessionID {
			out = append(out, i)
		}
	}
	slices.SortFunc(out, func(a, b domain.Item) int { return int(a.ID - b.ID) })
	return out, nil
}

func (f *fakeRepo) UnpaidItems(_ context.Context, before time.Time, limit int) ([]domain.Item, error) {
	var out []domain.Item
	for _, i := range f.items {
		if i.OrderID == nil && i.Live() && i.CreatedAt.Before(before) {
			out = append(out, i)
		}
	}
	slices.SortFunc(out, func(a, b domain.Item) int { return int(a.ID - b.ID) })
	return out[:min(limit, len(out))], nil
}

// --- transport ---

func (f *fakeRepo) InsertTransport(_ context.Context, option string, fee int64) (int64, error) {
	t := domain.Transport{
		ID: f.id(), Option: option, Status: domain.TransportPending, Fee: fee,
		CreatedAt: time.Now(),
	}
	f.shipments[t.ID] = t
	return t.ID, nil
}

func (f *fakeRepo) FindTransport(_ context.Context, transportID int64) (domain.Transport, error) {
	t, ok := f.shipments[transportID]
	if !ok {
		return domain.Transport{}, domain.ErrOrderNotFound
	}
	return t, nil
}

// BookTransport records what the courier gave back, guarded on there being no booking yet — as
// `data->>'provider_ref' IS NULL` is: a retry cannot replace a reference that stands.
func (f *fakeRepo) BookTransport(_ context.Context, transportID int64, data []byte) error {
	stored, ok := f.shipments[transportID]
	if !ok || stored.Status != domain.TransportPending || stored.Booked() {
		return domain.ErrTransportSettled
	}
	stored.Data = data
	f.shipments[transportID] = stored
	return nil
}

// FindTransportByRef is the webhook's lookup: a courier reports on its own id.
func (f *fakeRepo) FindTransportByRef(_ context.Context, ref string) (domain.Transport, error) {
	for _, t := range f.shipments {
		if t.Booked() && bookingRef(t) == ref {
			return t, nil
		}
	}
	return domain.Transport{}, domain.ErrTransportNotFound
}

func bookingRef(t domain.Transport) string {
	var data struct {
		ProviderRef string `json:"provider_ref"`
	}
	if json.Unmarshal(t.Data, &data) != nil {
		return ""
	}
	return data.ProviderRef
}

// UnbookedTransports is the retry list: the orders whose parcel no carrier has accepted.
func (f *fakeRepo) UnbookedTransports(_ context.Context, before time.Time, limit int) ([]int64, error) {
	var out []int64
	for _, o := range f.orders {
		if o.Settled() {
			continue
		}
		t, ok := f.shipments[o.TransportID]
		if !ok || t.Booked() || t.Status != domain.TransportPending || !t.CreatedAt.Before(before) {
			continue
		}
		out = append(out, o.ID)
	}
	slices.Sort(out)
	return out[:min(limit, len(out))], nil
}

// SaveTransport only applies to the status it read, as `WHERE status = @from` does: two carrier
// reports arriving at once cannot both land.
func (f *fakeRepo) SaveTransport(_ context.Context, t domain.Transport, from string) error {
	stored, ok := f.shipments[t.ID]
	if !ok || stored.Status != from {
		return domain.ErrTransportSettled
	}
	f.shipments[t.ID] = t
	return nil
}

// --- orders ---

// CreateOrder only ever inserts, and loses on the origin — which is what makes a redelivered
// webhook a conflict the caller resolves by reading the winner's row, rather than a second order.
// The real one runs the link inside the same transaction, so a lost origin rolls the link back
// too: that is why a resumed settlement calls LinkItems instead.
func (f *fakeRepo) CreateOrder(_ context.Context, o *domain.Order, itemIDs []int64) error {
	for _, stored := range f.orders {
		if sameOrigin(stored, *o) {
			return domain.ErrOrderSettled
		}
	}
	o.ID = f.id()
	o.CreatedAt = time.Now()
	f.orders[o.ID] = *o
	return f.LinkItems(context.Background(), o.ID, itemIDs)
}

// LinkItems claims only lines no order covers, as the guard does.
func (f *fakeRepo) LinkItems(_ context.Context, orderID int64, itemIDs []int64) error {
	for _, itemID := range itemIDs {
		i, ok := f.items[itemID]
		if !ok || i.OrderID != nil || !i.Live() {
			continue
		}
		i.OrderID = &orderID
		f.items[itemID] = i
	}
	return nil
}

func sameOrigin(a, b domain.Order) bool {
	if a.DraftID != nil && b.DraftID != nil {
		return *a.DraftID == *b.DraftID
	}
	if a.OfferID != nil && b.OfferID != nil {
		return *a.OfferID == *b.OfferID
	}
	return false
}

func (f *fakeRepo) FindOrder(_ context.Context, orderID int64) (domain.Order, error) {
	o, ok := f.orders[orderID]
	if !ok {
		return domain.Order{}, domain.ErrOrderNotFound
	}
	return o, nil
}

func (f *fakeRepo) FindOrderByOrigin(_ context.Context, origin domain.Origin) (domain.Order, error) {
	probe := domain.Order{DraftID: origin.DraftID, OfferID: origin.OfferID}
	for _, o := range f.orders {
		if sameOrigin(o, probe) {
			return o, nil
		}
	}
	return domain.Order{}, domain.ErrOrderNotFound
}

func (f *fakeRepo) ListOrders(_ context.Context, filter port.OrderFilter) ([]domain.Order, error) {
	var out []domain.Order
	for _, o := range f.orders {
		if filter.BuyerID != 0 && o.BuyerID != filter.BuyerID {
			continue
		}
		if filter.SellerID != 0 && o.SellerID != filter.SellerID {
			continue
		}
		if filter.State != "" && o.State() != filter.State {
			continue
		}
		if !pastCursor(filter.Cursor, o.CreatedAt, o.ID) {
			continue
		}
		out = append(out, o)
	}
	slices.SortFunc(out, func(a, b domain.Order) int { return int(b.ID - a.ID) })
	return out[:min(filter.Cursor.Limit, len(out))], nil
}

// SaveOrder only moves an open one: two payouts, or a payout racing a cancellation, cannot
// both land.
func (f *fakeRepo) SaveOrder(_ context.Context, o domain.Order) error {
	stored, ok := f.orders[o.ID]
	if !ok || stored.Settled() {
		return domain.ErrOrderSettled
	}
	f.orders[o.ID] = o
	return nil
}

func (f *fakeRepo) OrderItems(_ context.Context, orderID int64) ([]domain.Item, error) {
	var out []domain.Item
	for _, i := range f.items {
		if i.OrderID != nil && *i.OrderID == orderID {
			out = append(out, i)
		}
	}
	slices.SortFunc(out, func(a, b domain.Item) int { return int(a.ID - b.ID) })
	return out, nil
}

func (f *fakeRepo) PayoutDue(_ context.Context, now time.Time, limit int) ([]domain.Order, error) {
	var out []domain.Order
	for _, o := range f.orders {
		if o.Settled() || o.ReceivedAt == nil {
			continue
		}
		due := o.PayoutDue()
		if due == nil || due.After(now) {
			continue
		}
		if f.refundClaims(o.ID) {
			continue
		}
		out = append(out, o)
	}
	return out[:min(limit, len(out))], nil
}

// refundClaims reports whether any refund still has a claim on the order's escrow. Only a
// rejection and a withdrawal give it up; 'accepted' means the buyer has already been paid, so
// treating it as settled-and-done is how the sweep used to pay the seller as well.
func (f *fakeRepo) refundClaims(orderID int64) bool {
	for _, r := range f.refunds {
		if r.OrderID != orderID {
			continue
		}
		if r.Status != domain.RefundRejected && r.Status != domain.RefundCancelled {
			return true
		}
	}
	return false
}

// ClaimPayout is the locked decision: the same live-refund question, re-read, and the completion
// written under it. The fake is single-threaded, so what it models is the guard rather than the
// lock — a claim over an order a refund now covers has to lose.
func (f *fakeRepo) ClaimPayout(_ context.Context, o *domain.Order) error {
	stored, ok := f.orders[o.ID]
	if !ok || stored.Settled() || stored.ReceivedAt == nil || f.refundClaims(o.ID) {
		return domain.ErrOrderSettled
	}
	stored.CompletedAt = new(time.Now())
	f.orders[o.ID] = stored
	o.CompletedAt = stored.CompletedAt
	return nil
}

// ClaimedPayouts is exactly the stranded set, as the real query is: a completed order whose
// release never landed. A fake that returned every completed order would hide the whole point of
// the marker — that a healthy platform reads nothing here.
func (f *fakeRepo) ClaimedPayouts(_ context.Context, limit int) ([]domain.Order, error) {
	var out []domain.Order
	for _, o := range f.orders {
		if o.CompletedAt == nil || o.PayoutReleasedAt != nil || o.CancelledAt != nil {
			continue
		}
		out = append(out, o)
	}
	slices.SortFunc(out, func(a, b domain.Order) int { return int(a.ID - b.ID) })
	return out[:min(limit, len(out))], nil
}

// MarkPayoutReleased is guarded by the column being NULL, as the real one is.
func (f *fakeRepo) MarkPayoutReleased(_ context.Context, o domain.Order) error {
	stored, ok := f.orders[o.ID]
	if !ok || stored.PayoutReleasedAt != nil {
		return nil
	}
	stored.PayoutReleasedAt = o.PayoutReleasedAt
	f.orders[o.ID] = stored
	return nil
}

// --- refunds and disputes ---

// InsertRefund holds both guards the real one does: one live refund per order, and an order
// whose escrow is still there to argue over — the row lands under the same lock the payout claim
// takes, so a case opened over an order already paid out is refused rather than written.
func (f *fakeRepo) InsertRefund(_ context.Context, r *domain.Refund) error {
	o, ok := f.orders[r.OrderID]
	if !ok || o.Settled() {
		return domain.ErrRefundNotDue
	}
	for _, stored := range f.refunds {
		if stored.OrderID == r.OrderID && !stored.Settled() {
			return domain.ErrRefundAlreadyOpen
		}
	}
	r.ID = f.id()
	r.CreatedAt = time.Now()
	f.refunds[r.ID] = *r
	return nil
}

func (f *fakeRepo) FindRefund(_ context.Context, refundID int64) (domain.Refund, error) {
	r, ok := f.refunds[refundID]
	if !ok {
		return domain.Refund{}, domain.ErrRefundNotFound
	}
	return r, nil
}

func (f *fakeRepo) FindOpenRefundByOrder(_ context.Context, orderID int64) (domain.Refund, error) {
	for _, r := range f.refunds {
		if r.OrderID == orderID && !r.Settled() {
			return r, nil
		}
	}
	return domain.Refund{}, domain.ErrRefundNotFound
}

func (f *fakeRepo) ListRefunds(ctx context.Context, filter port.RefundFilter) ([]domain.Refund, error) {
	var out []domain.Refund
	for _, r := range f.refunds {
		if filter.BuyerID != 0 && r.BuyerID != filter.BuyerID {
			continue
		}
		if filter.SellerID != 0 {
			o, err := f.FindOrder(ctx, r.OrderID)
			if err != nil || o.SellerID != filter.SellerID {
				continue
			}
		}
		if len(filter.Statuses) > 0 && !slices.Contains(filter.Statuses, r.Status) {
			continue
		}
		if !pastCursor(filter.Cursor, r.CreatedAt, r.ID) {
			continue
		}
		out = append(out, r)
	}
	slices.SortFunc(out, func(a, b domain.Refund) int { return int(b.ID - a.ID) })
	return out[:min(filter.Cursor.Limit, len(out))], nil
}

// SaveRefund only moves a live one, as the status guard does.
func (f *fakeRepo) SaveRefund(_ context.Context, r domain.Refund) error {
	stored, ok := f.refunds[r.ID]
	if !ok || stored.Settled() {
		return domain.ErrRefundSettled
	}
	f.refunds[r.ID] = r
	return nil
}

func (f *fakeRepo) OverdueRefunds(_ context.Context, now time.Time, limit int) ([]domain.Refund, error) {
	var out []domain.Refund
	for _, r := range f.refunds {
		if r.Settled() || r.DeadlineAt == nil || r.DeadlineAt.After(now) {
			continue
		}
		out = append(out, r)
	}
	return out[:min(limit, len(out))], nil
}

func (f *fakeRepo) InsertDispute(_ context.Context, d *domain.Dispute) error {
	for _, stored := range f.disputes {
		if stored.RefundID == d.RefundID && stored.Round == d.Round {
			return domain.ErrDisputeSettled
		}
	}
	d.ID = f.id()
	d.CreatedAt = time.Now()
	f.disputes[d.ID] = *d
	return nil
}

func (f *fakeRepo) FindDispute(_ context.Context, disputeID int64) (domain.Dispute, error) {
	d, ok := f.disputes[disputeID]
	if !ok {
		return domain.Dispute{}, domain.ErrDisputeNotFound
	}
	return d, nil
}

func (f *fakeRepo) ListOpenDisputes(_ context.Context, filter port.CursorFilter) ([]domain.Dispute, error) {
	var out []domain.Dispute
	for _, d := range f.disputes {
		if d.Status == domain.DisputeOpen && beyondCursor(filter, d.CreatedAt, d.ID) {
			out = append(out, d)
		}
	}
	slices.SortFunc(out, func(a, b domain.Dispute) int { return int(a.ID - b.ID) })
	return out[:min(filter.Limit, len(out))], nil
}

// SaveRefundOutcome writes all three rows or none, as the transaction does: a ruled round over a
// still-disputed refund and a settled refund over an open order are both states nothing can get
// out of, so a half-applied outcome must not be reachable here either.
func (f *fakeRepo) SaveRefundOutcome(ctx context.Context, r domain.Refund, d *domain.Dispute, o *domain.Order) error {
	stored, ok := f.refunds[r.ID]
	if !ok || stored.Settled() {
		return domain.ErrRefundSettled
	}
	if d != nil {
		if storedDispute, ok := f.disputes[d.ID]; !ok || storedDispute.Status != domain.DisputeOpen {
			return domain.ErrDisputeSettled
		}
	}
	if o != nil {
		if storedOrder, ok := f.orders[o.ID]; !ok || storedOrder.Settled() {
			return domain.ErrOrderSettled
		}
	}
	f.refunds[r.ID] = r
	if d != nil {
		f.disputes[d.ID] = *d
	}
	if o != nil {
		f.orders[o.ID] = *o
	}
	return nil
}

// fakeUploads is the upload seam a service test needs: it records a slot per resource id and
// resolves a confirmed one, refusing what the real store refuses — an unconfirmed id, another
// uploader's slot, and bytes that never arrived. Deliberately duplicated per module (see
// catalog's own copy): each module owns its own resource table.
type fakeUploads struct {
	nextID int64
	// slots is what Presign handed out, owner is who may confirm it, confirmed is whether it
	// has been.
	slots     map[int64]bool
	owner     map[int64]int64
	confirmed map[int64]bool
	// arrived is whether the client actually uploaded. A confirm without it is refused, which
	// is what stops a row rendering as a broken image.
	arrived map[int64]bool
}

func newFakeUploads() *fakeUploads {
	return &fakeUploads{
		slots: map[int64]bool{}, owner: map[int64]int64{},
		confirmed: map[int64]bool{}, arrived: map[int64]bool{},
	}
}

func (f *fakeUploads) Presign(_ context.Context, uploaderID int64, _ string, req common.UploadRequest) (common.UploadSlot, error) {
	f.nextID++
	f.slots[f.nextID] = true
	f.owner[f.nextID] = uploaderID
	return common.UploadSlot{
		ResourceID: f.nextID,
		URL:        "https://store.test/put/" + strconv.FormatInt(f.nextID, 10),
		ExpiresAt:  time.Now().Add(15 * time.Minute),
	}, nil
}

func (f *fakeUploads) Confirm(_ context.Context, uploaderID, resourceID int64) (common.Resource, error) {
	if !f.slots[resourceID] || f.confirmed[resourceID] || f.owner[resourceID] != uploaderID {
		return common.Resource{}, common.ErrResourceNotFound
	}
	if !f.arrived[resourceID] {
		return common.Resource{}, storage.ErrObjectNotFound
	}
	f.confirmed[resourceID] = true
	return common.Resource{ID: resourceID, Provider: "test", ObjectKey: "k", Mime: "image/jpeg"}, nil
}

func (f *fakeUploads) Resolve(_ context.Context, ids []int64) (map[int64]common.ResourceDTO, error) {
	out := make(map[int64]common.ResourceDTO, len(ids))
	for _, one := range ids {
		if !f.confirmed[one] {
			continue
		}
		out[one] = common.Resource{
			ID: one, Provider: "test", ObjectKey: "k", Mime: "image/jpeg",
			URL: "https://store.test/get/" + strconv.FormatInt(one, 10),
		}.ToDTO()
	}
	return out, nil
}

// confirm marks a resource id already confirmed, for a test that just needs a usable evidence
// id rather than to walk the reserve/PUT/confirm flow itself.
func (f *fakeUploads) confirm(id int64) {
	f.slots[id] = true
	f.arrived[id] = true
	f.confirmed[id] = true
}
