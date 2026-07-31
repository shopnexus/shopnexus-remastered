package domain

// Stock is what one variant has on hand and what is already committed. It is written by
// guarded statements rather than through the listing aggregate: reserving moves on every
// checkout while the variant row moves when a seller edits, so sharing a version would
// make every reservation collide with every edit.
//
// Two counters, not one: reserved comes back when a checkout is abandoned, sold never
// goes down. One combined column made "how many sold" a number that inflates with open
// carts.
type Stock struct {
	Quantity int64 `validate:"gte=0"`
	Reserved int64 `validate:"gte=0"`
	Sold     int64 `validate:"gte=0"`
}

// Available is what a buyer may still take.
func (s Stock) Available() int64 { return s.Quantity - s.Reserved - s.Sold }

// Committed is what a seller can no longer take away.
func (s Stock) Committed() int64 { return s.Reserved + s.Sold }

// SetQuantity refuses a total below what is already committed, because an oversold row
// must not be representable.
func (s *Stock) SetQuantity(quantity int64) error {
	if quantity < 0 || quantity < s.Committed() {
		return ErrQuantityBelowCommitted
	}
	s.Quantity = quantity
	return nil
}
