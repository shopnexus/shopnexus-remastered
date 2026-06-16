package ordermodel

import (
	"database/sql/driver"
	"fmt"
)

// Status is the lifecycle status shared by payment sessions and transactions
// (and, nullable, by transport). Stored as the "order"."order_status" enum.
type Status string

const (
	StatusPending    Status = "Pending"
	StatusProcessing Status = "Processing"
	StatusSuccess    Status = "Success"
	StatusCancelled  Status = "Cancelled"
	StatusFailed     Status = "Failed"
)

func (e *Status) Scan(src any) error          { return scanEnum((*string)(e), src) }
func (e Status) Value() (driver.Value, error) { return string(e), nil }

// RefundStatus is the refund-request lifecycle ("order"."refund_status").
type RefundStatus string

const (
	RefundStatusShipping             RefundStatus = "Shipping"
	RefundStatusAwaitingSellerReview RefundStatus = "AwaitingSellerReview"
	RefundStatusDisputed             RefundStatus = "Disputed"
	RefundStatusAccepted             RefundStatus = "Accepted"
	RefundStatusRejected             RefundStatus = "Rejected"
	RefundStatusCancelled            RefundStatus = "Cancelled"
)

func (e *RefundStatus) Scan(src any) error          { return scanEnum((*string)(e), src) }
func (e RefundStatus) Value() (driver.Value, error) { return string(e), nil }

// DisputeStatus is the dispute-resolution status ("order"."dispute_status").
type DisputeStatus string

const (
	DisputeStatusOpen       DisputeStatus = "Open"
	DisputeStatusSellerWins DisputeStatus = "SellerWins"
	DisputeStatusBuyerWins  DisputeStatus = "BuyerWins"
)

func (e *DisputeStatus) Scan(src any) error          { return scanEnum((*string)(e), src) }
func (e DisputeStatus) Value() (driver.Value, error) { return string(e), nil }

// scanEnum copies a text enum value out of a pgx/database-sql source. nil → "".
func scanEnum(dst *string, src any) error {
	switch s := src.(type) {
	case []byte:
		*dst = string(s)
	case string:
		*dst = s
	case nil:
		*dst = ""
	default:
		return fmt.Errorf("unsupported scan type for enum: %T", src)
	}
	return nil
}
