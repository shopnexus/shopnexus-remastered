package base

import "github.com/google/uuid"

// Cross-boundary types shared by several domain packages. They live here (not
// in their "owning" domain) to keep the import graph acyclic: e.g. the
// checkout workflow depends on the transport handler, so the item type they
// both speak cannot live in either.

// CheckoutItem is one line in a buyer checkout — used by the checkout workflow
// input, the transport quote endpoint, and the buyer checkout-summary handler.
type CheckoutItem struct {
	SkuID           uuid.UUID `json:"sku_id"           validate:"required"`
	Quantity        int64     `json:"quantity"         validate:"required,gt=0,max=100000"`
	TransportOption string    `json:"transport_option" validate:"required,min=1,max=100"`
	Note            string    `json:"note"             validate:"max=500"`
}
