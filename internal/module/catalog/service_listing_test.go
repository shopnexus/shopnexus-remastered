package catalog_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	catalogapi "shopnexus/internal/module/catalog/api"
	"shopnexus/internal/module/catalog/domain"
	"shopnexus/internal/module/common"
	"shopnexus/internal/shared/id"
)

// seeded counts the categories one test has made, because the name is unique and a test that
// seeds two listings would otherwise collide with itself.
var seeded int

func createListingRequest(h *harness, t *testing.T) catalogapi.CreateListingRequest {
	t.Helper()
	seeded++
	category, err := newHarnessAdmin(h).svc.AdminCreateCategory(context.Background(),
		catalogapi.CreateCategoryRequest{ActorID: actor, Name: fmt.Sprintf("Tops %d", seeded)})
	if err != nil {
		t.Fatalf("AdminCreateCategory: %v", err)
	}
	return catalogapi.CreateListingRequest{
		ActorID:     actor,
		Name:        "Áo thun Uniqlo",
		Description: "Còn mới",
		CategoryID:  category.ID,
		Condition:   "used",
		PriceMode:   "fixed",
		Currency:    "VND",
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

// An approved listing is public, and `favorited` is false for a viewer who has not saved it.
// A draft is not public — that is TestGetListing_DraftIsTheSellersOwn.
func TestGetListing(t *testing.T) {
	h := newHarnessWith("user", true)
	mod := newHarnessModerator(h)
	ctx := context.Background()
	created, err := h.svc.CreateListing(ctx, createListingRequest(h, t))
	if err != nil {
		t.Fatalf("CreateListing: %v", err)
	}
	if _, err := h.svc.PublishListing(ctx, catalogapi.PublishListingRequest{ActorID: actor, ID: created.ID}); err != nil {
		t.Fatalf("PublishListing: %v", err)
	}
	if _, err := mod.svc.AdminApproveListing(ctx, catalogapi.ApproveListingRequest{
		ActorID: actor, ID: created.ID,
	}); err != nil {
		t.Fatalf("AdminApproveListing: %v", err)
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

// A listing that was never public is the seller's own: a stranger gets a 404 rather than a
// competitor's unpublished draft. Hidden and soft-deleted stay readable, because a cart that
// names one still has to render.
func TestGetListing_DraftIsTheSellersOwn(t *testing.T) {
	h := newHarnessWith("user", true)
	mod := newHarnessModerator(h)
	ctx := context.Background()
	listing := seedListing(t, h)
	stranger := id.Of[id.Account](999)

	for _, name := range []string{"draft", "pending"} {
		if name == "pending" {
			if _, err := h.svc.PublishListing(ctx, catalogapi.PublishListingRequest{
				ActorID: actor, ID: listing.ID,
			}); err != nil {
				t.Fatalf("PublishListing: %v", err)
			}
		}
		if s := status(t, mustErr(h.svc.GetListing(ctx, catalogapi.GetListingRequest{
			ID: listing.ID, ViewerID: stranger,
		}))); s != 404 {
			t.Errorf("%s read by a stranger = %d, want 404", name, s)
		}
		// Anonymous is the same answer.
		if s := status(t, mustErr(h.svc.GetListing(ctx, catalogapi.GetListingRequest{
			ID: listing.ID,
		}))); s != 404 {
			t.Errorf("%s read anonymously = %d, want 404", name, s)
		}
		// The owner and a moderator both see it.
		if _, err := h.svc.GetListing(ctx, catalogapi.GetListingRequest{
			ID: listing.ID, ViewerID: actor,
		}); err != nil {
			t.Errorf("%s read by its seller: %v", name, err)
		}
		if _, err := mod.svc.GetListing(ctx, catalogapi.GetListingRequest{
			ID: listing.ID, ViewerID: stranger,
		}); err != nil {
			t.Errorf("%s read by a moderator: %v", name, err)
		}
	}
}

// A held edit is the owner's and staff's to see; a buyer gets the approved version until it is
// applied, so the field is absent rather than leaking the wording under review.
func TestGetListing_PendingEditIsNotPublic(t *testing.T) {
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
	renamed := "Renamed"
	if _, err := h.svc.UpdateListing(ctx, catalogapi.UpdateListingRequest{
		ActorID: actor, ID: listing.ID, Name: &renamed,
	}); err != nil {
		t.Fatalf("UpdateListing: %v", err)
	}

	owner, err := h.svc.GetListing(ctx, catalogapi.GetListingRequest{ID: listing.ID, ViewerID: actor})
	if err != nil {
		t.Fatalf("GetListing: %v", err)
	}
	if owner.PendingEdit == nil {
		t.Error("the owner cannot see their own held edit")
	}
	buyer, err := h.svc.GetListing(ctx, catalogapi.GetListingRequest{
		ID: listing.ID, ViewerID: id.Of[id.Account](999),
	})
	if err != nil {
		t.Fatalf("GetListing: %v", err)
	}
	if buyer.PendingEdit != nil {
		t.Errorf("a buyer sees the edit under review: %+v", buyer.PendingEdit)
	}
}

// An image id that names no confirmed resource is refused: a row pointing at nothing is a
// picture that never renders, and the seller should hear it now rather than from a buyer.
func TestCreateListing_UnknownAttachmentNotFound(t *testing.T) {
	h := newHarnessWith("user", true)
	req := createListingRequest(h, t)
	req.Attachments = []id.ID[id.Resource]{id.Of[id.Resource](42)}
	if got := status(t, mustErr(h.svc.CreateListing(context.Background(), req))); got != 404 {
		t.Fatalf("status = %d, want 404", got)
	}

	// Declared as confirmed, it goes through — and the gallery keeps the order it was sent in,
	// because the first image is the cover.
	h.images[42], h.images[7] = true, true
	req.Attachments = []id.ID[id.Resource]{id.Of[id.Resource](42), id.Of[id.Resource](7)}
	got, err := h.svc.CreateListing(context.Background(), req)
	if err != nil {
		t.Fatalf("CreateListing: %v", err)
	}
	if len(got.Images) != 2 || got.Images[0].ID != id.Of[id.Resource](42) {
		t.Fatalf("images = %+v, want the order they were sent in", got.Images)
	}
}

// A held edit's attachments are checked when it is parked and again when it is approved: the
// row still carries the old ids while the edit waits, so validating the row alone would let an
// approval write ids that name nothing.
func TestUpdateListing_HeldEditAttachmentsAreChecked(t *testing.T) {
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

	unknown := []id.ID[id.Resource]{id.Of[id.Resource](99)}
	if got := status(t, mustErr(h.svc.UpdateListing(ctx, catalogapi.UpdateListingRequest{
		ActorID: actor, ID: listing.ID, Attachments: unknown,
	}))); got != 404 {
		t.Fatalf("status = %d, want 404 for an edit naming an unknown image", got)
	}
}

// The two-step upload, and the reason it is two steps: a slot on its own attaches to nothing, so
// a listing cannot end up rendering a photo whose bytes never arrived.
func TestUpload_ConfirmedBeforeItCanBeAttached(t *testing.T) {
	h := newHarnessWith("user", true)
	ctx := context.Background()

	slot, err := h.svc.CreateUpload(ctx, common.CreateUploadRequest{
		ActorID: actor, Filename: "front.jpg", Mime: "image/jpeg", Size: 2048,
	})
	if err != nil {
		t.Fatalf("CreateUpload: %v", err)
	}
	if slot.URL == "" || slot.ResourceID == 0 || !slot.ExpiresAt.After(time.Now()) {
		t.Fatalf("slot = %+v, want somewhere to PUT and a future expiry", slot)
	}

	// Unconfirmed, so it names no usable upload: attaching it is refused exactly as a made-up
	// id would be.
	unconfirmed := createListingRequest(h, t)
	unconfirmed.Attachments = []id.ID[id.Resource]{slot.ResourceID}
	if got := status(t, mustErr(h.svc.CreateListing(ctx, unconfirmed))); got != 404 {
		t.Fatalf("status = %d, want 404 attaching an unconfirmed upload", got)
	}
	// And confirming before the bytes are there is refused too, rather than producing a row
	// that renders as a broken image.
	if err := mustErr(h.svc.ConfirmUpload(ctx, common.ConfirmUploadRequest{
		ActorID: actor, ID: slot.ResourceID,
	})); err == nil {
		t.Fatal("an upload was confirmed before anything was uploaded")
	}

	// The client PUTs, then confirms.
	h.uploads.arrived[slot.ResourceID.Int64()] = true
	res, err := h.svc.ConfirmUpload(ctx, common.ConfirmUploadRequest{
		ActorID: actor, ID: slot.ResourceID,
	})
	if err != nil {
		t.Fatalf("ConfirmUpload: %v", err)
	}
	if res.ID != slot.ResourceID {
		t.Fatalf("confirmed = %+v, want the slot's own resource", res)
	}

	// Now it attaches, and the listing renders it with a link rather than a bare id.
	attached := createListingRequest(h, t)
	attached.Attachments = []id.ID[id.Resource]{slot.ResourceID}
	listing, err := h.svc.CreateListing(ctx, attached)
	if err != nil {
		t.Fatalf("CreateListing: %v", err)
	}
	if len(listing.Images) != 1 || listing.Images[0].URL == "" {
		t.Fatalf("images = %+v, want one with a signed link on it", listing.Images)
	}

	// Somebody else's slot is not theirs to confirm: a resource id is guessable.
	other, err := h.svc.CreateUpload(ctx, common.CreateUploadRequest{
		ActorID: actor, Filename: "back.jpg", Mime: "image/jpeg", Size: 1024,
	})
	if err != nil {
		t.Fatalf("CreateUpload: %v", err)
	}
	h.uploads.arrived[other.ResourceID.Int64()] = true
	if err := mustErr(h.svc.ConfirmUpload(ctx, common.ConfirmUploadRequest{
		ActorID: id.Of[id.Account](4242), ID: other.ResourceID,
	})); err == nil {
		t.Fatal("a stranger confirmed somebody else's upload")
	}
}
