package catalog_test

import (
	"context"
	"errors"
	"testing"

	catalogapi "shopnexus/internal/module/catalog/api"
	"shopnexus/internal/module/catalog/domain"
)

// The console is staff-only, and the check is the service's because the role is a row in the
// account module's table.
func TestAdminListListings_PlainUserRefused(t *testing.T) {
	h := newHarnessWith("user", true)
	err := mustErr(h.svc.AdminListListings(context.Background(), catalogapi.AdminListListingsRequest{
		ActorID: actor, Page: 1, Limit: 20,
	}))
	if got := status(t, err); got != 403 {
		t.Fatalf("status = %d, want 403", got)
	}
}

// The queue is two things at once: a listing awaiting its first publication, and a live one
// holding an edit.
func TestAdminListListings_BothHalvesOfTheQueue(t *testing.T) {
	seller := newHarnessWith("user", true)
	mod := newHarnessModerator(seller)
	ctx := context.Background()

	first := queue(t, seller, seedListing(t, seller))

	page, err := mod.svc.AdminListListings(ctx, catalogapi.AdminListListingsRequest{
		ActorID: actor, Page: 1, Limit: 20,
	})
	if err != nil {
		t.Fatalf("AdminListListings: %v", err)
	}
	if len(page.Data) != 1 || page.Data[0].ID != first.ID {
		t.Fatalf("queue = %+v, want the pending listing", page.Data)
	}
	// The card carries a price, which is the featured variant's rather than a stored column.
	if page.Data[0].Price == 0 {
		t.Error("the card has no price")
	}

	// Approve it, then edit it: it comes back into the queue holding an edit while staying
	// live.
	approve(t, mod, actor, first)
	renamed := "Renamed"
	if _, err := seller.svc.UpdateListing(ctx, catalogapi.UpdateListingRequest{
		ActorID: actor, ID: first.ID, Name: &renamed,
	}); err != nil {
		t.Fatalf("UpdateListing: %v", err)
	}
	page, err = mod.svc.AdminListListings(ctx, catalogapi.AdminListListingsRequest{
		ActorID: actor, Page: 1, Limit: 20,
	})
	if err != nil {
		t.Fatalf("AdminListListings: %v", err)
	}
	if len(page.Data) != 1 || page.Data[0].Status != string(domain.StatusActive) {
		t.Fatalf("queue = %+v, want the live listing holding an edit", page.Data)
	}
}

// Approving applies a held edit, and approving something with nothing pending is a conflict.
func TestAdminApproveListing(t *testing.T) {
	seller := newHarnessWith("user", true)
	mod := newHarnessModerator(seller)
	ctx := context.Background()
	listing := seedListing(t, seller)

	if s := status(t, mustErr(mod.svc.AdminApproveListing(ctx, catalogapi.ApproveListingRequest{
		ActorID: actor, ID: listing.ID, Version: listing.Version,
	}))); s != 409 {
		t.Fatalf("status = %d, want 409 for a draft", s)
	}

	got := approve(t, mod, actor, queue(t, seller, listing))
	if got.Status != string(domain.StatusActive) {
		t.Fatalf("status = %q, want active", got.Status)
	}

	renamed := "Renamed"
	if _, err := seller.svc.UpdateListing(ctx, catalogapi.UpdateListingRequest{
		ActorID: actor, ID: listing.ID, Name: &renamed,
	}); err != nil {
		t.Fatalf("UpdateListing: %v", err)
	}
	held, err := seller.svc.GetListing(ctx, catalogapi.GetListingRequest{ID: listing.ID, ViewerID: actor})
	if err != nil {
		t.Fatalf("GetListing: %v", err)
	}
	got = approve(t, mod, actor, held)
	if got.Name != renamed || got.PendingEdit != nil {
		t.Fatalf("listing = %+v, want the edit applied and cleared", got)
	}
}

// A takedown hides the listing with a reason, drops any held edit, and the seller cannot
// undo it: publishing again re-enters the queue.
// The verdict is on a version, so a listing the seller changed while it sat in the queue is
// refused rather than approved. Without this a seller could keep a queued listing clean while a
// moderator reads it and swap it for anything a moment before the decision landed — the edit is
// written straight through while it is queued, so nothing else stood in the way.
func TestAdminApproveListing_RefusesAVerdictOnAStaleRead(t *testing.T) {
	seller := newHarnessWith("user", true)
	mod := newHarnessModerator(seller)
	ctx := context.Background()
	read := queue(t, seller, seedListing(t, seller))

	swapped := "Something else entirely"
	if _, err := seller.svc.UpdateListing(ctx, catalogapi.UpdateListingRequest{
		ActorID: actor, ID: read.ID, Name: &swapped,
	}); err != nil {
		t.Fatalf("UpdateListing: %v", err)
	}

	_, err := mod.svc.AdminApproveListing(ctx, catalogapi.ApproveListingRequest{
		ActorID: actor, ID: read.ID, Version: read.Version,
	})
	if !errors.Is(err, domain.ErrVersionConflict) {
		t.Fatalf("AdminApproveListing = %v, want ErrVersionConflict", err)
	}
	after, err := seller.svc.GetListing(ctx, catalogapi.GetListingRequest{ID: read.ID, ViewerID: actor})
	if err != nil {
		t.Fatalf("GetListing: %v", err)
	}
	if after.Status != string(domain.StatusPending) {
		t.Fatalf("status = %q, want it still queued", after.Status)
	}
	// Reading it again is all the moderator has to do.
	approve(t, mod, actor, after)
}

func TestAdminTakedownListing(t *testing.T) {
	seller := newHarnessWith("user", true)
	mod := newHarnessModerator(seller)
	ctx := context.Background()
	listing := seedListing(t, seller)
	approve(t, mod, actor, queue(t, seller, listing))

	got, err := mod.svc.AdminTakedownListing(ctx, catalogapi.TakedownListingRequest{
		ActorID: actor, ID: listing.ID, Reason: "counterfeit",
	})
	if err != nil {
		t.Fatalf("AdminTakedownListing: %v", err)
	}
	if got.Status != string(domain.StatusHidden) {
		t.Fatalf("status = %q, want hidden", got.Status)
	}
	// The trail says who and why, at the payload's declared type.
	diff, ok := auditedDiff(mod.repo, domain.TakenDown)
	if !ok || diff.Reason != "counterfeit" {
		t.Fatalf("trail = %+v ok = %v", diff, ok)
	}

	republished, err := seller.svc.PublishListing(ctx, catalogapi.PublishListingRequest{
		ActorID: actor, ID: listing.ID,
	})
	if err != nil {
		t.Fatalf("PublishListing: %v", err)
	}
	if republished.Status != string(domain.StatusPending) {
		t.Fatalf("status = %q — a seller must not be able to undo a takedown", republished.Status)
	}
}

// A seller has to be able to tell "staff removed this" from "I hid this", and to read why. Both
// still write `hidden`, so the marker is what carries it — and publishing again clears both, or the
// seller would be told their live listing was removed.
func TestAdminTakedown_TellsTheSellerWhyAndOnlyWhileItIsDown(t *testing.T) {
	h := newHarnessWith("user", true)
	ctx := context.Background()
	l := seedListing(t, h)
	publish(t, h, l)
	staff := newHarnessModerator(h)

	down, err := staff.svc.AdminTakedownListing(ctx, catalogapi.TakedownListingRequest{
		ActorID: actor, ID: l.ID, Reason: "Ảnh trùng với gian hàng chính hãng",
	})
	if err != nil {
		t.Fatalf("AdminTakedownListing: %v", err)
	}
	if down.Status != string(domain.StatusHidden) || down.TakenDownAt == nil {
		t.Fatalf("listing = %+v, want it hidden and marked as taken down", down)
	}
	if down.TakedownReason == nil || *down.TakedownReason != "Ảnh trùng với gian hàng chính hãng" {
		t.Fatalf("reason = %v, want the moderator's words", down.TakedownReason)
	}
	// The seller's own read carries it too — that is the whole point.
	mine, err := h.svc.GetListing(ctx, catalogapi.GetListingRequest{ID: l.ID, ViewerID: actor})
	if err != nil {
		t.Fatalf("GetListing: %v", err)
	}
	if mine.TakenDownAt == nil || mine.TakedownReason == nil {
		t.Fatalf("listing = %+v, want the seller told why", mine)
	}
	// And their own list can badge it without opening every row.
	page, err := h.svc.ListListings(ctx, catalogapi.ListListingsRequest{
		ViewerID: actor, Mine: true, Page: 1, Limit: 20,
	})
	if err != nil {
		t.Fatalf("ListListings(mine): %v", err)
	}
	if len(page.Data) != 1 || page.Data[0].TakenDownAt == nil {
		t.Fatalf("card = %+v, want the takedown marked on the card", page.Data)
	}

	// Publishing again re-enters moderation, so the reason must not survive: it described a state
	// the listing is no longer in.
	if _, err := h.svc.PublishListing(ctx, catalogapi.PublishListingRequest{
		ActorID: actor, ID: l.ID,
	}); err != nil {
		t.Fatalf("PublishListing: %v", err)
	}
	again, err := h.svc.GetListing(ctx, catalogapi.GetListingRequest{ID: l.ID, ViewerID: actor})
	if err != nil {
		t.Fatalf("GetListing: %v", err)
	}
	if again.TakenDownAt != nil || again.TakedownReason != nil {
		t.Fatalf("listing = %+v, want the takedown cleared once it is back in moderation", again)
	}
}

// The moderator's `notify_seller: false` has always been a recorded choice; it now decides whether
// the seller reads the reason. The marker still lands, because "staff removed this" is not a secret.
func TestAdminTakedown_WithoutNotifyKeepsTheReasonInTheTrailOnly(t *testing.T) {
	h := newHarnessWith("user", true)
	ctx := context.Background()
	l := seedListing(t, h)
	publish(t, h, l)

	down, err := newHarnessModerator(h).svc.AdminTakedownListing(ctx, catalogapi.TakedownListingRequest{
		ActorID: actor, ID: l.ID, Reason: "nghi ngờ rửa tiền, đang điều tra",
		NotifySeller: new(false),
	})
	if err != nil {
		t.Fatalf("AdminTakedownListing: %v", err)
	}
	if down.TakenDownAt == nil {
		t.Fatalf("listing = %+v, want the takedown still marked", down)
	}
	if down.TakedownReason != nil {
		t.Fatalf("reason = %v, want it kept out of the seller's view", *down.TakedownReason)
	}
}
