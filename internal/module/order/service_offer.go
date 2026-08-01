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
	"shopnexus/internal/provider/transport"
	"shopnexus/internal/shared/id"
)

// checkoutSource is what a payment session carries back to this module when it completes:
// which sale it was paying for. Stored on the session by the opener and handed to the
// subscriber, so settling needs no lookup by amount or by guesswork.
type checkoutSource struct {
	DraftID *int64 `json:"draft_id,omitempty"`
	OfferID *int64 `json:"offer_id,omitempty"`
	// ShippingFee is what the buyer agreed to pay for delivery, quoted at checkout and carried
	// here because the session is the thing they paid against. The settle path needs it to open
	// the shipment with the right fee and to keep it out of the seller's escrow.
	ShippingFee int64 `json:"shipping_fee,omitempty"`
}

func checkoutContext(origin domain.Origin, shippingFee int64) []byte {
	raw, err := json.Marshal(checkoutSource{
		DraftID: origin.DraftID, OfferID: origin.OfferID, ShippingFee: shippingFee,
	})
	if err != nil {
		// The shape is two optional numbers; there is nothing here that can fail to encode.
		return []byte("{}")
	}
	return raw
}

// decodeShippingFee reads back what the buyer paid for delivery. Zero when the session predates
// the fee or carried none, which is the same thing as free delivery to every caller.
func decodeShippingFee(raw []byte) int64 {
	var src checkoutSource
	if len(raw) == 0 || json.Unmarshal(raw, &src) != nil {
		return 0
	}
	return src.ShippingFee
}

// The chat cards a negotiation posts. A closed set, named because the expiry posts the same body
// from two places and a card whose wording drifts reads as a different event.
const (
	cardOfferOpened    = "offer opened"
	cardOfferRevised   = "offer revised"
	cardOfferWithdrawn = "offer withdrawn"
	cardOfferAccepted  = "offer accepted"
	cardOfferExpired   = "offer expired"
)

// CreateOffer opens a negotiation on a variant of a `negotiable` listing. The buyer's route: a
// seller has nobody to propose to on their own listing, so they answer the terms instead.
//
// The card goes into the pair's chat thread: the conversation is chat's, the terms are this
// row's, and the message carries only the offer's id so a counter cannot leave an old price
// on screen.
func (s *Service) CreateOffer(ctx context.Context, req orderapi.CreateOfferRequest) (orderapi.Offer, error) {
	listing, _, err := s.variantOf(ctx, req.ActorID, req.VariantID)
	if err != nil {
		return orderapi.Offer{}, err
	}
	if listing.PriceMode != catalogapi.PriceModeNegotiable {
		return orderapi.Offer{}, domain.ErrFixedPriceListing
	}
	sellerID := listing.Seller.ID
	if req.ActorID == sellerID {
		// The seller opening it means they are proposing to somebody; without a buyer there
		// is nobody to propose to, so this route is the buyer's to start.
		return orderapi.Offer{}, domain.ErrSellerCannotOffer
	}
	o, err := domain.NewOffer(domain.NewTerms{
		ListingID: listing.ID.Int64(),
		VariantID: req.VariantID.Int64(),
		BuyerID:   req.ActorID.Int64(),
		SellerID:  sellerID.Int64(),
		Quantity:  req.Quantity,
		Total:     req.Total,
		Reason:    req.Reason,
	}, offerWindow)
	if err != nil {
		return orderapi.Offer{}, err
	}
	if err := s.repo.InsertOffer(ctx, &o); err != nil {
		return orderapi.Offer{}, fmt.Errorf("insert offer: %w", err)
	}
	s.postOfferCard(ctx, o, cardOfferOpened)
	// The standing proposal is on a clock from here; accepting restarts it, and the run re-reads
	// the row rather than holding the deadline it first saw.
	s.timer("start offer", s.workflows.StartOffer(ctx, o.ID))
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
	rows, meta := page(rows, req.Limit, func(o domain.Offer) (time.Time, int64) {
		return o.CreatedAt, o.ID
	})
	currencies, err := s.listingCurrencies(ctx, req.ActorID, rows)
	if err != nil {
		return orderapi.OfferPage{}, err
	}
	out := make([]orderapi.Offer, 0, len(rows))
	for _, o := range rows {
		out = append(out, toAPIOffer(o, currencies[o.ListingID]))
	}
	return orderapi.OfferPage{Data: out, Meta: meta}, nil
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
	// From `active` only: a counter that read the row before somebody else's acceptance landed
	// loses, rather than putting terms back on a table that was already agreed.
	if err := s.repo.SaveOffer(ctx, o, []string{domain.OfferActive}); err != nil {
		return orderapi.Offer{}, fmt.Errorf("save offer: %w", err)
	}
	s.postOfferCard(ctx, o, cardOfferRevised)
	// The currency is the listing's, resolved like every other offer answer: a total with nothing
	// beside it is not a price, and `required` in the contract means this route cannot skip it.
	currencies, err := s.listingCurrencies(ctx, req.ActorID, []domain.Offer{o})
	if err != nil {
		return orderapi.Offer{}, err
	}
	return toAPIOffer(o, currencies[o.ListingID]), nil
}

func (s *Service) CancelOffer(ctx context.Context, req orderapi.OfferRequest) error {
	o, err := s.party(ctx, req.ActorID, req.ID)
	if err != nil {
		return err
	}
	if err := o.Cancel(req.ActorID.Int64()); err != nil {
		return err
	}
	if err := s.repo.SaveOffer(ctx, o, []string{domain.OfferActive}); err != nil {
		return fmt.Errorf("save offer: %w", err)
	}
	s.postOfferCard(ctx, o, cardOfferWithdrawn)
	return nil
}

// AcceptOffer agrees to the terms on the table. Whoever does not own the standing proposal — the
// two sides alternate, so either of them may be the one who says yes.
//
// It is not the sale, and nothing is charged: it freezes the price and starts a short window for
// the buyer to press "create order now", where they choose delivery and pay exactly as they would
// from a fixed-price listing. That separation is what makes a seller accepting a buyer's price
// safe, and it is why a negotiated sale and a fixed-price one end up in the same checkout.
func (s *Service) AcceptOffer(ctx context.Context, req orderapi.OfferRequest) (orderapi.Offer, error) {
	o, err := s.party(ctx, req.ActorID, req.ID)
	if err != nil {
		return orderapi.Offer{}, err
	}
	if err := o.Accept(req.ActorID.Int64(), time.Now(), acceptedWindow); err != nil {
		return orderapi.Offer{}, err
	}
	if err := s.repo.SaveOffer(ctx, o, []string{domain.OfferActive}); err != nil {
		return orderapi.Offer{}, fmt.Errorf("save offer: %w", err)
	}
	s.postOfferCard(ctx, o, cardOfferAccepted)
	// The frozen price is on a clock, and the run that closes it is the same one a standing
	// proposal had: the row carries its own deadline either way.
	s.timer("start offer", s.workflows.StartOffer(ctx, o.ID))
	// The currency is the listing's, resolved for the same reason a read resolves it: a total
	// with nothing beside it is not a price, and this answer is the agreed one.
	currencies, err := s.listingCurrencies(ctx, req.ActorID, []domain.Offer{o})
	if err != nil {
		return orderapi.Offer{}, err
	}
	return toAPIOffer(o, currencies[o.ListingID]), nil
}

// CheckoutOffer is the buyer's "create order now": the agreed price, plus the delivery they choose
// here and pay for. The same shape as a fixed-price checkout, deliberately — a negotiated sale
// differs only in where its price came from, and the buyer pays for delivery on both.
func (s *Service) CheckoutOffer(ctx context.Context, req orderapi.CheckoutOfferRequest) (orderapi.CheckoutResult, error) {
	if err := s.v.Struct(req); err != nil {
		return orderapi.CheckoutResult{}, err
	}
	o, err := s.party(ctx, req.ActorID, req.ID)
	if err != nil {
		return orderapi.CheckoutResult{}, err
	}
	// Buyer only, only once, and only while the agreed price is still good.
	now := time.Now()
	if err := o.CheckoutBy(req.ActorID.Int64(), now); err != nil {
		return orderapi.CheckoutResult{}, err
	}
	listing, _, err := s.variantOf(ctx, req.ActorID, id.Of[id.Variant](o.VariantID))
	if err != nil {
		return orderapi.CheckoutResult{}, err
	}
	if err := s.transportOption(ctx, req.TransportOption); err != nil {
		return orderapi.CheckoutResult{}, err
	}
	address, err := s.contactSnapshot(ctx, req.ActorID, req.ContactID)
	if err != nil {
		return orderapi.CheckoutResult{}, err
	}

	// The terms are claimed before anything is reserved or charged, exactly as a draft is spent
	// before its checkout: the write is the claim, so a double-clicked "create order now" opens
	// one payment session and the loser is refused. Claiming afterwards would let both presses
	// open one and only the last write lose — and two paid sessions on one negotiation is money
	// the escrow cannot account for.
	if err := s.repo.ClaimOfferCheckout(ctx, o.ID, now); err != nil {
		return orderapi.CheckoutResult{}, err
	}
	o.CheckOut()
	// Whatever fails from here hands the claim back, so the buyer retries inside the window they
	// still have instead of having to negotiate the price again.
	unclaim := func() {
		if err := s.repo.ReleaseOfferCheckout(ctx, o.ID); err != nil {
			s.log.Error("release offer claim after failed checkout", "offer_id", o.ID, "err", err)
		}
	}
	if err := s.catalog.ReserveStock(ctx, catalogapi.StockMovementRequest{
		VariantID: id.Of[id.Variant](o.VariantID), Units: o.Quantity,
	}); err != nil {
		unclaim()
		return orderapi.CheckoutResult{}, fmt.Errorf("reserve stock: %w", err)
	}
	release := func() {
		unclaim()
		if err := s.catalog.ReleaseStock(ctx, catalogapi.StockMovementRequest{
			VariantID: id.Of[id.Variant](o.VariantID), Units: o.Quantity,
		}); err != nil {
			s.log.Error("release stock after failed offer checkout", "err", err)
		}
	}

	item, err := domain.NewItem(domain.NewLine{
		Origin:          domain.FromOffer(o.ID),
		BuyerID:         o.BuyerID,
		SellerID:        o.SellerID,
		ListingID:       o.ListingID,
		VariantID:       o.VariantID,
		Address:         address,
		Note:            req.Note,
		Currency:        listing.Currency,
		Quantity:        o.Quantity,
		TransportOption: req.TransportOption,
		Total:           o.Total,
	})
	if err != nil {
		release()
		return orderapi.CheckoutResult{}, err
	}
	// Delivery, priced from the carrier for this parcel to this address — the negotiated price
	// covers the goods and nothing else.
	fee, err := s.quoteShipping(ctx, req.TransportOption, o.SellerID, address,
		[]transport.ItemMetadata{{VariantID: o.VariantID, Quantity: o.Quantity}})
	if err != nil {
		release()
		return orderapi.CheckoutResult{}, err
	}
	session, err := s.finance.OpenCheckout(ctx, financeapi.OpenCheckoutRequest{
		BuyerID:  id.Of[id.Account](o.BuyerID),
		SellerID: id.Of[id.Account](o.SellerID),
		Currency: listing.Currency,
		Total:    o.Total + fee,
		Note:     listing.Name,
		Data:     checkoutContext(domain.FromOffer(o.ID), fee),
	})
	if err != nil {
		release()
		return orderapi.CheckoutResult{}, fmt.Errorf("open checkout: %w", err)
	}
	sessionID := session.ID.Int64()
	item.PaymentSessionID = sessionID
	lines := []*domain.Item{&item}
	if err := s.repo.InsertItems(ctx, lines); err != nil {
		release()
		return orderapi.CheckoutResult{}, fmt.Errorf("insert items: %w", err)
	}
	// Which checkout the claim became. Recorded after it exists rather than as the claim, so the
	// session the buyer is paying is never guessed.
	o.PaymentSessionID = &sessionID
	if err := s.repo.AttachOfferSession(ctx, o.ID, sessionID); err != nil {
		return orderapi.CheckoutResult{}, err
	}
	s.timer("start checkout", s.workflows.StartCheckout(ctx, sessionID))
	return s.checkoutResult(lines, session, fee), nil
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
