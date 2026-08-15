package order

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"time"

	catalogapi "shopnexus/internal/module/catalog/api"
	financeapi "shopnexus/internal/module/finance/api"
	orderapi "shopnexus/internal/module/order/api"
	"shopnexus/internal/module/order/domain"
	"shopnexus/internal/provider/transport"
	"shopnexus/internal/shared/id"
)

// CreateDraft opens a purchase session by freezing the listing's terms — for any listing, however
// it is priced. A `negotiable` one carries an asking price like any other, and a buyer who does not
// want to haggle takes it: the listing page asks which they want, and this is the "buy now" half.
// Negotiating instead freezes the agreed terms in the offer, and its own checkout spends those.
func (s *Service) CreateDraft(ctx context.Context, req orderapi.CreateDraftRequest) (orderapi.Draft, error) {
	listing, err := s.catalog.GetListing(ctx, catalogapi.GetListingRequest{
		ID: req.ListingID, ViewerID: req.ActorID,
	})
	if err != nil {
		return orderapi.Draft{}, fmt.Errorf("get listing: %w", err)
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
	rows, meta := page(rows, req.Limit, func(d domain.Draft) (time.Time, int64) {
		return d.CreatedAt, d.ID
	})
	out := make([]orderapi.Draft, 0, len(rows))
	for _, d := range rows {
		out = append(out, toAPIDraft(d))
	}
	return orderapi.DraftPage{Data: out, Meta: meta}, nil
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
	// NewDraft already refuses this, but sessions opened before it did are still in the table
	// and this is the last gate before the money is asked for.
	if d.BuyerID == d.Snapshot.SellerID {
		return orderapi.CheckoutResult{}, domain.ErrSelfPurchase
	}
	if _, err := s.courier(ctx, req.TransportOption); err != nil {
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
		item, err := domain.NewItem(domain.NewLine{
			Origin:          domain.FromDraft(d.ID),
			BuyerID:         req.ActorID.Int64(),
			SellerID:        d.Snapshot.SellerID,
			ListingID:       d.ListingID,
			VariantID:       line.VariantID.Int64(),
			Address:         address,
			Note:            req.Note,
			Currency:        req.Currency,
			Quantity:        line.Quantity,
			TransportOption: req.TransportOption,
			Total:           amount,
		})
		if err != nil {
			release()
			return orderapi.CheckoutResult{}, err
		}
		lines = append(lines, &item)
	}

	// Delivery, priced from the carrier for this parcel to this address. The buyer pays it, so
	// it is part of what the session collects — and quoting here rather than at settlement means
	// a seller with no collection point is refused before any money is asked for.
	fee, err := s.quoteShipping(ctx, req.TransportOption, d.Snapshot.SellerID, address,
		shippingLines(d, req.Lines))
	if err != nil {
		release()
		return orderapi.CheckoutResult{}, err
	}
	session, err := s.finance.OpenCheckout(ctx, financeapi.OpenCheckoutRequest{
		BuyerID:  req.ActorID,
		SellerID: id.Of[id.Account](d.Snapshot.SellerID),
		Currency: req.Currency,
		Total:    total + fee,
		Note:     d.Snapshot.Name,
		Data:     checkoutContext(domain.FromDraft(d.ID), fee),
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

	var variantIDs []int64
	for _, line := range req.Lines {
		variantIDs = append(variantIDs, line.VariantID.Int64())
	}
	if err := s.repo.DeleteCartItemsByVariants(ctx, req.ActorID.Int64(), variantIDs); err != nil {
		s.log.Warn("failed to delete cart items after checkout", "account_id", req.ActorID.Int64(), "err", err)
	}

	return s.checkoutResult(lines, session, fee), nil
}

// shippingLines is what the courier prices: how many of what, with the package details frozen in
// the draft. The weights are the snapshot's, not the live listing's — the parcel a buyer is being
// quoted for is the one they are buying.
func shippingLines(d domain.Draft, lines []orderapi.CheckoutLine) []transport.ItemMetadata {
	out := make([]transport.ItemMetadata, 0, len(lines))
	for _, line := range lines {
		frozen, err := d.Variant(line.VariantID.Int64())
		if err != nil {
			continue
		}
		out = append(out, transport.ItemMetadata{
			VariantID:      line.VariantID.Int64(),
			Quantity:       line.Quantity,
			PackageDetails: jsonOf(frozen.PackageDetails),
		})
	}
	return out
}

// jsonOf is the package details as the courier's client takes them. An unencodable map is an
// empty object rather than a failed checkout: a carrier that cannot read the dimensions prices
// by weight, and the alternative is refusing a sale over a JSON error.
func jsonOf(v map[string]any) jsontext.Value {
	if len(v) == 0 {
		return jsontext.Value("{}")
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return jsontext.Value("{}")
	}
	return raw
}

func (s *Service) checkoutResult(lines []*domain.Item, session financeapi.Session, fee int64) orderapi.CheckoutResult {
	out := orderapi.CheckoutResult{
		PaymentSession: session.ID,
		GoodsTotal:     session.TotalAmount - fee,
		ShippingFee:    fee,
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

// ShippingQuotes prices every enabled carrier for one purchase, so the buyer sees delivery before
// they pay for it — the same list on a fixed-price listing and on agreed terms, because the buyer
// pays carriage on both.
//
// Three sources, exactly one of them: a variant is an estimate for a listing page, a draft and an
// accepted offer are the two things that freeze a price. The fee is quoted again at checkout
// rather than carried from here — a quote a client holds is a quote it can keep past the point it
// was true, which is why this answers an estimate and the session answers the charge.
func (s *Service) ShippingQuotes(ctx context.Context, req orderapi.ShippingQuotesRequest) (orderapi.ShippingQuotes, error) {
	if err := s.v.Struct(req); err != nil {
		return orderapi.ShippingQuotes{}, err
	}
	named := 0
	for _, set := range []bool{req.VariantID != 0, req.DraftID != 0, req.OfferID != 0} {
		if set {
			named++
		}
	}
	if named != 1 {
		return orderapi.ShippingQuotes{}, domain.ErrQuoteSourceInvalid
	}
	address, contactID, err := s.deliverySnapshot(ctx, req.ActorID, req.ContactID)
	if err != nil {
		return orderapi.ShippingQuotes{}, err
	}

	var (
		sellerID int64
		currency string
		lines    []transport.ItemMetadata
	)
	switch {
	case req.VariantID != 0:
		// A listing page, before anything is frozen: the parcel is the variant the buyer is
		// looking at, one of it unless they said otherwise.
		listing, _, err := s.variantOf(ctx, req.ActorID, req.VariantID)
		if err != nil {
			return orderapi.ShippingQuotes{}, err
		}
		quantity := req.Quantity
		if quantity == 0 {
			quantity = 1
		}
		sellerID, currency = listing.Seller.ID.Int64(), listing.Currency
		lines = []transport.ItemMetadata{{VariantID: req.VariantID.Int64(), Quantity: quantity}}
	case req.DraftID != 0:
		d, err := s.repo.FindDraft(ctx, req.DraftID.Int64(), req.ActorID.Int64())
		if err != nil {
			return orderapi.ShippingQuotes{}, fmt.Errorf("find draft: %w", err)
		}
		if !d.Live(time.Now()) {
			return orderapi.ShippingQuotes{}, domain.ErrDraftExpired
		}
		sellerID, currency = d.Snapshot.SellerID, d.Snapshot.Currency
		lines = shippingLines(d, req.Lines)
	default:
		o, err := s.party(ctx, req.ActorID, req.OfferID)
		if err != nil {
			return orderapi.ShippingQuotes{}, err
		}
		// Quoted for terms the buyer may actually check out, so the same guard the checkout uses.
		if err := o.CheckoutBy(req.ActorID.Int64(), time.Now()); err != nil {
			return orderapi.ShippingQuotes{}, err
		}
		listing, _, err := s.variantOf(ctx, req.ActorID, id.Of[id.Variant](o.VariantID))
		if err != nil {
			return orderapi.ShippingQuotes{}, err
		}
		sellerID, currency = o.SellerID, listing.Currency
		lines = []transport.ItemMetadata{{VariantID: o.VariantID, Quantity: o.Quantity}}
	}
	if len(lines) == 0 {
		return orderapi.ShippingQuotes{}, domain.ErrCheckoutEmpty
	}

	carriers, err := s.carriers(ctx)
	if err != nil {
		return orderapi.ShippingQuotes{}, err
	}
	// The seller's collection point is the same for every carrier, so it is read once — a
	// per-option lookup here is one account round trip per row on a page-load route.
	pickup, err := s.pickupSnapshot(ctx, sellerID)
	if err != nil {
		return orderapi.ShippingQuotes{}, err
	}
	out := orderapi.ShippingQuotes{
		Currency:  currency,
		ContactID: contactID,
		Options:   make([]orderapi.ShippingQuote, 0, len(carriers)),
	}
	for _, carrier := range carriers {
		// The row is already in hand, so the provider is resolved from it rather than looked up
		// again per carrier. A row whose provider went missing drops out with the ones that
		// declined, which is the same thing to a buyer: it is not offered.
		client, err := s.clientFor(carrier)
		if err != nil {
			s.log.Debug("carrier has no provider to quote with", "option", carrier.ID, "err", err)
			continue
		}
		fee, err := s.quoteCarrier(ctx, client, carrier.ID, pickup, address, lines)
		if err != nil {
			// One carrier that cannot price this parcel is one option missing from the list, not
			// a page that fails: the buyer picks from whoever answered.
			s.log.Debug("carrier declined to quote", "option", carrier.ID, "err", err)
			continue
		}
		out.Options = append(out.Options, orderapi.ShippingQuote{
			Option: carrier.ID, Name: carrier.Name, Fee: fee,
		})
	}
	return out, nil
}
