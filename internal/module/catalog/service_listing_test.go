package catalog_test

import (
	"context"
	"testing"

	catalogapi "shopnexus/internal/module/catalog/api"
	"shopnexus/internal/module/catalog/domain"
	"shopnexus/internal/shared/id"
)

func createListingRequest(h *harness, t *testing.T) catalogapi.CreateListingRequest {
	t.Helper()
	category, err := newHarnessAdmin(h).svc.AdminCreateCategory(context.Background(),
		catalogapi.CreateCategoryRequest{ActorID: actor, Name: "Tops"})
	if err != nil {
		t.Fatalf("AdminCreateCategory: %v", err)
	}
	return catalogapi.CreateListingRequest{
		ActorID:        actor,
		Name:           "Áo thun Uniqlo",
		Description:    "Còn mới",
		CategoryID:     category.ID,
		Condition:      "used",
		PriceMode:      "fixed",
		ShippingPaidBy: "buyer",
		Currency:       "VND",
		Variants: []catalogapi.CreateVariantInput{{
			Price: 299000, Attributes: map[string]any{"size": "l"},
			PackageDetails: map[string]any{"weight_g": 200}, Quantity: 5,
		}},
	}
}

// A seller who has not verified their identity cannot list: the flag is a row in the account
// module's table, so this module asks that service for it.
func TestCreateListing_NeedsAVerifiedIdentity(t *testing.T) {
	h := newHarnessWith("user", false)
	req := createListingRequest(h, t)
	if got := status(t, mustErr(h.svc.CreateListing(context.Background(), req))); got != 422 {
		t.Fatalf("status = %d, want 422", got)
	}
}

// Creation is atomic and lands in draft with its variants and their stock.
func TestCreateListing(t *testing.T) {
	h := newHarnessWith("user", true)
	ctx := context.Background()
	req := createListingRequest(h, t)

	got, err := h.svc.CreateListing(ctx, req)
	if err != nil {
		t.Fatalf("CreateListing: %v", err)
	}
	if got.Status != string(domain.StatusDraft) {
		t.Errorf("status = %q, want draft", got.Status)
	}
	if got.Slug == "" {
		t.Error("slug was not derived")
	}
	if len(got.Variants) != 1 || got.Variants[0].Stock.Available != 5 {
		t.Fatalf("variants = %+v", got.Variants)
	}
	if got.FeaturedVariantID == nil || *got.FeaturedVariantID != got.Variants[0].ID {
		t.Error("the only variant is not featured")
	}
	if got.Seller.ID != actor {
		t.Errorf("seller = %+v", got.Seller)
	}
}

// The same name twice derives the same slug, which is globally unique: a seller who typed
// the title twice is told rather than handed a suffixed one.
func TestCreateListing_DuplicateSlugConflicts(t *testing.T) {
	h := newHarnessWith("user", true)
	ctx := context.Background()
	req := createListingRequest(h, t)
	if _, err := h.svc.CreateListing(ctx, req); err != nil {
		t.Fatalf("first CreateListing: %v", err)
	}
	if got := status(t, mustErr(h.svc.CreateListing(ctx, req))); got != 409 {
		t.Fatalf("status = %d, want 409", got)
	}
}

// A tag the dictionary does not have is a 404: the join has a foreign key, and inventing the
// tag on the seller's behalf would let anyone write the dictionary.
func TestCreateListing_UnknownTagNotFound(t *testing.T) {
	h := newHarnessWith("user", true)
	req := createListingRequest(h, t)
	req.Tags = []string{"no-such-tag"}
	if got := status(t, mustErr(h.svc.CreateListing(context.Background(), req))); got != 404 {
		t.Fatalf("status = %d, want 404", got)
	}
}

// Eleven tags is refused by the domain, before any write.
func TestCreateListing_TooManyTags(t *testing.T) {
	h := newHarnessWith("user", true)
	req := createListingRequest(h, t)
	req.Tags = []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k"}
	if got := status(t, mustErr(h.svc.CreateListing(context.Background(), req))); got != 422 {
		t.Fatalf("status = %d, want 422", got)
	}
}

// A read by a stranger still works — a listing is public — and `favorited` is false for one
// who has not saved it.
func TestGetListing(t *testing.T) {
	h := newHarnessWith("user", true)
	ctx := context.Background()
	created, err := h.svc.CreateListing(ctx, createListingRequest(h, t))
	if err != nil {
		t.Fatalf("CreateListing: %v", err)
	}
	got, err := h.svc.GetListing(ctx, catalogapi.GetListingRequest{ID: created.ID})
	if err != nil {
		t.Fatalf("GetListing: %v", err)
	}
	if got.ID != created.ID || got.Favorited {
		t.Fatalf("listing = %+v", got)
	}
	if _, err := h.svc.GetListing(ctx, catalogapi.GetListingRequest{
		ID: id.Of[id.Listing](999),
	}); status(t, err) != 404 {
		t.Fatalf("a missing listing must be 404")
	}
}
