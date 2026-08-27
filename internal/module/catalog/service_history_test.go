package catalog_test

import (
	"context"
	"testing"

	catalogapi "shopnexus/internal/module/catalog/api"
	"shopnexus/internal/module/catalog/domain"
	"shopnexus/internal/shared/id"
)

// stranger is an account that is neither the seller nor staff. The seeded listings all
// belong to `actor`.
const stranger = id.ID[id.Account](2)

func history(t *testing.T, h *harness, caller id.ID[id.Account], listingID id.ID[id.Listing]) []catalogapi.ListingHistoryEntry {
	t.Helper()
	page, err := h.svc.ListListingHistory(context.Background(), catalogapi.ListListingHistoryRequest{
		ActorID: caller, ID: listingID, Page: 1, Limit: 20,
	})
	if err != nil {
		t.Fatalf("ListListingHistory: %v", err)
	}
	return page.Data
}

func entryFor(entries []catalogapi.ListingHistoryEntry, code domain.EventCode) (catalogapi.ListingHistoryEntry, bool) {
	for _, e := range entries {
		if e.Code == string(code) {
			return e, true
		}
	}
	return catalogapi.ListingHistoryEntry{}, false
}

// The trail starts at the insert. Without that row a seller's history would begin at their
// first publication, with nothing saying when they wrote the listing.
func TestListListingHistory_StartsAtTheListingBeingPosted(t *testing.T) {
	h := newHarnessWith("user", true)
	listing := seedListing(t, h)

	entries := history(t, h, actor, listing.ID)
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want the creation alone", len(entries))
	}
	created := entries[0]
	if created.Code != string(domain.Created.Code) || created.Version != 1 {
		t.Fatalf("first entry = %+v, want listing.create at version 1", created)
	}
	if created.ChangeType != "insert" {
		t.Errorf("change_type = %q, want insert", created.ChangeType)
	}
	if created.ActorKind != "seller" || created.Actor == nil {
		t.Errorf("actor = %+v (%s), want the seller named", created.Actor, created.ActorKind)
	}
}

// Newest first, which is the order a history is read in rather than the order it was written.
func TestListListingHistory_NewestFirst(t *testing.T) {
	h := newHarnessWith("user", true)
	ctx := context.Background()
	listing := seedListing(t, h)
	if _, err := h.svc.PublishListing(ctx, catalogapi.PublishListingRequest{ActorID: actor, ID: listing.ID}); err != nil {
		t.Fatalf("PublishListing: %v", err)
	}

	entries := history(t, h, actor, listing.ID)
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want the creation and the publication", len(entries))
	}
	if entries[0].Code != string(domain.Published.Code) || entries[1].Code != string(domain.Created.Code) {
		t.Fatalf("order = %q, %q; want the publication first", entries[0].Code, entries[1].Code)
	}
	if entries[0].Version <= entries[1].Version {
		t.Errorf("versions = %d, %d; want descending", entries[0].Version, entries[1].Version)
	}
}

// An edit to a listing nobody is looking at is written straight through, so nothing else on
// that path would record it — and "what did I change" is the question the history is for.
func TestListListingHistory_ADraftEditNamesItsFields(t *testing.T) {
	h := newHarnessWith("user", true)
	ctx := context.Background()
	listing := seedListing(t, h)

	renamed := "A better name"
	if _, err := h.svc.UpdateListing(ctx, catalogapi.UpdateListingRequest{
		ActorID: actor, ID: listing.ID, Name: &renamed,
	}); err != nil {
		t.Fatalf("UpdateListing: %v", err)
	}

	edit, ok := entryFor(history(t, h, actor, listing.ID), domain.Edited.Code)
	if !ok {
		t.Fatal("the edit is not in the history")
	}
	if len(edit.Fields) != 1 || edit.Fields[0] != "name" {
		t.Errorf("fields = %v, want [name]", edit.Fields)
	}
}

// A price change is the edit a seller makes most, and it goes through the variant rather
// than through the listing's own fields.
func TestListListingHistory_AVariantEditNamesItsFields(t *testing.T) {
	h := newHarnessWith("user", true)
	ctx := context.Background()
	listing := seedListing(t, h)

	price := int64(250000)
	if _, err := h.svc.UpdateVariant(ctx, catalogapi.UpdateVariantRequest{
		ActorID: actor, ID: listing.Variants[0].ID, Price: &price,
	}); err != nil {
		t.Fatalf("UpdateVariant: %v", err)
	}

	edit, ok := entryFor(history(t, h, actor, listing.ID), domain.VariantEdited.Code)
	if !ok {
		t.Fatal("the variant edit is not in the history")
	}
	if len(edit.Fields) != 1 || edit.Fields[0] != "price" {
		t.Errorf("fields = %v, want [price]", edit.Fields)
	}
	// The variant is named the way every id is on the wire — opaque, never the database's
	// own int64.
	published, ok := edit.Details["variant_id"].(id.ID[id.Variant])
	if !ok {
		t.Fatalf("variant_id = %#v, want an opaque id", edit.Details["variant_id"])
	}
	if published != listing.Variants[0].ID {
		t.Errorf("variant_id = %s, want %s", published, listing.Variants[0].ID)
	}
}

// Somebody else's listing is not theirs to know about — the same answer the seller-scoped
// loads give, rather than a 403 that confirms the listing exists.
func TestListListingHistory_StrangerNotFound(t *testing.T) {
	h := newHarnessWith("user", true)
	listing := seedListing(t, h)

	_, err := h.svc.ListListingHistory(context.Background(), catalogapi.ListListingHistoryRequest{
		ActorID: stranger, ID: listing.ID, Page: 1, Limit: 20,
	})
	if got := status(t, err); got != 404 {
		t.Fatalf("status = %d, want 404", got)
	}
}

// One trail, two audiences. A moderator is `staff` to the seller and nothing more, and the
// words moderators write for each other stay theirs: a takedown reason the moderator chose
// not to send is exactly the sentence the listing's own `takedown_reason` withholds.
func TestListListingHistory_ModeratorsWordsStayWithStaff(t *testing.T) {
	seller := newHarnessWith("user", true)
	mod := newHarnessModerator(seller)
	ctx := context.Background()
	listing := seedListing(t, seller)

	queued := queue(t, seller, listing)
	if _, err := mod.svc.AdminApproveListing(ctx, catalogapi.ApproveListingRequest{
		ActorID: stranger, ID: listing.ID, Version: queued.Version, Note: "looks fine to me",
	}); err != nil {
		t.Fatalf("AdminApproveListing: %v", err)
	}
	quiet := false
	if _, err := mod.svc.AdminTakedownListing(ctx, catalogapi.TakedownListingRequest{
		ActorID: stranger, ID: listing.ID, Reason: "repeat offender, watch this seller", NotifySeller: &quiet,
	}); err != nil {
		t.Fatalf("AdminTakedownListing: %v", err)
	}

	sellerSees, ok := entryFor(history(t, seller, actor, listing.ID), domain.TakenDown.Code)
	if !ok {
		t.Fatal("the takedown is not in the seller's history")
	}
	if sellerSees.ActorKind != "staff" || sellerSees.Actor != nil {
		t.Errorf("actor = %+v (%s), want staff with no account named", sellerSees.Actor, sellerSees.ActorKind)
	}
	if _, leaked := sellerSees.Details["reason"]; leaked {
		t.Errorf("details = %v, want the withheld reason gone", sellerSees.Details)
	}
	if _, leaked := sellerSees.Details["notify_seller"]; leaked {
		t.Errorf("details = %v, want the moderator's own choice gone", sellerSees.Details)
	}
	approval, ok := entryFor(history(t, seller, actor, listing.ID), domain.Approved.Code)
	if !ok {
		t.Fatal("the approval is not in the seller's history")
	}
	if _, leaked := approval.Details["note"]; leaked {
		t.Errorf("details = %v, want the approval note gone", approval.Details)
	}

	staffSees, ok := entryFor(history(t, mod, stranger, listing.ID), domain.TakenDown.Code)
	if !ok {
		t.Fatal("the takedown is not in the moderator's history")
	}
	if staffSees.Actor == nil || staffSees.ActorKind != "staff" {
		t.Errorf("actor = %+v (%s), want the moderator named", staffSees.Actor, staffSees.ActorKind)
	}
	if staffSees.Details["reason"] != "repeat offender, watch this seller" {
		t.Errorf("details = %v, want the full reason", staffSees.Details)
	}
}

// A reason the moderator did choose to send is the seller's to read: it is the same sentence
// the listing itself now carries.
func TestListListingHistory_ANotifiedReasonReachesTheSeller(t *testing.T) {
	seller := newHarnessWith("user", true)
	mod := newHarnessModerator(seller)
	ctx := context.Background()
	listing := seedListing(t, seller)

	if _, err := seller.svc.PublishListing(ctx, catalogapi.PublishListingRequest{ActorID: actor, ID: listing.ID}); err != nil {
		t.Fatalf("PublishListing: %v", err)
	}
	told := true
	if _, err := mod.svc.AdminTakedownListing(ctx, catalogapi.TakedownListingRequest{
		ActorID: stranger, ID: listing.ID, Reason: "the photos are not of this item", NotifySeller: &told,
	}); err != nil {
		t.Fatalf("AdminTakedownListing: %v", err)
	}

	entry, ok := entryFor(history(t, seller, actor, listing.ID), domain.TakenDown.Code)
	if !ok {
		t.Fatal("the takedown is not in the seller's history")
	}
	if entry.Details["reason"] != "the photos are not of this item" {
		t.Errorf("details = %v, want the reason the moderator sent", entry.Details)
	}
}
