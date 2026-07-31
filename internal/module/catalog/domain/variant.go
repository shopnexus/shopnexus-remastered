package domain

import (
	"time"

	"shopnexus/internal/shared/validation"
)

// Variant is a purchasable variant of a listing: the row that carries a price and, through
// Stock, the units on hand. IsFeatured lives here rather than as a pointer on the listing,
// so "the featured variant belongs to this listing" is not a rule anybody enforces.
type Variant struct {
	ID             int64
	ListingID      int64
	Price          int64          `validate:"gte=1"`
	Attributes     map[string]any `validate:"required,min=1"`
	PackageDetails map[string]any `validate:"required"`
	Attachments    []int64
	IsFeatured     bool
	Stock          Stock
	CreatedAt      time.Time
	// DeletedAt is a soft delete: order.item holds variant_id without a foreign key, so a
	// past order has to stay resolvable.
	DeletedAt *time.Time
}

type NewVariantInput struct {
	Price          int64
	Attributes     map[string]any
	PackageDetails map[string]any
	Attachments    []int64
	Quantity       int64
}

func NewVariant(in NewVariantInput) (*Variant, error) {
	v := &Variant{
		Price:          in.Price,
		Attributes:     in.Attributes,
		PackageDetails: in.PackageDetails,
		Attachments:    in.Attachments,
		Stock:          Stock{Quantity: in.Quantity},
	}
	if v.PackageDetails == nil {
		v.PackageDetails = map[string]any{}
	}
	if err := v.Validate(); err != nil {
		return nil, err
	}
	return v, nil
}

func (v *Variant) Validate() error {
	if err := validation.Default().Struct(v); err != nil {
		return validation.AsError(err)
	}
	if v.Stock.Quantity < 0 || v.Stock.Reserved < 0 || v.Stock.Sold < 0 {
		return ErrQuantityBelowCommitted
	}
	if v.Stock.Committed() > v.Stock.Quantity {
		return ErrQuantityBelowCommitted
	}
	return nil
}

// IsLive reports whether the variant still counts: a soft-deleted one is kept only so an
// order that names it can be rendered.
func (v *Variant) IsLive() bool { return v.DeletedAt == nil }
