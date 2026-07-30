// Package domain: catalog entity + pure business rules.
package domain

import (
	"time"

	"shopnexus/internal/shared/validation"
)

const StatusActive = "active"

type Listing struct {
	ID        int64
	OwnerID   int64  `validate:"required"`
	Title     string `validate:"required"`
	Price     int64  `validate:"gt=0"`
	Status    string
	CreatedAt time.Time
}

func NewListing(ownerID int64, title string, price int64) (Listing, error) {
	l := Listing{OwnerID: ownerID, Title: title, Price: price, Status: StatusActive}
	if err := validation.Default().Struct(l); err != nil {
		return Listing{}, validation.AsError(err)
	}
	return l, nil
}
