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
	// Scoped to this test's own category. The dev database is shared and seeded, and once the
	// embedder has run over that seed every listing has a vector — so an unscoped assertion of
	// "only mine came back" tests the neighbours rather than the probe.
	rows, _, err := repo.ListListings(ctx, port.ListingFilter{
		Query: query, Probe: probe, ProbeFromQuery: false, Sort: port.SortRecommended,
		CategoryID: category.ID, Limit: 20,
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

// Where the goods are, in the one query the feed already is: the administrative filter is a plain
// column match, but "near me" is PostGIS — ST_DWithin against a geography, ordered by a distance
// the same statement computes. A fake can imitate the arithmetic; only Postgres can say whether the
// SQL, the index and the geography cast are right.
func TestRepo_ListListingsFiltersByAreaAndDistance(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	category := createCategory(t, repo, unique("cat-"), nil)
	createTag(t, repo, "handmade", nil)

	live := newListingFor(t, repo, category.ID, unique("Ao khoac "))
	if err := live.Publish(); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if err := live.Approve(""); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if err := repo.SaveListing(ctx, live, testSeller); err != nil {
		t.Fatalf("SaveListing: %v", err)
	}
	area := testLocation()

	// The location survived the write, coordinates and all — it is read back through ST_Y/ST_X.
	stored, err := repo.GetListing(ctx, live.ID)
	if err != nil {
		t.Fatalf("GetListing: %v", err)
	}
	if stored.Location == nil || !stored.Location.Geocoded() {
		t.Fatalf("location = %+v, want it round-tripped with its point", stored.Location)
	}
	if stored.Location.WardName != area.WardName {
		t.Fatalf("ward = %q, want %q", stored.Location.WardName, area.WardName)
	}

	base := port.ListingFilter{ViewerID: testSeller, SellerID: testSeller, Limit: 20}
	held := func(t *testing.T, f port.ListingFilter) bool {
		t.Helper()
		rows, _, err := repo.ListListings(ctx, f)
		if err != nil {
			t.Fatalf("ListListings: %v", err)
		}
		for _, row := range rows {
			if row.ID == live.ID {
				return true
			}
		}
		return false
	}

	// Its own ward, and another province.
	byWard := base
	byWard.ProvinceCode, byWard.DistrictCode, byWard.WardCode = area.ProvinceCode, *area.DistrictCode, area.WardCode
	if !held(t, byWard) {
		t.Error("the listing is missing from its own ward")
	}
	elsewhere := base
	elsewhere.ProvinceCode = "01"
	if held(t, elsewhere) {
		t.Error("the listing showed up in another province")
	}

	// Near me: a couple of streets away, then ~50 km out.
	near := base
	near.Near = &port.Point{Latitude: *area.Latitude + 0.01, Longitude: *area.Longitude + 0.01}
	near.RadiusKM = 5
	near.Sort = port.SortDistance
	rows, _, err := repo.ListListings(ctx, near)
	if err != nil {
		t.Fatalf("ListListings near: %v", err)
	}
	var found *port.ListingSummary
	for i := range rows {
		if rows[i].ID == live.ID {
			found = &rows[i]
		}
	}
	if found == nil {
		t.Fatal("the listing is missing from a 5 km radius around it")
	}
	// ST_Distance answers metres on the spheroid; 0.01° is about 1.5 km here.
	if found.DistanceKM == nil || *found.DistanceKM <= 0 || *found.DistanceKM > 5 {
		t.Fatalf("distance = %v, want a couple of km", found.DistanceKM)
	}
	far := near
	far.Near = &port.Point{Latitude: *area.Latitude + 0.5, Longitude: *area.Longitude + 0.5}
	if held(t, far) {
		t.Error("the listing showed up within 5 km of somewhere 50 km away")
	}

	// A position with no radius ranks every distance instead of bounding it, which is what the
	// "nearest first" browse does.
	ranked := base
	ranked.Near = near.Near
	ranked.Sort = port.SortDistance
	if !held(t, ranked) {
		t.Error("a distance sort with no radius dropped a listing")
	}
}

// The card carries the listing's own tags and the takedown marker, both read in the one feed
// statement — a page of twenty must not cost a query per row to draw its chips, and a seller has to
// be able to tell a takedown from their own hiding without opening every listing.
func TestRepo_ListListingsCarriesTagsAndTheTakedownMarker(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	category := createCategory(t, repo, unique("cat-"), nil)
	createTag(t, repo, "handmade", nil)

	live := newListingFor(t, repo, category.ID, unique("Ao thun "))
	if err := live.Publish(); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if err := live.Approve(""); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if err := repo.SaveListing(ctx, live, testSeller); err != nil {
		t.Fatalf("SaveListing: %v", err)
	}

	byID := func(id int64) port.ListingSummary {
		t.Helper()
		rows, _, err := repo.ListListings(ctx, port.ListingFilter{
			IDs: []int64{id}, ViewerID: testSeller, Limit: 20,
		})
		if err != nil {
			t.Fatalf("ListListings: %v", err)
		}
		if len(rows) != 1 {
			t.Fatalf("rows = %d, want the one listing", len(rows))
		}
		return rows[0]
	}

	card := byID(live.ID)
	if len(card.Tags) != 1 || card.Tags[0] != "handmade" {
		t.Fatalf("tags = %v, want the listing's own", card.Tags)
	}
	if card.TakenDownAt != nil {
		t.Fatalf("taken down = %v, want a live listing unmarked", card.TakenDownAt)
	}

	// Staff take it down, and the marker is what a seller's list can badge.
	if err := live.Takedown("hàng giả", true); err != nil {
		t.Fatalf("Takedown: %v", err)
	}
	if err := repo.SaveListing(ctx, live, testSeller); err != nil {
		t.Fatalf("SaveListing: %v", err)
	}
	if down := byID(live.ID); down.TakenDownAt == nil {
		t.Fatal("the takedown left no marker on the card")
	}
	stored, err := repo.GetListing(ctx, live.ID)
	if err != nil {
		t.Fatalf("GetListing: %v", err)
	}
	if stored.TakedownReason == nil || *stored.TakedownReason != "hàng giả" {
		t.Fatalf("reason = %v, want the moderator's words", stored.TakedownReason)
	}

	// And the CHECK is what stops a stale reason surviving the way back out: publishing clears
	// both, so the row can never say a live listing was removed.
	if err := stored.Publish(); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if err := repo.SaveListing(ctx, stored, testSeller); err != nil {
		t.Fatalf("SaveListing after publishing again: %v", err)
	}
	again, err := repo.GetListing(ctx, live.ID)
	if err != nil {
		t.Fatalf("GetListing: %v", err)
	}
	if again.TakenDownAt != nil || again.TakedownReason != nil {
		t.Fatalf("listing = %+v, want the takedown cleared", again)
	}
}
