package order

import (
	"context"
	"fmt"
	"time"

	catalogapi "shopnexus/internal/module/catalog/api"
	financeapi "shopnexus/internal/module/finance/api"
	orderapi "shopnexus/internal/module/order/api"
	"shopnexus/internal/module/order/domain"
	"shopnexus/internal/shared/id"
)

// CreateDraft opens a purchase session by freezing the listing's terms. A listing priced
// `negotiable` has no draft: its price is agreed in a negotiation, and there is nothing to
// freeze until somebody accepts.
func (s *Service) CreateDraft(ctx context.Context, req orderapi.CreateDraftRequest) (orderapi.Draft, error) {
	listing, err := s.catalog.GetListing(ctx, catalogapi.GetListingRequest{
		ID: req.ListingID, ViewerID: req.ActorID,
	})
	if err != nil {
		return orderapi.Draft{}, fmt.Errorf("get listing: %w", err)
	}
	if listing.PriceMode != "fixed" {
		return orderapi.Draft{}, domain.ErrNegotiableNeedsOffer
	}
	snapshot := domain.ListingSnapshot{
		ListingID: listing.ID.Int64(),
		SellerID:  listing.Seller.ID.Int64(),
		Name:      listing.Name,
		Currency:  listing.Currency,
		PriceMode: listing.PriceMode,
	}
	for _, v := range listing.Variants {
		snapshot.Variants = append(snapshot.Variants, domain.VariantSnapshot{
			VariantID:      v.ID.Int64(),
			Price:          v.Price,
			Attributes:     v.Attributes,
			PackageDetails: v.PackageDetails,
		})
	}
	d, err := domain.NewDraft(req.ActorID.Int64(), snapshot, draftWindow)
	if err != nil {
		return orderapi.Draft{}, err
	}
	if err := s.repo.InsertDraft(ctx, &d); err != nil {
		return orderapi.Draft{}, fmt.Errorf("insert draft: %w", err)
	}
	return toAPIDraft(d), nil
}

func (s *Service) ListDrafts(ctx context.Context, req orderapi.ListDraftsRequest) (orderapi.DraftPage, error) {
	filter, err := cursorFilter(req.Cursor, req.Limit)
	if err != nil {
		return orderapi.DraftPage{}, err
	}
	rows, err := s.repo.ListDrafts(ctx, req.ActorID.Int64(), filter)
	if err != nil {
		return orderapi.DraftPage{}, fmt.Errorf("list drafts: %w", err)
	}
	hasMore := len(rows) > req.Limit
	if hasMore {
		rows = rows[:req.Limit]
	}
	out := make([]orderapi.Draft, 0, len(rows))
	for _, d := range rows {
		out = append(out, toAPIDraft(d))
	}
	page := orderapi.DraftPage{Data: out, Meta: orderapi.CursorInfo{HasMore: hasMore}}
	if hasMore && len(rows) > 0 {
		last := rows[len(rows)-1]
		page.Meta.NextCursor = formatCursor(last.CreatedAt, last.ID)
	}
	return page, nil
}

func (s *Service) GetDraft(ctx context.Context, req orderapi.DraftRequest) (orderapi.Draft, error) {
	d, err := s.repo.FindDraft(ctx, req.ID.Int64(), req.ActorID.Int64())
	if err != nil {
		return orderapi.Draft{}, fmt.Errorf("find draft: %w", err)
	}
	return toAPIDraft(d), nil
}

func (s *Service) CancelDraft(ctx context.Context, req orderapi.DraftRequest) error {
	d, err := s.repo.FindDraft(ctx, req.ID.Int64(), req.ActorID.Int64())
	if err != nil {
		return fmt.Errorf("find draft: %w", err)
	}
	if err := d.Cancel(); err != nil {
		return err
	}
	if err := s.repo.SaveDraft(ctx, d); err != nil {
		return fmt.Errorf("save draft: %w", err)
	}
	return nil
}

// Checkout writes the lines and opens one payment session for them. Pay-first: nothing is
// reserved until the money is asked for, and the order appears when the session completes.
//
// The prices come from the frozen snapshot, never from the live catalog — that is what the
// draft is for. Stock is reserved here, so two buyers cannot both check out the last one.
func (s *Service) Checkout(ctx context.Context, req orderapi.CheckoutRequest) (orderapi.CheckoutResult, error) {
	d, err := s.repo.FindDraft(ctx, req.ID.Int64(), req.ActorID.Int64())
	if err != nil {
		return orderapi.CheckoutResult{}, fmt.Errorf("find draft: %w", err)
	}
	if !d.Live(time.Now()) {
		if d.CancelledAt != nil {
			return orderapi.CheckoutResult{}, domain.ErrDraftSettled
		}
		return orderapi.CheckoutResult{}, domain.ErrDraftExpired
	}
	if d.Snapshot.Currency != req.Currency {
		return orderapi.CheckoutResult{}, domain.ErrCurrencyMismatch
	}
	if err := s.transportOption(ctx, req.TransportOption); err != nil {
		return orderapi.CheckoutResult{}, err
	}
	address, err := s.contactSnapshot(ctx, req.ActorID, req.ContactID)
	if err != nil {
		return orderapi.CheckoutResult{}, err
	}

	// The draft is spent before anything is reserved or charged, and the write is the claim:
	// `WHERE cancelled_at IS NULL` means exactly one of two concurrent checkouts of one draft
	// gets through, and the loser is refused rather than handed a second payment session for
	// the same frozen price.
	if err := d.Cancel(); err != nil {
		return orderapi.CheckoutResult{}, err
	}
	if err := s.repo.SaveDraft(ctx, d); err != nil {
		return orderapi.CheckoutResult{}, err
	}

	// Reserve first, then charge. A reservation that is not paid for is released by the
	// session expiring; a charge with no stock behind it is a refund and an apology.
	var reserved []orderapi.CheckoutLine
	release := func() {
		for _, line := range reserved {
			if err := s.catalog.ReleaseStock(ctx, catalogapi.StockMovementRequest{
				VariantID: line.VariantID, Units: line.Quantity,
			}); err != nil {
				s.log.Error("release stock after failed checkout", "err", err)
			}
		}
	}
	total := int64(0)
	lines := make([]*domain.Item, 0, len(req.Lines))
	for _, line := range req.Lines {
		frozen, err := d.Variant(line.VariantID.Int64())
		if err != nil {
			release()
			return orderapi.CheckoutResult{}, err
		}
		if err := s.catalog.ReserveStock(ctx, catalogapi.StockMovementRequest{
			VariantID: line.VariantID, Units: line.Quantity,
		}); err != nil {
			release()
			return orderapi.CheckoutResult{}, fmt.Errorf("reserve stock: %w", err)
		}
		reserved = append(reserved, line)
		amount := frozen.Price * line.Quantity
		total += amount
		item, err := domain.NewItem(domain.FromDraft(d.ID), req.ActorID.Int64(),
			d.Snapshot.SellerID, d.ListingID, line.VariantID.Int64(), address, req.Note,
			req.Currency, line.Quantity, req.TransportOption, amount, 1)
		if err != nil {
			release()
			return orderapi.CheckoutResult{}, err
		}
		lines = append(lines, &item)
	}

	session, err := s.finance.OpenCheckout(ctx, financeapi.OpenCheckoutRequest{
		BuyerID:  req.ActorID,
		SellerID: id.Of[id.Account](d.Snapshot.SellerID),
		Currency: req.Currency,
		Total:    total,
		Note:     d.Snapshot.Name,
		Data:     checkoutContext(domain.FromDraft(d.ID)),
	})
	if err != nil {
		release()
		return orderapi.CheckoutResult{}, fmt.Errorf("open checkout: %w", err)
	}
	for _, item := range lines {
		item.PaymentSessionID = session.ID.Int64()
	}
	if err := s.repo.InsertItems(ctx, lines); err != nil {
		release()
		return orderapi.CheckoutResult{}, fmt.Errorf("insert items: %w", err)
	}
	// The reserved stock is now on a clock: a checkout nobody pays has to give it back.
	s.timer("start checkout", s.workflows.StartCheckout(ctx, session.ID.Int64()))
	return s.checkoutResult(lines, session), nil
}

func (s *Service) checkoutResult(lines []*domain.Item, session financeapi.Session) orderapi.CheckoutResult {
	out := orderapi.CheckoutResult{
		PaymentSession: session.ID,
		Total:          session.TotalAmount,
		Currency:       session.Currency,
	}
	for _, item := range lines {
		out.Items = append(out.Items, toAPIItem(*item))
	}
	return out
}

func toAPIDraft(d domain.Draft) orderapi.Draft {
	out := orderapi.Draft{
		ID:          id.Of[id.DraftOrder](d.ID),
		ListingID:   id.Of[id.Listing](d.ListingID),
		SellerID:    id.Of[id.Account](d.Snapshot.SellerID),
		Name:        d.Snapshot.Name,
		Currency:    d.Snapshot.Currency,
		PriceMode:   d.Snapshot.PriceMode,
		CreatedAt:   d.CreatedAt,
		ValidUntil:  d.ValidUntil,
		CancelledAt: d.CancelledAt,
	}
	for _, v := range d.Snapshot.Variants {
		out.Variants = append(out.Variants, orderapi.DraftVariant{
			VariantID:  id.Of[id.Variant](v.VariantID),
			Price:      v.Price,
			Attributes: v.Attributes,
		})
	}
	return out
}
