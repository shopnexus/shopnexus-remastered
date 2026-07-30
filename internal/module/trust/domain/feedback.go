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

// Feedback is one rating in one direction for one completed order. It stays
// blind (PublishedAt nil) until both sides rated or the window closed, so a
// rating cannot be retaliatory.
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

// Reputation is the per-account, per-role aggregate. The average is computed on
// read so the stored counters stay append-friendly.
type Reputation struct {
	AccountID       int64
	Role            string
	RatingSum       int64
	RatingCount     int64
	CompletedOrders int64
	CancelledOrders int64
	UpdatedAt       time.Time
}

// AverageRating returns 0 when nobody has rated yet.
func (r Reputation) AverageRating() float64 {
	if r.RatingCount == 0 {
		return 0
	}
	return float64(r.RatingSum) / float64(r.RatingCount)
}
