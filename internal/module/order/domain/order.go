// Package domain: order entity + pure business rules.
package domain

import (
	"time"

	"shopnexus/internal/shared/validation"
)

const StatusPending = "pending"

type Order struct {
	ID        int64
	BuyerID   int64 `validate:"required"`
	Total     int64 `validate:"gt=0"`
	Status    string
	CreatedAt time.Time
}

func NewOrder(buyerID, total int64) (Order, error) {
	o := Order{BuyerID: buyerID, Total: total, Status: StatusPending}
	if err := validation.Default().Struct(o); err != nil {
		return Order{}, validation.AsError(err)
	}
	return o, nil
}
