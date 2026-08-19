package catalog_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	catalogapi "shopnexus/internal/module/catalog/api"
	"shopnexus/internal/shared/id"
)

// The order comes from observability, not from the catalogue's own clock — proof the popular
// listing wins even though it was created first.
func TestListListings_TrendingOrdersByPopularity(t *testing.T) {
	h := newHarnessWith("user", true)
	ctx := context.Background()
	a := seedListingNamed(t, h, "A")
	publish(t, h, a)
	b := seedListingNamed(t, h, "B")
	publish(t, h, b)
	c := seedListingNamed(t, h, "C")
	publish(t, h, c)

	trending := newHarnessPopularity(h, fakePopularity{
		ids: []int64{c.ID.Int64(), a.ID.Int64(), b.ID.Int64()},
	})
	page, err := trending.svc.ListListings(ctx, catalogapi.ListListingsRequest{
		Sort: "trending", Page: 1, Limit: 20,
	})
	if err != nil {
		t.Fatalf("ListListings(trending): %v", err)
	}
	got := []id.ID[id.Listing]{page.Data[0].ID, page.Data[1].ID, page.Data[2].ID}
	want := []id.ID[id.Listing]{c.ID, a.ID, b.ID}
	if !slices.Equal(got, want) {
		t.Fatalf("order = %v, want %v — the popularity ranking, not creation order", got, want)
	}
	if page.Meta.TotalCount != nil {
		t.Errorf("total_count = %d, want nil on a ranking", *page.Meta.TotalCount)
	}
}

// A ranking thinner than the page it is asked for backfills with the newest listings not
// already on it, so a young platform's trending page is never emptier than its newest page
// would be.
func TestListListings_TrendingBackfillsWithNewest(t *testing.T) {
	h := newHarnessWith("user", true)
	ctx := context.Background()
	older := seedListingNamed(t, h, "Older")
	publish(t, h, older)
	newer := seedListingNamed(t, h, "Newer")
	publish(t, h, newer)

	trending := newHarnessPopularity(h, fakePopularity{ids: []int64{older.ID.Int64()}})
	page, err := trending.svc.ListListings(ctx, catalogapi.ListListingsRequest{
		Sort: "trending", Page: 1, Limit: 20,
	})
	if err != nil {
		t.Fatalf("ListListings(trending): %v", err)
	}
	if len(page.Data) != 2 {
		t.Fatalf("feed = %+v, want both — the second backfilled with newest", page.Data)
	}
	if page.Data[0].ID != older.ID {
		t.Errorf("first = %v, want the popular listing first", page.Data[0].ID)
	}
	if page.Data[1].ID != newer.ID {
		t.Errorf("second = %v, want the newest listing backfilled in", page.Data[1].ID)
	}
}

// Observability being unreachable is not a failed request: the page degrades to newest
// entirely, the same as a ranking with nothing in it.
func TestListListings_TrendingDegradesToNewestOnObservabilityFailure(t *testing.T) {
	h := newHarnessWith("user", true)
	ctx := context.Background()
	listing := seedListing(t, h)
	publish(t, h, listing)

	trending := newHarnessPopularity(h, fakePopularity{err: errors.New("boom")})
	page, err := trending.svc.ListListings(ctx, catalogapi.ListListingsRequest{
		Sort: "trending", Page: 1, Limit: 20,
	})
	if err != nil {
		t.Fatalf("ListListings(trending): %v", err)
	}
	if len(page.Data) != 1 || page.Data[0].ID != listing.ID {
		t.Fatalf("feed = %+v, want the one listing via the newest backfill", page.Data)
	}
}

// listing_popularity ranks the whole catalogue, with nothing to join that ranking against a
// filter — so trending is refused together with anything that would narrow the browse.
func TestListListings_TrendingRefusesNarrowing(t *testing.T) {
	h := newHarnessWith("user", true)
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		req  catalogapi.ListListingsRequest
	}{
		{"mine", catalogapi.ListListingsRequest{ViewerID: actor, Sort: "trending", Mine: true, Page: 1, Limit: 20}},
		{"favorited", catalogapi.ListListingsRequest{ViewerID: actor, Sort: "trending", Favorited: true, Page: 1, Limit: 20}},
		{"query", catalogapi.ListListingsRequest{Sort: "trending", Query: "phone", Page: 1, Limit: 20}},
		{"category_id", catalogapi.ListListingsRequest{Sort: "trending", CategoryID: new(id.ID[id.Category](1)), Page: 1, Limit: 20}},
		{"min_price", catalogapi.ListListingsRequest{Sort: "trending", MinPrice: new(int64(1)), Page: 1, Limit: 20}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := status(t, mustErr(h.svc.ListListings(ctx, tc.req))); got != 400 {
				t.Fatalf("status = %d, want 400", got)
			}
		})
	}
}

// A recommended feed with nothing computed for the account falls back to trending's own
// ranking, not to plain newest — proof the fallback is what it claims to be rather than a
// silent alias for the newest sort it replaced.
func TestListListings_RecommendedFallsBackToTrendingsOwnOrder(t *testing.T) {
	h := newHarnessWith("user", true)
	ctx := context.Background()
	a := seedListingNamed(t, h, "A")
	publish(t, h, a)
	b := seedListingNamed(t, h, "B")
	publish(t, h, b)

	// B is the newer listing, so a plain "newest" fallback would show it first. Popularity
	// says the opposite.
	trending := newHarnessPopularity(h, fakePopularity{ids: []int64{a.ID.Int64(), b.ID.Int64()}})
	buyer := id.Of[id.Account](81)
	page, err := trending.svc.ListListings(ctx, catalogapi.ListListingsRequest{
		ViewerID: buyer, Sort: "recommended", Page: 1, Limit: 20,
	})
	if err != nil {
		t.Fatalf("ListListings(recommended): %v", err)
	}
	if len(page.Data) != 2 || page.Data[0].ID != a.ID {
		t.Fatalf("feed = %+v, want popularity's order (A, B), not creation order", page.Data)
	}
}

// A recommended request that still names a query cannot fall back to trending — trending has
// nothing to rank the query's relevance against — so it keeps falling back to newest, filtered
// by that query, exactly as it did before trending existed.
func TestListListings_RecommendedWithQueryFallsBackToNewestNotTrending(t *testing.T) {
	h := newHarnessWith("user", true)
	ctx := context.Background()
	listing := seedListingNamed(t, h, "Uniqlo Tee")
	publish(t, h, listing)

	trending := newHarnessPopularity(h, fakePopularity{ids: []int64{listing.ID.Int64()}})
	buyer := id.Of[id.Account](82)
	page, err := trending.svc.ListListings(ctx, catalogapi.ListListingsRequest{
		ViewerID: buyer, Sort: "recommended", Query: "uniqlo", Page: 1, Limit: 20,
	})
	if err != nil {
		t.Fatalf("ListListings(recommended+q): %v", err)
	}
	if len(page.Data) != 1 || page.Data[0].ID != listing.ID {
		t.Fatalf("feed = %+v, want the query still filtering the fallback", page.Data)
	}
	if trending.repo.listListingsByIDsCalls != 0 {
		t.Error("hydrated by id — that is trending's path, not newest's")
	}
}
