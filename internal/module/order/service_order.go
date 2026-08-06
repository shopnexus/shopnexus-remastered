package order

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	catalogapi "shopnexus/internal/module/catalog/api"
	financeapi "shopnexus/internal/module/finance/api"
	orderapi "shopnexus/internal/module/order/api"
	"shopnexus/internal/module/order/domain"
	"shopnexus/internal/module/order/port"
	"shopnexus/internal/shared/id"
)

// ListItems answers the caller's lines — their purchases as a buyer, or the ones they are
// selling. `pending` is the window between the money landing and the order being written.
func (s *Service) ListItems(ctx context.Context, req orderapi.ListItemsRequest) (orderapi.ItemPage, error) {
	cursor, err := cursorFilter(req.Cursor, req.Limit)
	if err != nil {
		return orderapi.ItemPage{}, err
	}
	filter := port.ItemFilter{PendingOnly: req.Pending, Cursor: cursor}
	if req.Role == orderapi.RoleSeller {
		filter.SellerID = req.ActorID.Int64()
	} else {
		filter.BuyerID = req.ActorID.Int64()
	}
	rows, err := s.repo.ListItems(ctx, filter)
	if err != nil {
		return orderapi.ItemPage{}, fmt.Errorf("list items: %w", err)
	}
	rows, meta := page(rows, req.Limit, func(i domain.Item) (time.Time, int64) {
		return i.CreatedAt, i.ID
	})
	out := make([]orderapi.Item, 0, len(rows))
	for _, i := range rows {
		out = append(out, toAPIItem(i))
	}
	return orderapi.ItemPage{Data: out, Meta: meta}, nil
}

// CancelItem voids a line before it becomes an order, and gives the reserved stock back.
// After the money lands the buyer asks for a refund instead — a decision the seller sees.
func (s *Service) CancelItem(ctx context.Context, req orderapi.ItemRequest) (orderapi.Item, error) {
	i, err := s.repo.FindItem(ctx, req.ID.Int64())
	if err != nil {
		return orderapi.Item{}, fmt.Errorf("find item: %w", err)
	}
	// Either party may drop a line that has not become an order: the buyer changed their
	// mind, or the seller cannot fulfil it.
	if i.BuyerID != req.ActorID.Int64() && i.SellerID != req.ActorID.Int64() {
		return orderapi.Item{}, domain.ErrItemNotFound
	}
	if err := s.cancelLine(ctx, &i, req.ActorID.Int64()); err != nil {
		return orderapi.Item{}, err
	}
	return toAPIItem(i), nil
}

// cancelLine drops one unpaid line and gives its reservation back.
//
// `order_id IS NULL` is not the guard it looks like: the order is written by the payment
// webhook, so between the money landing and that write — and for as long as the write keeps
// failing, which is what the pending list is for — a paid line still has a null order. Without
// asking finance, a seller could cancel a line the buyer had paid for, release the stock and
// leave the capture covering nothing.
func (s *Service) cancelLine(ctx context.Context, i *domain.Item, actorID int64) error {
	if err := s.requireUnpaid(ctx, *i, actorID); err != nil {
		return err
	}
	if err := i.Cancel(actorID); err != nil {
		return err
	}
	if err := s.repo.SaveItem(ctx, *i); err != nil {
		return fmt.Errorf("save item: %w", err)
	}
	if err := s.catalog.ReleaseStock(ctx, catalogapi.StockMovementRequest{
		VariantID: id.Of[id.Variant](i.VariantID), Units: i.Quantity,
	}); err != nil {
		// The line is already cancelled; a stock release that failed is a reservation the
		// expiry sweep picks up rather than a reason to un-cancel.
		s.log.Error("release stock after cancelled item", "item_id", i.ID, "err", err)
	}
	// With every line of the session gone there is nothing left to pay for, so the run
	// waiting on the money stops waiting instead of holding its timer to the end.
	if s.sessionAbandoned(ctx, i.PaymentSessionID) {
		s.timer("checkout cancelled", s.workflows.CheckoutCancelled(ctx, i.PaymentSessionID))
	}
	return nil
}

// requireUnpaid asks finance whether the session behind a line has been covered. Fails closed:
// a session it cannot read is treated as paid, because releasing the stock under a capture is
// the expensive way to be wrong.
func (s *Service) requireUnpaid(ctx context.Context, i domain.Item, actorID int64) error {
	session, err := s.finance.GetSession(ctx, financeapi.GetSessionRequest{
		ActorID: id.Of[id.Account](actorID),
		ID:      id.Of[id.PaymentSession](i.PaymentSessionID),
	})
	if err != nil {
		return fmt.Errorf("read payment session: %w", err)
	}
	if session.PaidAt != nil || session.Status == financePaid {
		return domain.ErrSessionPaid
	}
	return nil
}

// sessionAbandoned reports whether a payment session has no live line left. Read after the
// cancellation rather than counted as it goes: the lines are the truth, and a counter would be
// a second one to keep in step.
func (s *Service) sessionAbandoned(ctx context.Context, sessionID int64) bool {
	lines, err := s.repo.ItemsByPaymentSession(ctx, sessionID)
	if err != nil {
		s.log.Error("read session lines", "session_id", sessionID, "err", err)
		return false
	}
	for _, line := range lines {
		if line.Live() {
			return false
		}
	}
	return true
}

// summaryWindow is how far back a summary may look. A dashboard reads a month; the cap exists so one
// request cannot ask for a series with a bucket per day since the platform opened.
const summaryWindow = 366 * 24 * time.Hour

// defaultSummaryWindow is what a caller who named no window gets.
const defaultSummaryWindow = 30 * 24 * time.Hour

// GetOrderSummary answers the caller's own side of the sale over a window. The window filters
// `created_at`, so the counts, the totals and the daily series all describe one set of orders — mixing
// "placed in the window" with "completed in the window" would make three numbers that cannot be added
// up by the person reading them.
func (s *Service) GetOrderSummary(ctx context.Context, req orderapi.OrderSummaryRequest) (orderapi.OrderSummary, error) {
	to := time.Now()
	if req.To != nil {
		to = *req.To
	}
	from := to.Add(-defaultSummaryWindow)
	if req.From != nil {
		from = *req.From
	}
	if !from.Before(to) {
		return orderapi.OrderSummary{}, domain.ErrSummaryWindowInvalid
	}
	if to.Sub(from) > summaryWindow {
		return orderapi.OrderSummary{}, domain.ErrSummaryWindowTooWide
	}
	// Validated here rather than in the SQL: Postgres answers an unknown zone with a 22023 the
	// caller would read as a server fault.
	zone := "UTC"
	if req.TZ != "" {
		if _, err := time.LoadLocation(req.TZ); err != nil {
			return orderapi.OrderSummary{}, domain.ErrTimeZoneUnknown
		}
		zone = req.TZ
	}

	filter := port.SummaryFilter{From: from, To: to, TZ: zone}
	if req.Role == orderapi.RoleSeller {
		filter.SellerID = req.ActorID.Int64()
	} else {
		filter.BuyerID = req.ActorID.Int64()
	}
	counts, err := s.repo.CountOrders(ctx, filter)
	if err != nil {
		return orderapi.OrderSummary{}, fmt.Errorf("count orders: %w", err)
	}
	days, err := s.repo.ListOrderDays(ctx, filter)
	if err != nil {
		return orderapi.OrderSummary{}, fmt.Errorf("list order days: %w", err)
	}

	out := orderapi.OrderSummary{
		From: from, To: to,
		Open: counts.Open, Completed: counts.Completed, Cancelled: counts.Cancelled,
		// Empty rather than null: both are lists a client renders, and a required field that
		// arrives as null is a contract the service broke.
		Totals: make([]orderapi.MoneyByCurrency, 0, len(counts.Totals)),
		Daily:  make([]orderapi.OrderSummaryDay, 0, len(days)),
	}
	// Sorted, because a map's order is not one and a chart legend that reshuffles per request looks
	// like the data moved.
	currencies := make([]string, 0, len(counts.Totals))
	for currency := range counts.Totals {
		currencies = append(currencies, currency)
	}
	slices.Sort(currencies)
	for _, currency := range currencies {
		out.Totals = append(out.Totals, orderapi.MoneyByCurrency{
			Currency: currency, Amount: counts.Totals[currency],
		})
	}
	for _, day := range days {
		out.Daily = append(out.Daily, orderapi.OrderSummaryDay{
			Date: day.Date, Placed: day.Placed, Completed: day.Completed,
		})
	}
	return out, nil
}

func (s *Service) ListOrders(ctx context.Context, req orderapi.ListOrdersRequest) (orderapi.OrderPage, error) {
	cursor, err := cursorFilter(req.Cursor, req.Limit)
	if err != nil {
		return orderapi.OrderPage{}, err
	}
	filter := port.OrderFilter{State: req.State, Cursor: cursor}
	if req.Role == orderapi.RoleSeller {
		filter.SellerID = req.ActorID.Int64()
	} else {
		filter.BuyerID = req.ActorID.Int64()
	}
	rows, err := s.repo.ListOrders(ctx, filter)
	if err != nil {
		return orderapi.OrderPage{}, fmt.Errorf("list orders: %w", err)
	}
	rows, meta := page(rows, req.Limit, func(o domain.Order) (time.Time, int64) {
		return o.CreatedAt, o.ID
	})
	out := make([]orderapi.Order, 0, len(rows))
	for _, o := range rows {
		view, err := s.orderView(ctx, o)
		if err != nil {
			return orderapi.OrderPage{}, err
		}
		out = append(out, view)
	}
	return orderapi.OrderPage{Data: out, Meta: meta}, nil
}

func (s *Service) GetOrder(ctx context.Context, req orderapi.OrderRequest) (orderapi.Order, error) {
	o, err := s.involved(ctx, req.ActorID, req.ID)
	if err != nil {
		return orderapi.Order{}, err
	}
	return s.orderView(ctx, o)
}

// ConfirmReceipt is the buyer saying the goods arrived, with the evidence a later refund
// would be judged on. It starts the escrow window, which is why it is not re-openable.
func (s *Service) ConfirmReceipt(ctx context.Context, req orderapi.ConfirmReceiptRequest) (orderapi.Order, error) {
	o, err := s.involved(ctx, req.ActorID, req.ID)
	if err != nil {
		return orderapi.Order{}, err
	}
	if o.BuyerID != req.ActorID.Int64() {
		return orderapi.Order{}, domain.ErrNotTheBuyer
	}
	attachments := resourceKeys(req.Attachments)
	if err := s.requireResources(ctx, attachments); err != nil {
		return orderapi.Order{}, err
	}
	if err := o.ConfirmReceipt(attachments); err != nil {
		return orderapi.Order{}, err
	}
	if err := s.repo.SaveOrder(ctx, o); err != nil {
		return orderapi.Order{}, fmt.Errorf("save order: %w", err)
	}
	// The receipt is what starts the escrow window, so the run waiting on delivery is told.
	s.timer("order received", s.workflows.OrderReceived(ctx, o.ID))
	return s.orderView(ctx, o)
}

// ConfirmOrder is the seller accepting a paid sale, and the only thing that hands the parcel to
// the carrier. Everything before this — the order row, the escrow, the frozen carrier and fee —
// is the *buyer's* side of the transaction, and none of it obliges the seller to post anything.
func (s *Service) ConfirmOrder(ctx context.Context, req orderapi.ConfirmOrderRequest) (orderapi.Order, error) {
	o, err := s.involved(ctx, req.ActorID, req.ID)
	if err != nil {
		return orderapi.Order{}, err
	}
	if o.SellerID != req.ActorID.Int64() {
		return orderapi.Order{}, domain.ErrNotTheSeller
	}
	if err := o.Confirm(); err != nil {
		return orderapi.Order{}, err
	}
	if err := s.repo.SaveOrder(ctx, o); err != nil {
		return orderapi.Order{}, fmt.Errorf("save order: %w", err)
	}
	lines, err := s.repo.OrderItems(ctx, o.ID)
	if err != nil {
		return orderapi.Order{}, fmt.Errorf("read order lines to book: %w", err)
	}
	parcel := make([]*domain.Item, 0, len(lines))
	for i := range lines {
		parcel = append(parcel, &lines[i])
	}
	// Best-effort, as it was at settlement: the confirmation is committed, so a carrier that is
	// down is a booking to retry rather than an acceptance to refuse — RetryUnbookedShipments is
	// the net under it, and it reads exactly the shipments with no provider_ref.
	s.bookShipment(ctx, o, parcel)
	s.timer("order confirmed", s.workflows.OrderConfirmed(ctx, o.ID))
	return s.orderView(ctx, o)
}

// DeclineOrder is the seller refusing a paid sale — out of stock, mispriced, whatever they say.
// The outcome is a cancellation with the escrow returned in full, delivery included, because the
// parcel never left. Same destination as letting the window pass, reached without making the
// buyer wait out a clock for an answer the seller already has.
func (s *Service) DeclineOrder(ctx context.Context, req orderapi.DeclineOrderRequest) (orderapi.Order, error) {
	o, err := s.involved(ctx, req.ActorID, req.ID)
	if err != nil {
		return orderapi.Order{}, err
	}
	if o.SellerID != req.ActorID.Int64() {
		return orderapi.Order{}, domain.ErrNotTheSeller
	}
	transport, err := s.repo.FindTransport(ctx, o.TransportID)
	if err != nil {
		return orderapi.Order{}, fmt.Errorf("find transport: %w", err)
	}
	if err := o.Decline(req.Reason); err != nil {
		return orderapi.Order{}, err
	}
	if err := s.repo.SaveOrder(ctx, o); err != nil {
		return orderapi.Order{}, fmt.Errorf("save order: %w", err)
	}
	if err := s.refundEscrow(ctx, o, transport.Fee); err != nil {
		return orderapi.Order{}, err
	}
	s.uncommitOrderStock(ctx, o)
	s.publishSettled(ctx, o, false)
	s.timer("order cancelled", s.workflows.OrderCancelled(ctx, o.ID))
	return s.orderView(ctx, o)
}

// CancelOrder voids an order before it ships and refunds the escrow. After the parcel has
// left, the buyer opens a refund instead: a shipment cannot be un-sent.
func (s *Service) CancelOrder(ctx context.Context, req orderapi.CancelOrderRequest) (orderapi.Order, error) {
	o, err := s.involved(ctx, req.ActorID, req.ID)
	if err != nil {
		return orderapi.Order{}, err
	}
	transport, err := s.repo.FindTransport(ctx, o.TransportID)
	if err != nil {
		return orderapi.Order{}, fmt.Errorf("find transport: %w", err)
	}
	if err := o.Cancel(transport.Shipped()); err != nil {
		return orderapi.Order{}, err
	}
	if err := s.repo.SaveOrder(ctx, o); err != nil {
		return orderapi.Order{}, fmt.Errorf("save order: %w", err)
	}
	// The money goes back and the stock with it — the delivery too, since the parcel never left
	// and `Cancel` just refused this route if it had. Keyed on the order, so a retried
	// cancellation cannot refund twice.
	if err := s.refundEscrow(ctx, o, transport.Fee); err != nil {
		return orderapi.Order{}, err
	}
	s.uncommitOrderStock(ctx, o)
	s.publishSettled(ctx, o, false)
	// Nothing is left to wait for. A cancellation always beats the receipt, so the run is
	// parked on that promise — telling it the refund resolved would resolve one nobody reads
	// and leave the invocation suspended for good.
	s.timer("order cancelled", s.workflows.OrderCancelled(ctx, o.ID))
	return s.orderView(ctx, o)
}

// AdvanceShipment corrects a checkpoint on the outbound leg by hand. Staff only: the parcel's
// position is the carrier's own report (RecordCarrierCheckpoint), and this route exists for a
// courier that got it wrong or never reported at all.
//
// The seller used to write it, because nothing else did. But `Shipped()` — the guard that stops
// an order being cancelled and the escrow taken back — reads that column, so one request saying
// `picked-up` with no parcel behind it ended the buyer's cancellation, and nobody checked. A
// seller who sees the status wrong raises an `order-issue` ticket, which is the same escalation
// every other one-sided claim on this platform goes through.
func (s *Service) AdvanceShipment(ctx context.Context, req orderapi.AdvanceShipmentRequest) (orderapi.Transport, error) {
	// Asked before the order is read: a caller with no business here learns nothing about it.
	// Only a *refusal* becomes "the carrier reports this" — an account module that could not be
	// read is reported as itself, or an outage reads as a permissions problem.
	if err := s.requireModerator(ctx, req.ActorID); err != nil {
		if errors.Is(err, domain.ErrModeratorRequired) {
			return orderapi.Transport{}, domain.ErrShipmentNotReportable
		}
		return orderapi.Transport{}, err
	}
	o, err := s.repo.FindOrder(ctx, req.ID.Int64())
	if err != nil {
		return orderapi.Transport{}, fmt.Errorf("find order: %w", err)
	}
	t, err := s.advanceLeg(ctx, o.TransportID, req.Status)
	if err != nil {
		return orderapi.Transport{}, err
	}
	return toAPITransport(t), nil
}

// advanceLeg moves one shipment and writes it, guarded on the status it moved out of.
func (s *Service) advanceLeg(ctx context.Context, transportID int64, status string) (domain.Transport, error) {
	t, err := s.repo.FindTransport(ctx, transportID)
	if err != nil {
		return domain.Transport{}, fmt.Errorf("find transport: %w", err)
	}
	from := t.Status
	if err := t.Advance(status); err != nil {
		return domain.Transport{}, err
	}
	if err := s.repo.SaveTransport(ctx, t, from); err != nil {
		return domain.Transport{}, fmt.Errorf("save transport: %w", err)
	}
	return t, nil
}

func (s *Service) GetOrderTransport(ctx context.Context, req orderapi.OrderRequest) (orderapi.Transport, error) {
	o, err := s.involved(ctx, req.ActorID, req.ID)
	if err != nil {
		return orderapi.Transport{}, err
	}
	t, err := s.repo.FindTransport(ctx, o.TransportID)
	if err != nil {
		return orderapi.Transport{}, fmt.Errorf("find transport: %w", err)
	}
	return toAPITransport(t), nil
}

// involved reads an order the caller is a party to. Somebody else's is not found rather
// than forbidden.
func (s *Service) involved(ctx context.Context, actorID id.ID[id.Account], orderID id.ID[id.Order]) (domain.Order, error) {
	o, err := s.repo.FindOrder(ctx, orderID.Int64())
	if err != nil {
		return domain.Order{}, fmt.Errorf("find order: %w", err)
	}
	if !o.Involves(actorID.Int64()) {
		return domain.Order{}, domain.ErrOrderNotFound
	}
	return o, nil
}

// orderTotal sums the lines the order covers. Not a column: the lines are the record of what
// was paid, and a stored total would be a second answer to the same question.
func (s *Service) orderTotal(ctx context.Context, orderID int64) (int64, string, []domain.Item, error) {
	items, err := s.repo.OrderItems(ctx, orderID)
	if err != nil {
		return 0, "", nil, fmt.Errorf("read order items: %w", err)
	}
	var total int64
	currency := ""
	for _, i := range items {
		if i.Live() {
			total += i.TotalAmount
		}
		currency = i.Currency
	}
	return total, currency, items, nil
}

func (s *Service) orderView(ctx context.Context, o domain.Order) (orderapi.Order, error) {
	total, currency, items, err := s.orderTotal(ctx, o.ID)
	if err != nil {
		return orderapi.Order{}, err
	}
	buyer, err := s.summary(ctx, o.BuyerID)
	if err != nil {
		return orderapi.Order{}, err
	}
	seller, err := s.summary(ctx, o.SellerID)
	if err != nil {
		return orderapi.Order{}, err
	}
	evidence, err := s.resources(ctx, o.ReceiptAttachments)
	if err != nil {
		return orderapi.Order{}, err
	}
	out := orderapi.Order{
		ID:                     id.Of[id.Order](o.ID),
		Buyer:                  buyer,
		Seller:                 seller,
		Address:                toAPIAddress(o.Address),
		PickupAddress:          toAPIAddress(o.PickupAddress),
		State:                  o.State(),
		Total:                  total,
		Currency:               currency,
		ConfirmedAt:            o.ConfirmedAt,
		ConfirmationDeadlineAt: o.ConfirmationDue(),
		DeclineReason:          o.DeclineReason,
		ReceivedAt:             o.ReceivedAt,
		ReceiptAttachments:     pick(evidence, o.ReceiptAttachments),
		PayoutDeadlineAt:       o.PayoutDue(),
		CreatedAt:              o.CreatedAt,
		CompletedAt:            o.CompletedAt,
		CancelledAt:            o.CancelledAt,
	}
	out.PayoutReleasedAt = o.PayoutReleasedAt
	if o.DraftID != nil {
		out.DraftID = new(id.Of[id.DraftOrder](*o.DraftID))
	}
	if o.OfferID != nil {
		out.OfferID = new(id.Of[id.Offer](*o.OfferID))
	}
	for _, i := range items {
		out.Items = append(out.Items, toAPIItem(i))
	}
	if t, err := s.repo.FindTransport(ctx, o.TransportID); err == nil {
		shipment := toAPITransport(t)
		out.Transport = &shipment
	}
	return out, nil
}

// refundEscrow sends the held money back to the buyer. One key per order, deliberately shared
// by every route that does this — a cancellation and a granted refund are the same fact about
// the same escrow, and a key per reason would let each of them pay the buyer once.
//
// A key that has already been posted is success: the money is where the caller wanted it, so a
// retried settlement carries on to the rows it still has to write.
//
// shipping is the carriage to hand back with the goods, and only a cancellation sends it: the
// parcel had not left, so the buyer paid for a delivery that never happened. A granted refund
// sends zero — that parcel was carried, and who bears the return leg is the verdict's business,
// not a fee reversal.
func (s *Service) refundEscrow(ctx context.Context, o domain.Order, shipping int64) error {
	total, currency, _, err := s.orderTotal(ctx, o.ID)
	if err != nil {
		return err
	}
	if total == 0 {
		return nil
	}
	err = s.finance.RefundEscrow(ctx, financeapi.EscrowRequest{
		BuyerID:        id.Of[id.Account](o.BuyerID),
		SellerID:       id.Of[id.Account](o.SellerID),
		OrderID:        id.Of[id.Order](o.ID),
		Currency:       currency,
		Amount:         total,
		ShippingFee:    shipping,
		IdempotencyKey: fmt.Sprintf("order:%d:refund", o.ID),
	})
	if err != nil && !errors.Is(err, financeMovementPosted) {
		return fmt.Errorf("refund escrow: %w", err)
	}
	return nil
}

// commitItemStock turns one line's reservation into a sale, keyed by the line so a retried
// settlement does not sell the same units twice.
func (s *Service) commitItemStock(ctx context.Context, orderID int64, i domain.Item) error {
	if err := s.catalog.CommitStock(ctx, catalogapi.StockCommitRequest{
		VariantID:      id.Of[id.Variant](i.VariantID),
		Units:          i.Quantity,
		IdempotencyKey: fmt.Sprintf("order:%d:item:%d:commit", orderID, i.ID),
	}); err != nil {
		return fmt.Errorf("commit stock: %w", err)
	}
	return nil
}

// uncommitOrderStock puts the units back on the shelf after the sale is undone — a cancelled
// order or a granted refund. Uncommit rather than release: by this point the units are in
// `sold`, and decrementing `reserved` would either affect nothing or eat another buyer's
// reservation and oversell.
//
// Best-effort: the order is already closed and the money already back, and the reversal is
// keyed, so re-running it is what a repair would do.
func (s *Service) uncommitOrderStock(ctx context.Context, o domain.Order) {
	items, err := s.repo.OrderItems(ctx, o.ID)
	if err != nil {
		s.log.Error("read items to uncommit stock", "order_id", o.ID, "err", err)
		return
	}
	for _, i := range items {
		if !i.Live() {
			continue
		}
		if err := s.catalog.UncommitStock(ctx, catalogapi.StockCommitRequest{
			VariantID:      id.Of[id.Variant](i.VariantID),
			Units:          i.Quantity,
			IdempotencyKey: fmt.Sprintf("order:%d:item:%d:uncommit", o.ID, i.ID),
		}); err != nil {
			s.log.Error("uncommit stock", "item_id", i.ID, "err", err)
		}
	}
}

func toAPIItem(i domain.Item) orderapi.Item {
	out := orderapi.Item{
		ID:               id.Of[id.Item](i.ID),
		ListingID:        id.Of[id.Listing](i.ListingID),
		VariantID:        id.Of[id.Variant](i.VariantID),
		SellerID:         id.Of[id.Account](i.SellerID),
		Quantity:         i.Quantity,
		Currency:         i.Currency,
		TotalAmount:      i.TotalAmount,
		TransportOption:  i.TransportOption,
		PaymentSessionID: id.Of[id.PaymentSession](i.PaymentSessionID),
		Note:             i.Note,
		CancelledAt:      i.CancelledAt,
		CreatedAt:        i.CreatedAt,
	}
	if i.OrderID != nil {
		out.OrderID = new(id.Of[id.Order](*i.OrderID))
	}
	return out
}

func toAPITransport(t domain.Transport) orderapi.Transport {
	return orderapi.Transport{
		ID:        id.Of[id.Transport](t.ID),
		Option:    t.Option,
		Status:    t.Status,
		Fee:       t.Fee,
		CreatedAt: t.CreatedAt,
	}
}

func toAPIAddress(a domain.AddressSnapshot) orderapi.AddressSnapshot {
	return orderapi.AddressSnapshot{
		FullName:      a.FullName,
		Phone:         a.Phone,
		Country:       a.Country,
		ProvinceCode:  a.ProvinceCode,
		DistrictCode:  a.DistrictCode,
		WardCode:      a.WardCode,
		AddressDetail: a.AddressDetail,
	}
}
