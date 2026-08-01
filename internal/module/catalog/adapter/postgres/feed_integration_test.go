//go:build integration

package postgres_test

import (
	"context"
	"testing"

	"shopnexus/internal/module/catalog/domain"
	"shopnexus/internal/module/catalog/port"
)

// The feed's statement is one query with every filter in it, so what a test has to prove is
// that Postgres accepts each combination and that the visibility rules hold. The fake cannot
// answer either.
func TestRepo_ListListingsAppliesEveryFilter(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	category := createCategory(t, repo, unique("cat-"), nil)
	createTag(t, repo, "handmade", nil)

	draft := newListingFor(t, repo, category.ID, unique("Draft "))
	// A real-looking name, because the trigram threshold is a similarity over the whole string:
	// a four-letter query against a timestamp scores below it, which is correct behaviour.
	live := newListingFor(t, repo, category.ID, unique("Ao thun Uniqlo "))
	if err := live.Publish(); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if err := live.Approve(""); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if err := repo.SaveListing(ctx, live, testSeller); err != nil {
		t.Fatalf("SaveListing: %v", err)
	}

	// Every filter at once, so each branch of the statement is exercised by Postgres.
	full := port.ListingFilter{
		ViewerID:   testSeller,
		CategoryID: category.ID,
		SellerID:   testSeller,
		Condition:  domain.ConditionUsed,
		Tag:        "handmade",
		MinPrice:   1,
		MaxPrice:   new(int64(1_000_000)),
		Query:      "ao thun uniqlo",
		Sort:       port.SortRelevance,
		Limit:      20,
	}
	rows, _, err := repo.ListListings(ctx, full)
	if err != nil {
		t.Fatalf("ListListings(every filter): %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("the live listing matched nothing")
	}
	if rows[0].Score == nil {
		t.Error("a search answered no score")
	}
	if rows[0].Price == 0 {
		t.Error("the card has no price — the lateral join found no variant")
	}

	// Every sort has to be a statement Postgres accepts.
	for _, order := range []string{
		port.SortNewest, port.SortRating, port.SortPriceAsc, port.SortPriceDesc,
		port.SortBestSelling, port.SortRelevance, port.SortRecommended,
	} {
		f := port.ListingFilter{ViewerID: testSeller, SellerID: testSeller, Sort: order, Limit: 5}
		if order == port.SortRelevance {
			f.Query = "ao thun uniqlo"
		}
		if _, _, err := repo.ListListings(ctx, f); err != nil {
			t.Errorf("sort %q: %v", order, err)
		}
	}

	// A draft stays out of the public feed and comes back for its own seller.
	public, _, err := repo.ListListings(ctx, port.ListingFilter{SellerID: testSeller, Limit: 50})
	if err != nil {
		t.Fatalf("ListListings(public): %v", err)
	}
	for _, row := range public {
		if row.ID == draft.ID {
			t.Fatal("a draft is in the public feed")
		}
	}
	mine, _, err := repo.ListListings(ctx, port.ListingFilter{
		ViewerID: testSeller, Mine: true, Status: domain.StatusDraft, Limit: 50,
	})
	if err != nil {
		t.Fatalf("ListListings(mine): %v", err)
	}
	var found bool
	for _, row := range mine {
		if row.ID == draft.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("the seller cannot see their own draft")
	}
}

// `ids` ignores the feed's own visibility rules for both ways a listing leaves it: hidden
// and soft-deleted. An order that references a listing has to render it either way, so
// neither `l.status = 'active'` nor `l.deleted_at IS NULL` may reach the `ids` branch.
func TestRepo_ListListingsIDsResolveEvenWhenDeleted(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	category := createCategory(t, repo, unique("cat-"), nil)
	createTag(t, repo, "handmade", nil)
	l := newListingFor(t, repo, category.ID, unique("Gone "))
	if err := l.Publish(); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if err := l.Approve(""); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if err := repo.SaveListing(ctx, l, testSeller); err != nil {
		t.Fatalf("SaveListing: %v", err)
	}
	if err := repo.SoftDeleteListing(ctx, l.ID, testSeller, testSeller); err != nil {
		t.Fatalf("SoftDeleteListing: %v", err)
	}

	// Gone from the public feed.
	public, _, err := repo.ListListings(ctx, port.ListingFilter{Limit: 50})
	if err != nil {
		t.Fatalf("ListListings(public): %v", err)
	}
	for _, row := range public {
		if row.ID == l.ID {
			t.Fatal("a deleted listing is in the public feed")
		}
	}
	// Still resolvable by id.
	byID, _, err := repo.ListListings(ctx, port.ListingFilter{IDs: []int64{l.ID}, Limit: 5})
	if err != nil {
		t.Fatalf("ListListings(ids): %v", err)
	}
	if len(byID) != 1 || byID[0].ID != l.ID || byID[0].DeletedAt == nil {
		t.Fatalf("ids = %+v, want the deleted listing with deleted_at set", byID)
	}
}

// A recommended feed's probe comes from the account's interest vectors, which have
// nothing to do with `q`: `sort=recommended` together with a query must still filter
// lexically. Only a probe that is the query's own embedding (ProbeFromQuery) may skip
// the lexical predicate.
func TestRepo_ListListingsRecommendedProbeDoesNotBypassQueryFilter(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	category := createCategory(t, repo, unique("cat-"), nil)
	createTag(t, repo, "handmade", nil)

	match := newListingFor(t, repo, category.ID, unique("Ao thun Uniqlo "))
	if err := match.Publish(); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if err := match.Approve(""); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if err := repo.SaveListing(ctx, match, testSeller); err != nil {
		t.Fatalf("SaveListing: %v", err)
	}

	other := newListingFor(t, repo, category.ID, unique("Random accessory "))
	if err := other.Publish(); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if err := other.Approve(""); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if err := repo.SaveListing(ctx, other, testSeller); err != nil {
		t.Fatalf("SaveListing: %v", err)
	}

	probe := make(port.Vector, 1024)
	probe[0] = 1
	// The full phrase, as the trigram index needs: a short single-word query against a
	// name padded with a unique timestamp suffix can fall under the similarity threshold.
	const query = "ao thun uniqlo"
	rows, _, err := repo.ListListings(ctx, port.ListingFilter{
		Query: query, Probe: probe, ProbeFromQuery: false, Sort: port.SortRecommended, Limit: 20,
	})
	if err != nil {
		t.Fatalf("ListListings: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != match.ID {
		t.Fatalf("rows = %+v, want only the listing matching %q", rows, query)
	}
}

// A semantic probe has to be a legal statement even though nothing embeds a query yet, and the
// wishlist filter has to work off the favorite table.
func TestRepo_ListListingsProbeAndWishlist(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	category := createCategory(t, repo, unique("cat-"), nil)
	createTag(t, repo, "handmade", nil)
	l := newListingFor(t, repo, category.ID, unique("Probe "))
	if err := l.Publish(); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if err := l.Approve(""); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if err := repo.SaveListing(ctx, l, testSeller); err != nil {
		t.Fatalf("SaveListing: %v", err)
	}

	probe := make(port.Vector, 1024)
	probe[0] = 1
	if _, _, err := repo.ListListings(ctx, port.ListingFilter{
		Query: "probe", Mode: port.ModeHybrid, Probe: probe, ProbeFromQuery: true,
		Sort: port.SortRelevance, Limit: 5,
	}); err != nil {
		t.Fatalf("ListListings(hybrid): %v", err)
	}

	const buyer = int64(4242)
	if err := repo.AddFavorite(ctx, buyer, l.ID); err != nil {
		t.Fatalf("AddFavorite: %v", err)
	}
	// Twice is once, which is what makes the route idempotent.
	if err := repo.AddFavorite(ctx, buyer, l.ID); err != nil {
		t.Fatalf("AddFavorite twice: %v", err)
	}
	saved, _, err := repo.ListListings(ctx, port.ListingFilter{
		ViewerID: buyer, Favorited: true, Limit: 20,
	})
	if err != nil {
		t.Fatalf("ListListings(favorited): %v", err)
	}
	if len(saved) != 1 || saved[0].ID != l.ID {
		t.Fatalf("wishlist = %+v, want the saved listing", saved)
	}
	among, err := repo.FavoritedAmong(ctx, buyer, []int64{l.ID})
	if err != nil || !among[l.ID] {
		t.Fatalf("FavoritedAmong = %v, %v; want it saved", among, err)
	}

	if err := repo.RemoveFavorite(ctx, buyer, l.ID); err != nil {
		t.Fatalf("RemoveFavorite: %v", err)
	}
	// Removing what is gone is not an error: a wishlist has to stay cleanable.
	if err := repo.RemoveFavorite(ctx, buyer, l.ID); err != nil {
		t.Fatalf("RemoveFavorite twice: %v", err)
	}
	if err := repo.AddFavorite(ctx, buyer, 0); err == nil {
		t.Error("saving a listing that does not exist was accepted")
	}
}
