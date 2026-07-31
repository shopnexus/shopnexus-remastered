package domain

import (
	"time"

	"shopnexus/internal/shared/validation"
)

// CartItem is one saved intention to buy. Keyed by (account, variant), so adding the same
// variant twice changes the quantity rather than stacking rows — which is what a cart
// means and what the unique constraint enforces.
//
// The listing is denormalised on insert: rendering a cart means reading listings, and a
// variant is not addressable on its own in the catalog API.
type CartItem struct {
	ID        int64
	AccountID int64 `validate:"required"`
	ListingID int64 `validate:"required"`
	VariantID int64 `validate:"required"`
	Quantity  int64 `validate:"required,gt=0"`
	CreatedAt time.Time
}

func NewCartItem(accountID, listingID, variantID, quantity int64) (CartItem, error) {
	c := CartItem{
		AccountID: accountID, ListingID: listingID, VariantID: variantID, Quantity: quantity,
	}
	if err := validation.Default().Struct(c); err != nil {
		return CartItem{}, validation.AsError(err)
	}
	return c, nil
}

// SetQuantity is the only edit a cart row has. Zero is not "remove": that is a DELETE, so
// there is one way to spell each intention.
func (c *CartItem) SetQuantity(quantity int64) error {
	if quantity <= 0 {
		return errQuantityPositive
	}
	c.Quantity = quantity
	return nil
}
