package order

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	catalogapi "shopnexus/internal/module/catalog/api"
	chatapi "shopnexus/internal/module/chat/api"
	financeapi "shopnexus/internal/module/finance/api"
	orderapi "shopnexus/internal/module/order/api"
	"shopnexus/internal/module/order/domain"
	"shopnexus/internal/module/order/port"
	"shopnexus/internal/shared/id"
)

// checkoutSource is what a payment session carries back to this module when it completes:
// which sale it was paying for. Stored on the session by the opener and handed to the
// subscriber, so settling needs no lookup by amount or by guesswork.
type checkoutSource struct {
	DraftID *int64 `json:"draft_id,omitempty"`
	OfferID *int64 `json:"offer_id,omitempty"`
}

func checkoutContext(origin domain.Origin) []byte {
	raw, err := json.Marshal(checkoutSource{DraftID: origin.DraftID, OfferID: origin.OfferID})
	if err != nil {
		// The shape is two optional numbers; there is nothing here that can fail to encode.
		return []byte("{}")
	}
	return raw
}

func decodeCheckoutSource(raw []byte) (domain.Origin, error) {
	var src checkoutSource
	if len(raw) == 0 {
		return domain.Origin{}, domain.ErrOrderNotFound
	}
	if err := json.Unmarshal(raw, &src); err != nil {
		return domain.Origin{}, fmt.Errorf("decode checkout context: %w", err)
	}
	origin := domain.Origin{DraftID: src.DraftID, OfferID: src.OfferID}
	if !origin.Valid() {
		return domain.Origin{}, domain.ErrOrderNotFound
	}
	return origin, nil
}

// CreateOffer opens a negotiation on a variant of a `negotiable` listing. Either side may
// start it — the buyer from the listing page, the seller from a thread — and whoever does
// owns the standing proposal, so the other one answers.
//
// The card goes into the pair's chat thread: the conversation is chat's, the terms are this
// row's, and the message carries only the offer's id so a counter cannot leave an old price
// on screen.
func (s *Service) CreateOffer(ctx context.Context, req orderapi.CreateOfferRequest) (orderapi.Offer, error) {
	listing, _, err := s.variantOf(ctx, req.ActorID, req.VariantID)
	if err != nil {
		return orderapi.Offer{}, err
	}
	if listing.PriceMode != "negotiable" {
		return orderapi.Offer{}, domain.ErrFixedPriceListing
	}
	sellerID := listing.Seller.ID
	buyerID := req.ActorID
	if req.ActorID == sellerID {
		// The seller opening it means they are proposing to somebody; without a buyer there
		// is nobody to propose to, so this route is the buyer's to start.
		return orderapi.Offer{}, domain.ErrOnlyBuyerAccepts
	}
	o, err := domain.NewOffer(listing.ID.Int64(), req.VariantID.Int64(), req.ActorID.Int64(),
		buyerID.Int64(), sellerID.Int64(), req.Quantity, req.Total, req.Reason, offerWindow)
	if err != nil {
		return orderapi.Offer{}, err
	}
	if err := s.repo.InsertOffer(ctx, &o); err != nil {
		return orderapi.Offer{}, fmt.Errorf("insert offer: %w", err)
	}
	s.postOfferCard(ctx, o, "offer opened")
	return toAPIOffer(o, listing.Currency), nil
}

func (s *Service) ListOffers(ctx context.Context, req orderapi.ListOffersRequest) (orderapi.OfferPage, error) {
	cursor, err := cursorFilter(req.Cursor, req.Limit)
	if err != nil {
		return orderapi.OfferPage{}, err
	}
	rows, err := s.repo.ListOffers(ctx, port.OfferFilter{
		AccountID: req.ActorID.Int64(), Status: req.Status, Cursor: cursor,
	})
	if err != nil {
		return orderapi.OfferPage{}, fmt.Errorf("list offers: %w", err)
	}
	hasMore := len(rows) > req.Limit
	if hasMore {
		rows = rows[:req.Limit]
	}
	currencies, err := s.listingCurrencies(ctx, req.ActorID, rows)
	if err != nil {
		return orderapi.OfferPage{}, err
	}
	out := make([]orderapi.Offer, 0, len(rows))
	for _, o := range rows {
		out = append(out, toAPIOffer(o, currencies[o.ListingID]))
	}
	page := orderapi.OfferPage{Data: out, Meta: orderapi.CursorInfo{HasMore: hasMore}}
	if hasMore && len(rows) > 0 {
		last := rows[len(rows)-1]
		page.Meta.NextCursor = formatCursor(last.CreatedAt, last.ID)
	}
	return page, nil
}

func (s *Service) GetOffer(ctx context.Context, req orderapi.OfferRequest) (orderapi.Offer, error) {
	o, err := s.party(ctx, req.ActorID, req.ID)
	if err != nil {
		return orderapi.Offer{}, err
	}
	currencies, err := s.listingCurrencies(ctx, req.ActorID, []domain.Offer{o})
	if err != nil {
		return orderapi.Offer{}, err
	}
	return toAPIOffer(o, currencies[o.ListingID]), nil
}

// listingCurrencies resolves the currency each offer's total is in. The offer row does not carry
// one — the listing decides it and cannot change it — so it is read here rather than copied at
// every revision. One catalog call for the whole page: a total with no currency beside it is not
// a price anybody can render.
func (s *Service) listingCurrencies(ctx context.Context, viewerID id.ID[id.Account], offers []domain.Offer) (map[int64]string, error) {
	out := make(map[int64]string, len(offers))
	if len(offers) == 0 {
		return out, nil
	}
	ids := make([]id.ID[id.Listing], 0, len(offers))
	for _, o := range offers {
		if _, ok := out[o.ListingID]; ok {
			continue
		}
		out[o.ListingID] = ""
		ids = append(ids, id.Of[id.Listing](o.ListingID))
	}
	page, err := s.catalog.ListListings(ctx, catalogapi.ListListingsRequest{
		ViewerID: viewerID, IDs: ids, Page: 1, Limit: len(ids),
	})
	if err != nil {
		return nil, fmt.Errorf("resolve offer listings: %w", err)
	}
	for _, listing := range page.Data {
		out[listing.ID.Int64()] = listing.Currency
	}
	return out, nil
}

// CounterOffer revises the terms and hands the turn over. Only the side that does not own
// the standing proposal may counter, so the two alternate and a price on the table is always
// somebody else's to answer.
func (s *Service) CounterOffer(ctx context.Context, req orderapi.CounterOfferRequest) (orderapi.Offer, error) {
	o, err := s.party(ctx, req.ActorID, req.ID)
	if err != nil {
		return orderapi.Offer{}, err
	}
	if err := o.Counter(req.ActorID.Int64(), req.Quantity, req.Total, req.Reason, time.Now(), offerWindow); err != nil {
		return orderapi.Offer{}, err
	}
	if err := s.repo.SaveOffer(ctx, o); err != nil {
		return orderapi.Offer{}, fmt.Errorf("save offer: %w", err)
	}
	s.postOfferCard(ctx, o, "offer revised")
	return toAPIOffer(o, ""), nil
}

func (s *Service) CancelOffer(ctx context.Context, req orderapi.OfferRequest) error {
	o, err := s.party(ctx, req.ActorID, req.ID)
	if err != nil {
		return err
	}
	if err := o.Cancel(req.ActorID.Int64()); err != nil {
		return err
	}
	if err := s.repo.SaveOffer(ctx, o); err != nil {
		return fmt.Errorf("save offer: %w", err)
	}
	s.postOfferCard(ctx, o, "offer withdrawn")
	return nil
}

// AcceptOffer is the buyer closing the negotiation, which opens exactly the checkout a
// fixed-price sale uses. The seller is not asked again: accepting is the last decision
// either party makes about whether the sale happens.
func (s *Service) AcceptOffer(ctx context.Context, req orderapi.AcceptOfferRequest) (orderapi.CheckoutResult, error) {
	o, err := s.party(ctx, req.ActorID, req.ID)
	if err != nil {
		return orderapi.CheckoutResult{}, err
	}
	listing, _, err := s.variantOf(ctx, req.ActorID, id.Of[id.Variant](o.VariantID))
	if err != nil {
		return orderapi.CheckoutResult{}, err
	}
	if err := s.transportOption(ctx, req.TransportOption); err != nil {
		return orderapi.CheckoutResult{}, err
	}
	if err := o.Accept(req.ActorID.Int64(), time.Now()); err != nil {
		return orderapi.CheckoutResult{}, err
	}
	address, err := s.contactSnapshot(ctx, req.ActorID, req.ContactID)
	if err != nil {
		return orderapi.CheckoutResult{}, err
	}
	if err := s.catalog.ReserveStock(ctx, catalogapi.StockMovementRequest{
		VariantID: id.Of[id.Variant](o.VariantID), Units: o.Quantity,
	}); err != nil {
		return orderapi.CheckoutResult{}, fmt.Errorf("reserve stock: %w", err)
	}
	release := func() {
		if err := s.catalog.ReleaseStock(ctx, catalogapi.StockMovementRequest{
			VariantID: id.Of[id.Variant](o.VariantID), Units: o.Quantity,
		}); err != nil {
			s.log.Error("release stock after failed acceptance", "err", err)
		}
	}

	item, err := domain.NewItem(domain.FromOffer(o.ID), o.BuyerID, o.SellerID, o.ListingID,
		o.VariantID, address, req.Note, listing.Currency, o.Quantity, req.TransportOption,
		o.Total, 1)
	if err != nil {
		release()
		return orderapi.CheckoutResult{}, err
	}
	session, err := s.finance.OpenCheckout(ctx, financeapi.OpenCheckoutRequest{
		BuyerID:  id.Of[id.Account](o.BuyerID),
		SellerID: id.Of[id.Account](o.SellerID),
		Currency: listing.Currency,
		Total:    o.Total,
		Note:     listing.Name,
		Data:     checkoutContext(domain.FromOffer(o.ID)),
	})
	if err != nil {
		release()
		return orderapi.CheckoutResult{}, fmt.Errorf("open checkout: %w", err)
	}
	item.PaymentSessionID = session.ID.Int64()
	lines := []*domain.Item{&item}
	if err := s.repo.InsertItems(ctx, lines); err != nil {
		release()
		return orderapi.CheckoutResult{}, fmt.Errorf("insert items: %w", err)
	}
	sessionID := session.ID.Int64()
	o.PaymentSessionID = &sessionID
	// The offer's status already moved to accepted, so this write is the same transition the
	// WHERE clause guards: a double-clicked acceptance loses here.
	if err := s.repo.SaveOffer(ctx, o); err != nil {
		release()
		return orderapi.CheckoutResult{}, fmt.Errorf("save offer: %w", err)
	}
	s.postOfferCard(ctx, o, "offer accepted")
	s.timer("start checkout", s.workflows.StartCheckout(ctx, sessionID))
	return s.checkoutResult(lines, session), nil
}

// party reads a negotiation the caller is in. Somebody else's is not found rather than
// forbidden — it is not theirs to know about.
func (s *Service) party(ctx context.Context, actorID id.ID[id.Account], offerID id.ID[id.Offer]) (domain.Offer, error) {
	o, err := s.repo.FindOffer(ctx, offerID.Int64())
	if err != nil {
		return domain.Offer{}, fmt.Errorf("find offer: %w", err)
	}
	if !o.Involves(actorID.Int64()) {
		return domain.Offer{}, domain.ErrOfferNotFound
	}
	return o, nil
}

// postOfferCard puts the negotiation's card into the pair's thread. Best-effort: the terms
// are already written, and a chat that is down must not undo a price both sides agreed.
func (s *Service) postOfferCard(ctx context.Context, o domain.Offer, body string) {
	_, err := s.chat.PostSystemMessage(ctx, chatapi.PostSystemMessageRequest{
		AccountAID: id.Of[id.Account](o.BuyerID),
		AccountBID: id.Of[id.Account](o.SellerID),
		Body:       body,
		// The id and nothing else: copying the price in would let a counter-offer leave the
		// thread showing terms that are no longer on the table.
		Card: map[string]any{"offer_id": id.Of[id.Offer](o.ID).String()},
	})
	if err != nil {
		s.log.Error("post offer card failed", "offer_id", o.ID, "err", err)
	}
}

func toAPIOffer(o domain.Offer, currency string) orderapi.Offer {
	return orderapi.Offer{
		ID:        id.Of[id.Offer](o.ID),
		ListingID: id.Of[id.Listing](o.ListingID),
		VariantID: id.Of[id.Variant](o.VariantID),
		BuyerID:   id.Of[id.Account](o.BuyerID),
		SellerID:  id.Of[id.Account](o.SellerID),
		AuthorID:  id.Of[id.Account](o.AuthorID),
		Status:    o.Status,
		Quantity:  o.Quantity,
		Total:     o.Total,
		Currency:  currency,
		Reason:    o.Reason,
		CreatedAt: o.CreatedAt,
		ExpiresAt: o.ExpiresAt,
	}
}
