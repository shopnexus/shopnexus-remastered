// Package trustapi is the published contract of the trust service: two-way
// order feedback, per-account reputation, and abuse reports.
package trustapi

import (
	"context"

	"shopnexus/internal/shared/id"
)

type Feedback struct {
	ID        id.ID[id.Feedback] `json:"id"`
	OrderID   id.ID[id.Order]    `json:"order_id"`
	RateeID   id.ID[id.Account]  `json:"ratee_id"`
	Direction string             `json:"direction"`
	Rating    int16              `json:"rating"`
	Comment   string             `json:"comment,omitempty"`
	Published bool               `json:"published"`
}

type Reputation struct {
	AccountID       id.ID[id.Account] `json:"account_id"`
	Role            string            `json:"role"`
	AverageRating   float64           `json:"average_rating"`
	RatingCount     int64             `json:"rating_count"`
	CompletedOrders int64             `json:"completed_orders"`
	CancelledOrders int64             `json:"cancelled_orders"`
}

// Report's RefID is an opaque id whose kind is given by RefType, so it is a
// string here and the service encodes it with that kind's prefix.
type Report struct {
	ID      id.ID[id.Report] `json:"id"`
	RefType string           `json:"ref_type"`
	RefID   string           `json:"ref_id"`
	Reason  string           `json:"reason"`
	Status  string           `json:"status"`
}

type SubmitFeedbackRequest struct {
	RaterID   id.ID[id.Account] `json:"-"` // taken from the token
	OrderID   id.ID[id.Order]   `json:"order_id" validate:"required"`
	RateeID   id.ID[id.Account] `json:"ratee_id" validate:"required"`
	Direction string            `json:"direction" validate:"required,oneof=buyer-to-seller seller-to-buyer"`
	Rating    int16             `json:"rating" validate:"required,gte=1,lte=5"`
	Comment   string            `json:"comment,omitempty" validate:"max=2000"`
}

type GetReputationRequest struct {
	AccountID id.ID[id.Account] `validate:"required"`
	Role      string            `validate:"required,oneof=seller buyer"`
}

// SubmitReportRequest's RefID is opaque and kinded by RefType, as in Report.
type SubmitReportRequest struct {
	ReporterID id.ID[id.Account] `json:"-"` // taken from the token
	RefType    string            `json:"ref_type" validate:"required,oneof=listing account message review review-reply"`
	RefID      string            `json:"ref_id" validate:"required"`
	Reason     string            `json:"reason" validate:"required,oneof=scam counterfeit prohibited harassment spam inappropriate other"`
	Detail     string            `json:"detail,omitempty" validate:"max=2000"`
}

type Service interface {
	SubmitFeedback(ctx context.Context, req SubmitFeedbackRequest) (Feedback, error)
	GetReputation(ctx context.Context, req GetReputationRequest) (Reputation, error)
	SubmitReport(ctx context.Context, req SubmitReportRequest) (Report, error)
}
