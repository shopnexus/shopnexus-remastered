package catalog_test

import (
	"context"
	"testing"

	catalogapi "shopnexus/internal/module/catalog/api"
	"shopnexus/internal/shared/id"
)

func seedListing(t *testing.T, h *harness) catalogapi.ListingDetail {
	t.Helper()
	created, err := h.svc.CreateListing(context.Background(), createListingRequest(h, t))
	if err != nil {
		t.Fatalf("CreateListing: %v", err)
	}
	return created
}

func TestCreateVariant(t *testing.T) {
	h := newHarnessWith("user", true)
	ctx := context.Background()
	listing := seedListing(t, h)

	got, err := h.svc.CreateVariant(ctx, catalogapi.CreateVariantRequest{
		ActorID: actor, ListingID: listing.ID,
		CreateVariantInput: catalogapi.CreateVariantInput{
			Price: 199000, Attributes: map[string]any{"size": "m"},
			PackageDetails: map[string]any{}, Quantity: 2,
		},
	})
	if err != nil {
		t.Fatalf("CreateVariant: %v", err)
	}
	if len(got.Variants) != 2 {
		t.Fatalf("variants = %d, want 2", len(got.Variants))
	}
	// The answer is the whole listing, so the client sees which variant is featured.
	if got.FeaturedVariantID == nil {
		t.Error("no featured variant in the response")
	}
}

// The same attributes twice is a conflict — two live variants a buyer cannot tell apart.
func TestCreateVariant_DuplicateAttributes(t *testing.T) {
	h := newHarnessWith("user", true)
	ctx := context.Background()
	listing := seedListing(t, h)
	req := catalogapi.CreateVariantRequest{
		ActorID: actor, ListingID: listing.ID,
		CreateVariantInput: catalogapi.CreateVariantInput{
			Price: 199000, Attributes: map[string]any{"size": "l"},
			PackageDetails: map[string]any{}, Quantity: 2,
		},
	}
	if got := status(t, mustErr(h.svc.CreateVariant(ctx, req))); got != 409 {
		t.Fatalf("status = %d, want 409", got)
	}
}

// Someone else's variant is not found rather than forbidden: it is not theirs to know about.
func TestUpdateVariant_StrangerNotFound(t *testing.T) {
	h := newHarnessWith("user", true)
	ctx := context.Background()
	listing := seedListing(t, h)
	price := int64(1)
	err := mustErr(h.svc.UpdateVariant(ctx, catalogapi.UpdateVariantRequest{
		ActorID: id.Of[id.Account](999), ID: listing.Variants[0].ID, Price: &price,
	}))
	if got := status(t, err); got != 404 {
		t.Fatalf("status = %d, want 404", got)
	}
}

// quantity sets the total. Below what is already committed it is refused, because an oversold
// row must not be representable.
func TestUpdateVariant_QuantityBelowCommitted(t *testing.T) {
	h := newHarnessWith("user", true)
	ctx := context.Background()
	listing := seedListing(t, h)
	variantID := listing.Variants[0].ID.Int64()
	if err := h.repo.ReserveStock(ctx, variantID, 3); err != nil {
		t.Fatalf("ReserveStock: %v", err)
	}

	quantity := int64(2)
	err := mustErr(h.svc.UpdateVariant(ctx, catalogapi.UpdateVariantRequest{
		ActorID: actor, ID: listing.Variants[0].ID, Quantity: &quantity,
	}))
	if got := status(t, err); got != 422 {
		t.Fatalf("status = %d, want 422", got)
	}

	// Above it is fine, and the reservation is untouched.
	quantity = 9
	got, err := h.svc.UpdateVariant(ctx, catalogapi.UpdateVariantRequest{
		ActorID: actor, ID: listing.Variants[0].ID, Quantity: &quantity,
	})
	if err != nil {
		t.Fatalf("UpdateVariant: %v", err)
	}
	if got.Variants[0].Stock.Quantity != 9 || got.Variants[0].Stock.Reserved != 3 {
		t.Fatalf("stock = %+v", got.Variants[0].Stock)
	}
}

// is_featured moves the flag inside the listing's own set.
func TestUpdateVariant_MovesTheFeaturedFlag(t *testing.T) {
	h := newHarnessWith("user", true)
	ctx := context.Background()
	listing := seedListing(t, h)
	added, err := h.svc.CreateVariant(ctx, catalogapi.CreateVariantRequest{
		ActorID: actor, ListingID: listing.ID,
		CreateVariantInput: catalogapi.CreateVariantInput{
			Price: 199000, Attributes: map[string]any{"size": "m"},
			PackageDetails: map[string]any{}, Quantity: 2,
		},
	})
	if err != nil {
		t.Fatalf("CreateVariant: %v", err)
	}
	second := added.Variants[1].ID
	featured := true
	got, err := h.svc.UpdateVariant(ctx, catalogapi.UpdateVariantRequest{
		ActorID: actor, ID: second, IsFeatured: &featured,
	})
	if err != nil {
		t.Fatalf("UpdateVariant: %v", err)
	}
	if got.FeaturedVariantID == nil || *got.FeaturedVariantID != second {
		t.Fatalf("featured = %v, want %v", got.FeaturedVariantID, second)
	}
}

// The last live variant of a listing that is live or queued cannot go: there would be
// nothing to buy.
func TestDeleteVariant_LastOneOfALiveListing(t *testing.T) {
	h := newHarnessWith("user", true)
	ctx := context.Background()
	listing := seedListing(t, h)
	if _, err := h.svc.PublishListing(ctx, catalogapi.PublishListingRequest{
		ActorID: actor, ID: listing.ID,
	}); err != nil {
		t.Fatalf("PublishListing: %v", err)
	}
	err := mustErr(h.svc.DeleteVariant(ctx, catalogapi.DeleteVariantRequest{
		ActorID: actor, ID: listing.Variants[0].ID,
	}))
	if got := status(t, err); got != 409 {
		t.Fatalf("status = %d, want 409", got)
	}
}
