package domain

import (
	"encoding/json/v2"
	"time"

	"shopnexus/internal/shared/validation"
)

// Draft is a buyer's purchase session for one fixed-price listing. It freezes the terms,
// so a listing that showed 100k cannot charge a newly-updated price at checkout.
//
// A negotiable listing has no draft: the accepted offer is what freezes its terms.
type Draft struct {
	ID        int64
	BuyerID   int64 `validate:"required"`
	ListingID int64 `validate:"required"`
	// Snapshot is the listing as it was when the session opened, variants included —
	// price and package weight live there, and those are what must not move under the
	// buyer.
	Snapshot    ListingSnapshot
	CreatedAt   time.Time
	CancelledAt *time.Time
	ValidUntil  time.Time `validate:"required"`
}

// ListingSnapshot is the frozen listing. Its JSON is the stored column, so a field name
// here is a stored contract.
type ListingSnapshot struct {
	ListingID int64             `json:"listing_id"`
	SellerID  int64             `json:"seller_id"`
	Name      string            `json:"name"`
	Currency  string            `json:"currency"`
	PriceMode string            `json:"price_mode"`
	Variants  []VariantSnapshot `json:"variants"`
}

// VariantSnapshot is one purchasable line as it was. Price and package details are the
// two things a checkout must not let move.
type VariantSnapshot struct {
	VariantID      int64          `json:"variant_id"`
	Price          int64          `json:"price"`
	Attributes     map[string]any `json:"attributes,omitempty"`
	PackageDetails map[string]any `json:"package_details,omitempty"`
}

// NewDraft opens a session. The window is short by design: a draft that sits open holds
// nothing, but a stale one would let a buyer check out at yesterday's price.
func NewDraft(buyerID int64, snapshot ListingSnapshot, window time.Duration) (Draft, error) {
	d := Draft{
		BuyerID:    buyerID,
		ListingID:  snapshot.ListingID,
		Snapshot:   snapshot,
		ValidUntil: time.Now().Add(window),
	}
	if len(snapshot.Variants) == 0 {
		return Draft{}, ErrVariantNotInDraft
	}
	if err := validation.Default().Struct(d); err != nil {
		return Draft{}, validation.AsError(err)
	}
	return d, nil
}

// Live reports whether the session can still be checked out.
func (d Draft) Live(now time.Time) bool {
	return d.CancelledAt == nil && now.Before(d.ValidUntil)
}

// Cancel closes the session. Only a live one: a cancelled draft is already where the
// caller wants it, and a checked-out one has an order behind it.
func (d *Draft) Cancel() error {
	if d.CancelledAt != nil {
		return ErrDraftSettled
	}
	d.CancelledAt = new(time.Now())
	return nil
}

// Variant finds a frozen line. A variant the session never carried cannot be bought
// through it — that is the whole point of freezing.
func (d Draft) Variant(variantID int64) (VariantSnapshot, error) {
	for _, v := range d.Snapshot.Variants {
		if v.VariantID == variantID {
			return v, nil
		}
	}
	return VariantSnapshot{}, ErrVariantNotInDraft
}

// EncodeSnapshot and DecodeSnapshot are the column's two ends, so the shape is written
// once and read once.
func (d Draft) EncodeSnapshot() ([]byte, error) {
	return json.Marshal(d.Snapshot)
}

func DecodeSnapshot(raw []byte) (ListingSnapshot, error) {
	var out ListingSnapshot
	if len(raw) == 0 {
		return out, nil
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return ListingSnapshot{}, err
	}
	return out, nil
}
