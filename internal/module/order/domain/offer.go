package domain

import (
	"time"

	"shopnexus/internal/shared/validation"
)

// Offer statuses (kebab-case, mirrors the offer_status enum).
const (
	OfferActive    = "active"
	OfferAccepted  = "accepted"
	OfferCancelled = "cancelled"
)

// Offer is a negotiation on one variant: the terms currently on the table, revised in
// place rather than by stacking rows, so "what is being offered" is one row and not a scan
// of a thread.
//
// The conversation happens in chat — each revision posts a card carrying this row's id —
// but the terms, the status and the expiry live here because they decide money.
type Offer struct {
	ID        int64
	ListingID int64 `validate:"required"`
	VariantID int64 `validate:"required"`
	// AuthorID is whoever put the standing proposal on the table. The two sides alternate,
	// so a price is always the other party's to answer.
	AuthorID int64  `validate:"required"`
	BuyerID  int64  `validate:"required"`
	SellerID int64  `validate:"required"`
	Status   string `validate:"required,oneof=active accepted cancelled"`
	Quantity int64  `validate:"required,gt=0"`
	Total    int64  `validate:"required,gt=0"`
	Reason   string
	// PaymentSessionID is set when the buyer accepts and the checkout opens.
	PaymentSessionID *int64
	CreatedAt        time.Time
	ExpiresAt        time.Time `validate:"required"`
}

// NewOffer opens a negotiation. Either side may start it — the buyer from the listing
// page, the seller from a thread — and whoever does owns the standing proposal.
func NewOffer(listingID, variantID, authorID, buyerID, sellerID, quantity, total int64, reason string, window time.Duration) (Offer, error) {
	o := Offer{
		ListingID: listingID, VariantID: variantID, AuthorID: authorID,
		BuyerID: buyerID, SellerID: sellerID, Status: OfferActive,
		Quantity: quantity, Total: total, Reason: reason,
		ExpiresAt: time.Now().Add(window),
	}
	if buyerID == sellerID {
		return Offer{}, ErrOnlyBuyerAccepts
	}
	if authorID != buyerID && authorID != sellerID {
		return Offer{}, ErrNotYourTurn
	}
	if err := validation.Default().Struct(o); err != nil {
		return Offer{}, validation.AsError(err)
	}
	return o, nil
}

// Live reports whether the negotiation is still open to a move.
func (o Offer) Live(now time.Time) bool {
	return o.Status == OfferActive && now.Before(o.ExpiresAt)
}

// Involves reports whether the account is one of the two parties.
func (o Offer) Involves(accountID int64) bool {
	return accountID != 0 && (o.BuyerID == accountID || o.SellerID == accountID)
}

// Counter revises the terms and moves authorship. Only the party that does not own the
// standing proposal may counter, so the two sides alternate and a price on the table is
// always somebody else's to answer.
func (o *Offer) Counter(actorID, quantity, total int64, reason string, now time.Time, window time.Duration) error {
	if err := o.movable(actorID, now); err != nil {
		return err
	}
	if o.AuthorID == actorID {
		return ErrNotYourTurn
	}
	if quantity <= 0 || total <= 0 {
		return errQuantityPositive
	}
	o.AuthorID, o.Quantity, o.Total = actorID, quantity, total
	if reason != "" {
		o.Reason = reason
	}
	// A counter restarts the clock: the other side is being asked something new.
	o.ExpiresAt = now.Add(window)
	return nil
}

// Accept closes the negotiation. Only the buyer, whichever side proposed the standing
// terms — an order may not appear without the buyer's explicit acceptance, and the seller
// putting a price up is an offer rather than a sale.
func (o *Offer) Accept(actorID int64, now time.Time) error {
	if err := o.movable(actorID, now); err != nil {
		return err
	}
	if actorID != o.BuyerID {
		return ErrOnlyBuyerAccepts
	}
	o.Status = OfferAccepted
	return nil
}

// Cancel walks away. Either party may, because a negotiation neither side wants is over.
func (o *Offer) Cancel(actorID int64) error {
	if !o.Involves(actorID) {
		return ErrOfferNotFound
	}
	if o.Status != OfferActive {
		return ErrOfferSettled
	}
	o.Status = OfferCancelled
	return nil
}

// Expire is what the timer does. Separate from Cancel because nobody decided it, and the
// two read differently to a client counting down.
func (o *Offer) Expire() error {
	if o.Status != OfferActive {
		return ErrOfferSettled
	}
	o.Status = OfferCancelled
	return nil
}

func (o Offer) movable(actorID int64, now time.Time) error {
	if !o.Involves(actorID) {
		return ErrOfferNotFound
	}
	if o.Status != OfferActive {
		return ErrOfferSettled
	}
	if !now.Before(o.ExpiresAt) {
		return ErrOfferExpired
	}
	return nil
}
