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

// Publication always enters the queue. A listing does not go live because a scan liked it.
func TestPublishListing_EntersModeration(t *testing.T) {
	h := newHarnessWith("user", true)
	ctx := context.Background()
	listing := seedListing(t, h)

	got, err := h.svc.PublishListing(ctx, catalogapi.PublishListingRequest{ActorID: actor, ID: listing.ID})
	if err != nil {
		t.Fatalf("PublishListing: %v", err)
	}
	if got.Status != string(domain.StatusPending) {
		t.Fatalf("status = %q, want pending", got.Status)
	}
	// Again is a conflict: there is already something in the queue.
	if s := status(t, mustErr(h.svc.PublishListing(ctx, catalogapi.PublishListingRequest{
		ActorID: actor, ID: listing.ID,
	}))); s != 409 {
		t.Fatalf("status = %d, want 409", s)
	}
}

// A draft is edited in place: there is nothing published to protect.
func TestUpdateListing_DraftWritesThrough(t *testing.T) {
	h := newHarnessWith("user", true)
	ctx := context.Background()
	listing := seedListing(t, h)

	renamed := "Áo thun Uniqlo cổ tròn"
	got, err := h.svc.UpdateListing(ctx, catalogapi.UpdateListingRequest{
		ActorID: actor, ID: listing.ID, Name: &renamed,
	})
	if err != nil {
		t.Fatalf("UpdateListing: %v", err)
	}
	if got.Name != renamed || got.PendingEdit != nil {
		t.Fatalf("draft edit = %+v, want it written through", got)
	}
}

// A delete is refused while a checkout is in flight — the reservation is the signal, and it
// is local, so there is no call into order and no second notion of "open".
func TestDeleteListing_RefusedWhileReserved(t *testing.T) {
	h := newHarnessWith("user", true)
	ctx := context.Background()
	listing := seedListing(t, h)
	if err := h.repo.ReserveStock(ctx, listing.Variants[0].ID.Int64(), 1); err != nil {
		t.Fatalf("ReserveStock: %v", err)
	}
	if s := status(t, h.svc.DeleteListing(ctx, catalogapi.DeleteListingRequest{
		ActorID: actor, ID: listing.ID,
	})); s != 409 {
		t.Fatalf("status = %d, want 409", s)
	}

	// Released, it goes. A completed sale would not have blocked it: soft delete is what
	// keeps that order renderable.
	if err := h.repo.ReleaseStock(ctx, listing.Variants[0].ID.Int64(), 1); err != nil {
		t.Fatalf("ReleaseStock: %v", err)
	}
	if err := h.svc.DeleteListing(ctx, catalogapi.DeleteListingRequest{
		ActorID: actor, ID: listing.ID,
	}); err != nil {
		t.Fatalf("DeleteListing: %v", err)
	}
	if _, err := h.svc.GetListing(ctx, catalogapi.GetListingRequest{ID: listing.ID}); status(t, err) != 404 {
		t.Fatal("a deleted listing must read as 404")
	}
}

// The seller taking their own listing down reads the same in the row as a takedown; who did
// it is in the trail. Safe only because publishing from hidden re-enters moderation.
func TestHideListing_NeedsToBeLive(t *testing.T) {
	h := newHarnessWith("user", true)
	ctx := context.Background()
	listing := seedListing(t, h)
	// A draft has nothing to hide.
	if s := status(t, mustErr(h.svc.HideListing(ctx, catalogapi.HideListingRequest{
		ActorID: actor, ID: listing.ID,
	}))); s != 409 {
		t.Fatalf("status = %d, want 409", s)
	}
}

// Editing a live listing parks the change so buyers keep seeing what was approved, and the
// listing stays live while it waits.
func TestUpdateListing_LiveEditIsHeld(t *testing.T) {
	h := newHarnessWith("user", true)
	mod := newHarnessModerator(h)
	ctx := context.Background()
	listing := seedListing(t, h)

	if _, err := h.svc.PublishListing(ctx, catalogapi.PublishListingRequest{ActorID: actor, ID: listing.ID}); err != nil {
		t.Fatalf("PublishListing: %v", err)
	}
	if _, err := mod.svc.AdminApproveListing(ctx, catalogapi.ApproveListingRequest{
		ActorID: actor, ID: listing.ID,
	}); err != nil {
		t.Fatalf("AdminApproveListing: %v", err)
	}

	renamed := "Áo thun Uniqlo cổ tròn xanh navy"
	got, err := h.svc.UpdateListing(ctx, catalogapi.UpdateListingRequest{
		ActorID: actor, ID: listing.ID, Name: &renamed,
	})
	if err != nil {
		t.Fatalf("UpdateListing: %v", err)
	}
	if got.Status != string(domain.StatusActive) {
		t.Errorf("status = %q, want it still active", got.Status)
	}
	if got.Name == renamed {
		t.Error("the live row was rewritten; the edit must be held")
	}
	if got.PendingEdit == nil || got.PendingEdit.Name == nil {
		t.Fatal("the edit was not held")
	}
}
