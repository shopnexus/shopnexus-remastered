// Package domain: trust & safety entities + pure business rules.
package domain

import (
	"time"

	"shopnexus/internal/shared/validation"
)

// Feedback directions and reputation roles (kebab-case, mirror the enums).
const (
	DirectionBuyerToSeller = "buyer-to-seller"
	DirectionSellerToBuyer = "seller-to-buyer"

	RoleSeller = "seller"
	RoleBuyer  = "buyer"
)

// BlindWindow is how long a rating stays hidden while only one side has submitted. After
// it, whatever was submitted becomes visible whether or not the other side answered —
// otherwise a party who simply never rates could keep the other's rating hidden for ever.
const BlindWindow = 14 * 24 * time.Hour

// Feedback is one rating in one direction for one completed order. It stays blind
// (PublishedAt nil) until both sides rated or the window closed, so nobody can read what
// they are being rated on before they rate.
type Feedback struct {
	ID          int64
	OrderID     int64  `validate:"required"`
	RaterID     int64  `validate:"required"`
	RateeID     int64  `validate:"required"`
	Direction   string `validate:"required,oneof=buyer-to-seller seller-to-buyer"`
	Rating      int16  `validate:"required,gte=1,lte=5"`
	Comment     string `validate:"max=2000"`
	CreatedAt   time.Time
	PublishedAt *time.Time
}

func NewFeedback(orderID, raterID, rateeID int64, direction string, rating int16, comment string) (Feedback, error) {
	if raterID == rateeID {
		return Feedback{}, ErrSelfFeedback
	}
	f := Feedback{
		OrderID:   orderID,
		RaterID:   raterID,
		RateeID:   rateeID,
		Direction: direction,
		Rating:    rating,
		Comment:   comment,
	}
	if err := validation.Default().Struct(f); err != nil {
		return Feedback{}, validation.AsError(err)
	}
	return f, nil
}

// Published reports whether this rating is visible and counted.
func (f Feedback) Published() bool { return f.PublishedAt != nil }

// Publish reveals a rating. Idempotent: a second call would move the timestamp a
// reputation recompute keys off, and it is asking for the state the row is already in.
func (f *Feedback) Publish(at time.Time) {
	if f.PublishedAt == nil {
		f.PublishedAt = &at
	}
}

// RevealAt is when this rating goes public even if the other side never answers. Nil once
// it is out, because then there is nothing left to wait for.
func (f Feedback) RevealAt() *time.Time {
	if f.Published() {
		return nil
	}
	return new(f.CreatedAt.Add(BlindWindow))
}

// DirectionFor says which way a rating goes, given which side of the order the rater is on,
// and who receives it. Derived rather than sent: a direction a client picks is a direction
// a client can lie about.
func DirectionFor(raterID, buyerID, sellerID int64) (direction string, rateeID int64, err error) {
	switch raterID {
	case buyerID:
		return DirectionBuyerToSeller, sellerID, nil
	case sellerID:
		return DirectionSellerToBuyer, buyerID, nil
	}
	return "", 0, ErrNotAParty
}

// Opposite is the other direction of the same order, which is what "has the counterparty
// rated yet" is looked up by.
func Opposite(direction string) string {
	if direction == DirectionBuyerToSeller {
		return DirectionSellerToBuyer
	}
	return DirectionBuyerToSeller
}

// RoleRated is the role the ratee was in when they earned this rating: a seller's
// reputation is not the same account's buying record.
func RoleRated(direction string) string {
	if direction == DirectionBuyerToSeller {
		return RoleSeller
	}
	return RoleBuyer
}

// Reputation is the per-account, per-role aggregate. The averages are computed on read so
// the stored counters stay append-friendly, and the two rating pairs stay apart because one
// order can produce both and summing them would count that order twice.
type Reputation struct {
	AccountID int64
	Role      string
	// From feedback: how the counterparty found the transaction. Both roles.
	RatingSum   int64
	RatingCount int64
	// From reviews: how buyers found the goods. Sellers only — nobody reviews a buyer's
	// products, and the table's CHECK says the same.
	ReviewRatingSum   int64
	ReviewRatingCount int64
	CompletedOrders   int64
	CancelledOrders   int64
	UpdatedAt         time.Time
}

// AverageRating returns 0 when nobody has rated yet.
func (r Reputation) AverageRating() float64 { return average(r.RatingSum, r.RatingCount) }

// AverageReviewRating is the product half, always 0 for a buyer.
func (r Reputation) AverageReviewRating() float64 {
	return average(r.ReviewRatingSum, r.ReviewRatingCount)
}

func average(sum, count int64) float64 {
	if count == 0 {
		return 0
	}
	return float64(sum) / float64(count)
}
