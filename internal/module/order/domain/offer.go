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
		return Offer{}, ErrSellerCannotOffer
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

// Accept agrees to the terms on the table. Whoever does *not* own the standing proposal —
// because the two sides alternate, so the price in front of you is always the other party's, and
// either of them may be the one who says yes to it.
//
// Agreeing is not the sale. It freezes the price and starts a short window for the buyer to turn
// it into an order, which is where they choose delivery and pay — exactly as they would from a
// fixed-price listing. That is why the seller accepting a buyer's price is safe: nothing is
// charged and no order exists until the buyer checks out.
func (o *Offer) Accept(actorID int64, now time.Time, window time.Duration) error {
	if err := o.movable(actorID, now); err != nil {
		return err
	}
	if o.AuthorID == actorID {
		return ErrNotYourTurn
	}
	o.Status = OfferAccepted
	// The clock restarts, short: an accepted price is a frozen price, and the same reason a
	// draft expires in half an hour applies here.
	o.ExpiresAt = now.Add(window)
	return nil
}

// CheckoutBy reports the error stopping this account from turning the accepted offer into an
// order. Only the buyer, and only while the accepted price is still good: they are the one who
// pays, and a seller has no checkout to perform.
func (o Offer) CheckoutBy(actorID int64, now time.Time) error {
	if !o.Involves(actorID) {
		return ErrOfferNotFound
	}
	if o.Status != OfferAccepted {
		return ErrOfferNotAccepted
	}
	if actorID != o.BuyerID {
		return ErrOnlyBuyerCheckout
	}
	if !now.Before(o.ExpiresAt) {
		return ErrOfferExpired
	}
	if o.PaymentSessionID != nil {
		return ErrOfferSettled
	}
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

// Expire is what the timer does. Separate from Cancel because nobody decided it, and the two
// read differently to a client counting down.
//
// An accepted offer expires too: the price was frozen for a short window, and a buyer who did not
// check out in it has to negotiate again rather than hold yesterday's price open.
func (o *Offer) Expire() error {
	if o.Status == OfferCancelled {
		return ErrOfferSettled
	}
	if o.Status == OfferAccepted && o.PaymentSessionID != nil {
		// Already checked out: the sale is the session's business now.
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
