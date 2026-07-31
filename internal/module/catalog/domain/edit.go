package domain

import "slices"

// PendingEdit is the editable subset of a listing, held until a moderator applies it. Its
// JSON is the `pending_edit` column, so a field name here is a stored contract.
//
// It holds no variants on purpose: moderation reviews what the listing claims to be, and a
// seller adjusting a price or restocking a size must not wait on a human.
type PendingEdit struct {
	Name           *string         `json:"name,omitempty"`
	Description    *string         `json:"description,omitempty"`
	CategoryID     *int64          `json:"category_id,omitempty"`
	Condition      *Condition      `json:"condition,omitempty"`
	PriceMode      *PriceMode      `json:"price_mode,omitempty"`
	ShippingPaidBy *ShippingPaidBy `json:"shipping_paid_by,omitempty"`
	Specifications map[string]any  `json:"specifications,omitempty"`
	Attachments    []int64         `json:"attachments,omitempty"`
	Tags           []string        `json:"tags,omitempty"`
}

// Fields names what the edit touches, for the trail and for a moderator's diff.
func (e PendingEdit) Fields() []string {
	var out []string
	for name, set := range map[string]bool{
		"name":             e.Name != nil,
		"description":      e.Description != nil,
		"category_id":      e.CategoryID != nil,
		"condition":        e.Condition != nil,
		"price_mode":       e.PriceMode != nil,
		"shipping_paid_by": e.ShippingPaidBy != nil,
		"specifications":   e.Specifications != nil,
		"attachments":      e.Attachments != nil,
		"tags":             e.Tags != nil,
	} {
		if set {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

// IsEmpty reports whether there is anything to review.
func (e PendingEdit) IsEmpty() bool { return len(e.Fields()) == 0 }
