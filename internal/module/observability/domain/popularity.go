package domain

// ListingInteractionEvent is one row of catalog.listing_interaction, as far as popularity
// scoring reads it: enough to fold into a delta, nothing about who acted or why. Weight is
// resolved by the caller (catalogapi.InteractionWeight) — this layer holds no opinion on what
// a "view" is worth, only on how to accumulate whatever it was handed.
type ListingInteractionEvent struct {
	ListingID int64
	Type      string
	Weight    float64
}
