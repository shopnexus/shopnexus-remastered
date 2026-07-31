package order

import (
	"context"
	"fmt"

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
	if req.Role == "seller" {
		filter.SellerID = req.ActorID.Int64()
	} else {
		filter.BuyerID = req.ActorID.Int64()
	}
	rows, err := s.repo.ListItems(ctx, filter)
	if err != nil {
		return orderapi.ItemPage{}, fmt.Errorf("list items: %w", err)
	}
	hasMore := len(rows) > req.Limit
	if hasMore {
		rows = rows[:req.Limit]
	}
	out := make([]orderapi.Item, 0, len(rows))
	for _, i := range rows {
		out = append(out, toAPIItem(i))
	}
	page := orderapi.ItemPage{Data: out, Meta: orderapi.CursorInfo{HasMore: hasMore}}
	if hasMore && len(rows) > 0 {
		page.Meta.NextCursor = formatCursor(rows[len(rows)-1].CreatedAt)
	}
	return page, nil
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
	if err := i.Cancel(req.ActorID.Int64()); err != nil {
		return orderapi.Item{}, err
	}
	if err := s.repo.SaveItem(ctx, i); err != nil {
		return orderapi.Item{}, fmt.Errorf("save item: %w", err)
	}
	if err := s.catalog.ReleaseStock(ctx, catalogapi.StockMovementRequest{
		VariantID: id.Of[id.Variant](i.VariantID), Units: i.Quantity,
	}); err != nil {
		// The line is already cancelled; a stock release that failed is a reservation that
		// expires on its own rather than a reason to un-cancel.
		s.log.Error("release stock after cancelled item", "item_id", i.ID, "err", err)
	}
	return toAPIItem(i), nil
}

func (s *Service) ListOrders(ctx context.Context, req orderapi.ListOrdersRequest) (orderapi.OrderPage, error) {
	cursor, err := cursorFilter(req.Cursor, req.Limit)
	if err != nil {
		return orderapi.OrderPage{}, err
	}
	filter := port.OrderFilter{State: req.State, Cursor: cursor}
	if req.Role == "seller" {
		filter.SellerID = req.ActorID.Int64()
	} else {
		filter.BuyerID = req.ActorID.Int64()
	}
	rows, err := s.repo.ListOrders(ctx, filter)
	if err != nil {
		return orderapi.OrderPage{}, fmt.Errorf("list orders: %w", err)
	}
	hasMore := len(rows) > req.Limit
	if hasMore {
		rows = rows[:req.Limit]
	}
	out := make([]orderapi.Order, 0, len(rows))
	for _, o := range rows {
		view, err := s.orderView(ctx, o)
		if err != nil {
			return orderapi.OrderPage{}, err
		}
		out = append(out, view)
	}
	page := orderapi.OrderPage{Data: out, Meta: orderapi.CursorInfo{HasMore: hasMore}}
	if hasMore && len(rows) > 0 {
		page.Meta.NextCursor = formatCursor(rows[len(rows)-1].CreatedAt)
	}
	return page, nil
}

func (s *Service) GetOrder(ctx context.Context, req orderapi.OrderRequest) (orderapi.Order, error) {
	o, err := s.involved(ctx, req.ActorID, req.ID)
	if err != nil {
		return orderapi.Order{}, err
	}
	return s.orderView(ctx, o)
}

// ConfirmReceipt is the buyer saying the goods arrived, with the evidence a later dispute
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
	// The money goes back and the stock with it. Keyed on the order, so a retried
	// cancellation cannot refund twice.
	if err := s.refundEscrow(ctx, o, "cancel"); err != nil {
		return orderapi.Order{}, err
	}
	s.releaseOrderStock(ctx, o)
	s.publishSettled(ctx, o, false)
	return s.orderView(ctx, o)
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
		ID:                 id.Of[id.Order](o.ID),
		Buyer:              buyer,
		Seller:             seller,
		Address:            toAPIAddress(o.Address),
		PickupAddress:      toAPIAddress(o.PickupAddress),
		State:              o.State(),
		Total:              total,
		Currency:           currency,
		ReceivedAt:         o.ReceivedAt,
		ReceiptAttachments: pick(evidence, o.ReceiptAttachments),
		PayoutDeadlineAt:   o.PayoutDue(),
		CreatedAt:          o.CreatedAt,
		CompletedAt:        o.CompletedAt,
		CancelledAt:        o.CancelledAt,
	}
	if o.DraftID != nil {
		out.DraftID = new(id.Of[id.DraftOrder](*o.DraftID))
	}
	if o.OfferID != nil {
		out.OfferID = new(id.Of[id.Offer](*o.OfferID))
	}
	if o.PayoutSessionID != nil {
		out.PayoutSessionID = new(id.Of[id.PaymentSession](*o.PayoutSessionID))
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

// refundEscrow sends the held money back to the buyer. The key carries the order and the
// reason, so a cancellation and a refund cannot each pay the buyer once for the same sale.
func (s *Service) refundEscrow(ctx context.Context, o domain.Order, reason string) error {
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
		IdempotencyKey: fmt.Sprintf("order:%d:refund", o.ID),
	})
	if err != nil {
		return fmt.Errorf("refund escrow: %w", err)
	}
	return nil
}

// releaseOrderStock gives the units back. Best-effort: the order is already cancelled, and a
// reservation nobody released expires on its own.
func (s *Service) releaseOrderStock(ctx context.Context, o domain.Order) {
	items, err := s.repo.OrderItems(ctx, o.ID)
	if err != nil {
		s.log.Error("read items to release stock", "order_id", o.ID, "err", err)
		return
	}
	for _, i := range items {
		if !i.Live() {
			continue
		}
		if err := s.catalog.ReleaseStock(ctx, catalogapi.StockMovementRequest{
			VariantID: id.Of[id.Variant](i.VariantID), Units: i.Quantity,
		}); err != nil {
			s.log.Error("release stock", "item_id", i.ID, "err", err)
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
