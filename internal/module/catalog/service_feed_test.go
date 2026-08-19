package catalog_test

import (
	"context"
	"reflect"
	"testing"

	catalogapi "shopnexus/internal/module/catalog/api"
	"shopnexus/internal/module/catalog/domain"
	"shopnexus/internal/module/catalog/port"
	"shopnexus/internal/shared/id"
)

// publish takes a seeded listing all the way live, which is what the public feed shows.
func publish(t *testing.T, h *harness, listing catalogapi.ListingDetail) {
	t.Helper()
	ctx := context.Background()
	if _, err := h.svc.PublishListing(ctx, catalogapi.PublishListingRequest{
		ActorID: actor, ID: listing.ID,
	}); err != nil {
		t.Fatalf("PublishListing: %v", err)
	}
	if _, err := newHarnessModerator(h).svc.AdminApproveListing(ctx, catalogapi.ApproveListingRequest{
		ActorID: actor, ID: listing.ID,
	}); err != nil {
		t.Fatalf("AdminApproveListing: %v", err)
	}
}

// With no parameters the feed is the live listings only: a draft is the seller's business.
func TestListListings_OnlyLiveByDefault(t *testing.T) {
	h := newHarnessWith("user", true)
	ctx := context.Background()
	live := seedListing(t, h)
	publish(t, h, live)
	draft := seedListingNamed(t, h, "Second listing")

	page, err := h.svc.ListListings(ctx, catalogapi.ListListingsRequest{Page: 1, Limit: 20})
	if err != nil {
		t.Fatalf("ListListings: %v", err)
	}
	if len(page.Data) != 1 || page.Data[0].ID != live.ID {
		t.Fatalf("feed = %+v, want only the live listing", page.Data)
	}
	if page.Meta.TotalCount == nil || *page.Meta.TotalCount != 1 {
		t.Errorf("total = %v, want 1", page.Meta.TotalCount)
	}

	// mine=true is how the seller sees the draft, and status is only honoured with it.
	page, err = h.svc.ListListings(ctx, catalogapi.ListListingsRequest{
		ViewerID: actor, Mine: true, Page: 1, Limit: 20,
	})
	if err != nil {
		t.Fatalf("ListListings(mine): %v", err)
	}
	if len(page.Data) != 2 {
		t.Fatalf("own listings = %d, want both", len(page.Data))
	}
	page, err = h.svc.ListListings(ctx, catalogapi.ListListingsRequest{
		ViewerID: actor, Mine: true, Status: string(domain.StatusDraft), Page: 1, Limit: 20,
	})
	if err != nil {
		t.Fatalf("ListListings(mine,draft): %v", err)
	}
	if len(page.Data) != 1 || page.Data[0].ID != draft.ID {
		t.Fatalf("drafts = %+v, want the draft alone", page.Data)
	}
}

// The combinations that have no answer are refused rather than resolved by precedence.
func TestListListings_RefusesCombinationsWithNoAnswer(t *testing.T) {
	h := newHarnessWith("user", true)
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		req  catalogapi.ListListingsRequest
		want uint16
	}{
		{"mine without a token", catalogapi.ListListingsRequest{Mine: true, Page: 1, Limit: 20}, 401},
		{"wishlist without a token", catalogapi.ListListingsRequest{Favorited: true, Page: 1, Limit: 20}, 401},
		{"recommended without a token", catalogapi.ListListingsRequest{Sort: "recommended", Page: 1, Limit: 20}, 401},
		{"status without mine", catalogapi.ListListingsRequest{ViewerID: actor, Status: "draft", Page: 1, Limit: 20}, 400},
		{"relevance without a query", catalogapi.ListListingsRequest{Sort: "relevance", Page: 1, Limit: 20}, 400},
		{"recommended over one's own", catalogapi.ListListingsRequest{ViewerID: actor, Sort: "recommended", Mine: true, Page: 1, Limit: 20}, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := status(t, mustErr(h.svc.ListListings(ctx, tc.req))); got != tc.want {
				t.Fatalf("status = %d, want %d", got, tc.want)
			}
		})
	}
}

// The wishlist is a filter on the feed, so a saved listing comes back as a card with prices —
// which is the whole reason it is not its own endpoint.
func TestFavorites_RoundTrip(t *testing.T) {
	h := newHarnessWith("user", true)
	ctx := context.Background()
	listing := seedListing(t, h)
	publish(t, h, listing)
	buyer := id.Of[id.Account](99)

	if err := h.svc.AddFavorite(ctx, catalogapi.FavoriteRequest{ActorID: buyer, ID: listing.ID}); err != nil {
		t.Fatalf("AddFavorite: %v", err)
	}
	// Twice is once: PUT is idempotent, so a retried request is not a conflict.
	if err := h.svc.AddFavorite(ctx, catalogapi.FavoriteRequest{ActorID: buyer, ID: listing.ID}); err != nil {
		t.Fatalf("AddFavorite twice: %v", err)
	}

	page, err := h.svc.ListListings(ctx, catalogapi.ListListingsRequest{
		ViewerID: buyer, Favorited: true, Page: 1, Limit: 20,
	})
	if err != nil {
		t.Fatalf("ListListings(favorited): %v", err)
	}
	if len(page.Data) != 1 || page.Data[0].ID != listing.ID {
		t.Fatalf("wishlist = %+v, want the saved listing", page.Data)
	}
	if !page.Data[0].Favorited {
		t.Error("the card does not say it is saved")
	}
	if page.Data[0].Price == 0 {
		t.Error("the card has no price — a wishlist wants what a feed wants")
	}

	if err := h.svc.RemoveFavorite(ctx, catalogapi.FavoriteRequest{ActorID: buyer, ID: listing.ID}); err != nil {
		t.Fatalf("RemoveFavorite: %v", err)
	}
	// Also idempotent, and it does not need the listing to exist: a wishlist whose listing was
	// deleted still has to be cleanable.
	if err := h.svc.RemoveFavorite(ctx, catalogapi.FavoriteRequest{ActorID: buyer, ID: listing.ID}); err != nil {
		t.Fatalf("RemoveFavorite twice: %v", err)
	}
	page, err = h.svc.ListListings(ctx, catalogapi.ListListingsRequest{
		ViewerID: buyer, Favorited: true, Page: 1, Limit: 20,
	})
	if err != nil {
		t.Fatalf("ListListings(favorited): %v", err)
	}
	if len(page.Data) != 0 {
		t.Fatalf("wishlist = %+v, want it empty", page.Data)
	}
}

// Saving is what teaches the feed: the wishlist write refreshes the account's interest slots
// on the spot, so the next personalised feed reflects what was just saved rather than waiting
// on a sweep. Unsaving the last of them leaves none, which is the fallback to newest.
func TestFavorites_RefreshTheInterestSlots(t *testing.T) {
	h := newHarnessWith("user", true)
	ctx := context.Background()
	listing := seedListing(t, h)
	publish(t, h, listing)
	buyer := id.Of[id.Account](77)

	if interests, _ := h.repo.Interests(ctx, buyer.Int64()); len(interests) != 0 {
		t.Fatalf("interests = %+v, want none before anything was saved", interests)
	}
	if err := h.svc.AddFavorite(ctx, catalogapi.FavoriteRequest{ActorID: buyer, ID: listing.ID}); err != nil {
		t.Fatalf("AddFavorite: %v", err)
	}
	interests, err := h.repo.Interests(ctx, buyer.Int64())
	if err != nil {
		t.Fatalf("Interests: %v", err)
	}
	if len(interests) != 1 || interests[0].Weight != 1 {
		t.Fatalf("interests = %+v, want the one thing they saved, at the whole of the signal", interests)
	}

	if err := h.svc.RemoveFavorite(ctx, catalogapi.FavoriteRequest{ActorID: buyer, ID: listing.ID}); err != nil {
		t.Fatalf("RemoveFavorite: %v", err)
	}
	if interests, _ = h.repo.Interests(ctx, buyer.Int64()); len(interests) != 0 {
		t.Fatalf("interests = %+v, want none once the wishlist is empty", interests)
	}
	// And with none, the personalised feed is the newest feed rather than an empty page.
	page, err := h.svc.ListListings(ctx, catalogapi.ListListingsRequest{
		ViewerID: buyer, Sort: "recommended", Page: 1, Limit: 20,
	})
	if err != nil {
		t.Fatalf("ListListings(recommended): %v", err)
	}
	if len(page.Data) == 0 {
		t.Error("a recommended feed with no interests answered nothing, not the newest listings")
	}
}

// A page the cache already covers is served without redrawing the feed — the whole point of
// materialising a batch instead of re-running the weighted draw on every page.
func TestListListings_RecommendedCachesTheDraw(t *testing.T) {
	h := newHarnessWith("user", true)
	ctx := context.Background()
	buyer := id.Of[id.Account](80)
	for _, name := range []string{"One", "Two", "Three", "Four"} {
		publish(t, h, seedListingNamed(t, h, name))
	}
	saved := seedListingNamed(t, h, "Saved")
	publish(t, h, saved)
	if err := h.svc.AddFavorite(ctx, catalogapi.FavoriteRequest{ActorID: buyer, ID: saved.ID}); err != nil {
		t.Fatalf("AddFavorite: %v", err)
	}

	req := catalogapi.ListListingsRequest{
		ViewerID: buyer, Sort: "recommended", Seed: "one-run", Limit: 2, Page: 1,
	}
	if _, err := h.svc.ListListings(ctx, req); err != nil {
		t.Fatalf("ListListings page 1: %v", err)
	}
	if h.repo.listListingsCalls != 1 {
		t.Fatalf("draws = %d, want 1 for the first page", h.repo.listListingsCalls)
	}

	req.Page = 2
	if _, err := h.svc.ListListings(ctx, req); err != nil {
		t.Fatalf("ListListings page 2: %v", err)
	}
	if h.repo.listListingsCalls != 1 {
		t.Errorf("draws = %d, want still 1 — page 2 should read the cached batch", h.repo.listListingsCalls)
	}
	if h.repo.listListingsByIDsCalls != 1 {
		t.Errorf("hydration reads = %d, want 1 for page 2", h.repo.listListingsByIDsCalls)
	}

	// A different run of the same feed — the seed a fresh page load sends — is not served
	// from the other seed's batch.
	req.Seed, req.Page = "another-run", 1
	if _, err := h.svc.ListListings(ctx, req); err != nil {
		t.Fatalf("ListListings other seed: %v", err)
	}
	if h.repo.listListingsCalls != 2 {
		t.Errorf("draws = %d, want 2 — a new seed is a new run", h.repo.listListingsCalls)
	}
}

// A query alongside a personalised feed stays a filter on the name. Embedding it would make
// the adapter drop the lexical predicate — the concession a real semantic search earns — and
// the caller would get their whole personalised feed back under a search they typed.
func TestListListings_RecommendedNeverEmbedsTheQuery(t *testing.T) {
	h := newHarnessWith("user", true)
	ctx := context.Background()
	listing := seedListing(t, h)
	publish(t, h, listing)
	buyer := id.Of[id.Account](78)
	if err := h.svc.AddFavorite(ctx, catalogapi.FavoriteRequest{ActorID: buyer, ID: listing.ID}); err != nil {
		t.Fatalf("AddFavorite: %v", err)
	}

	if _, err := h.svc.ListListings(ctx, catalogapi.ListListingsRequest{
		ViewerID: buyer, Sort: "recommended", Query: "anything", Page: 1, Limit: 20,
	}); err != nil {
		t.Fatalf("ListListings(recommended+q): %v", err)
	}
	if h.repo.lastFilter.ProbeFromQuery {
		t.Error("the query was embedded, so the adapter would have dropped the lexical filter")
	}
	if len(h.repo.lastFilter.Interests) == 0 {
		t.Error("the feed was not ranked against the account's interests")
	}
}

// A draft is not saveable by a stranger: a wishlist of ids nobody can read renders nothing.
func TestAddFavorite_StrangersDraftNotFound(t *testing.T) {
	h := newHarnessWith("user", true)
	listing := seedListing(t, h)
	err := h.svc.AddFavorite(context.Background(), catalogapi.FavoriteRequest{
		ActorID: id.Of[id.Account](99), ID: listing.ID,
	})
	if got := status(t, err); got != 404 {
		t.Fatalf("status = %d, want 404", got)
	}
}

// A ranked query visits only its top-K, the way the dictionary's `near` ranking does — so
// its count is not a stable, seekable total and must read as nil, unlike a plain browse.
func TestListListings_TotalCountNilOnRelevance(t *testing.T) {
	h := newHarnessWith("user", true)
	ctx := context.Background()
	listing := seedListing(t, h)
	publish(t, h, listing)

	ranked, err := h.svc.ListListings(ctx, catalogapi.ListListingsRequest{
		Query: "uniqlo", Sort: "relevance", Page: 1, Limit: 20,
	})
	if err != nil {
		t.Fatalf("ListListings(relevance): %v", err)
	}
	if len(ranked.Data) != 1 {
		t.Fatalf("feed = %+v, want the match", ranked.Data)
	}
	if ranked.Meta.TotalCount != nil {
		t.Errorf("total_count = %d, want nil on a ranking", *ranked.Meta.TotalCount)
	}

	// A plain browse still answers a real, seekable count.
	browsed, err := h.svc.ListListings(ctx, catalogapi.ListListingsRequest{Page: 1, Limit: 20})
	if err != nil {
		t.Fatalf("ListListings: %v", err)
	}
	if browsed.Meta.TotalCount == nil || *browsed.Meta.TotalCount != 1 {
		t.Errorf("total_count = %v, want 1 on a plain browse", browsed.Meta.TotalCount)
	}
}

// `ids` resolves a known set and ignores the other filters — how a cart renders its rows.
func TestListListings_IDsResolveEvenWhenHidden(t *testing.T) {
	h := newHarnessWith("user", true)
	ctx := context.Background()
	listing := seedListing(t, h)
	publish(t, h, listing)
	if _, err := h.svc.HideListing(ctx, catalogapi.HideListingRequest{ActorID: actor, ID: listing.ID}); err != nil {
		t.Fatalf("HideListing: %v", err)
	}

	// Gone from the feed, still resolvable by id: an order that references it has to render.
	page, err := h.svc.ListListings(ctx, catalogapi.ListListingsRequest{Page: 1, Limit: 20})
	if err != nil {
		t.Fatalf("ListListings: %v", err)
	}
	if len(page.Data) != 0 {
		t.Fatalf("feed = %+v, want a hidden listing out of it", page.Data)
	}
	page, err = h.svc.ListListings(ctx, catalogapi.ListListingsRequest{
		IDs: []id.ID[id.Listing]{listing.ID}, Page: 1, Limit: 20,
	})
	if err != nil {
		t.Fatalf("ListListings(ids): %v", err)
	}
	if len(page.Data) != 1 || page.Data[0].ID != listing.ID {
		t.Fatalf("ids = %+v, want the hidden listing", page.Data)
	}
}

// The same rule for the other way a listing leaves the feed: a soft delete. An order that
// references it still has to render, so `ids` must not carry the feed's `deleted_at IS
// NULL` guard.
func TestListListings_IDsResolveEvenWhenDeleted(t *testing.T) {
	h := newHarnessWith("user", true)
	ctx := context.Background()
	listing := seedListing(t, h)
	publish(t, h, listing)
	if err := h.svc.DeleteListing(ctx, catalogapi.DeleteListingRequest{ActorID: actor, ID: listing.ID}); err != nil {
		t.Fatalf("DeleteListing: %v", err)
	}

	// Gone from the feed, still resolvable by id.
	page, err := h.svc.ListListings(ctx, catalogapi.ListListingsRequest{Page: 1, Limit: 20})
	if err != nil {
		t.Fatalf("ListListings: %v", err)
	}
	if len(page.Data) != 0 {
		t.Fatalf("feed = %+v, want a deleted listing out of it", page.Data)
	}
	page, err = h.svc.ListListings(ctx, catalogapi.ListListingsRequest{
		IDs: []id.ID[id.Listing]{listing.ID}, Page: 1, Limit: 20,
	})
	if err != nil {
		t.Fatalf("ListListings(ids): %v", err)
	}
	if len(page.Data) != 1 || page.Data[0].ID != listing.ID {
		t.Fatalf("ids = %+v, want the deleted listing", page.Data)
	}
}

// Where the goods are is the C2C buyer's first filter, and "near me" is the browse they use most.
// Both read the listing's own snapshot of the seller's pickup address, so the feed answers them
// without reaching into the account module row by row.
func TestListListings_FiltersByAreaAndDistance(t *testing.T) {
	h := newHarnessWith("user", true)
	ctx := context.Background()
	live := seedListing(t, h)
	publish(t, h, live)

	// The address the harness's seller collects from — its province, district and ward all match.
	area := catalogapi.ListListingsRequest{
		ProvinceCode: sellerProvinceCode, DistrictCode: sellerDistrictCode,
		WardCode: sellerWardCode, Page: 1, Limit: 20,
	}
	page, err := h.svc.ListListings(ctx, area)
	if err != nil {
		t.Fatalf("ListListings by area: %v", err)
	}
	if len(page.Data) != 1 || page.Data[0].ID != live.ID {
		t.Fatalf("feed = %+v, want the listing in that ward", page.Data)
	}
	// The card carries the names a client renders, not just the codes it filtered on.
	got := page.Data[0].Location
	if got == nil || got.ProvinceName != "Ho Chi Minh" || got.WardName != "Ben Nghe" {
		t.Fatalf("location = %+v, want the seller's address on the card", got)
	}
	if got.DistanceKM != nil {
		t.Errorf("distance = %v, want none until the buyer says where they are", *got.DistanceKM)
	}

	// Another province is another market: the listing is not in it.
	elsewhere := area
	elsewhere.ProvinceCode = "01" // Hanoi
	elsewhere.DistrictCode, elsewhere.WardCode = "", ""
	if page, err = h.svc.ListListings(ctx, elsewhere); err != nil {
		t.Fatalf("ListListings elsewhere: %v", err)
	}
	if len(page.Data) != 0 {
		t.Fatalf("feed = %+v, want nothing in another province", page.Data)
	}

	// Near me: a buyer a few streets away sees it and is told how far, one ~50 km out does not.
	near := catalogapi.ListListingsRequest{
		Latitude: new(sellerLat + 0.01), Longitude: new(sellerLon + 0.01),
		RadiusKM: new(5.0), Sort: "distance", Page: 1, Limit: 20,
	}
	if page, err = h.svc.ListListings(ctx, near); err != nil {
		t.Fatalf("ListListings near: %v", err)
	}
	if len(page.Data) != 1 {
		t.Fatalf("feed = %+v, want the listing a couple of km away", page.Data)
	}
	if d := page.Data[0].Location.DistanceKM; d == nil || *d <= 0 || *d > 5 {
		t.Fatalf("distance = %v, want a couple of km", d)
	}
	far := near
	far.Latitude, far.Longitude = new(sellerLat+0.5), new(sellerLon+0.5)
	if page, err = h.svc.ListListings(ctx, far); err != nil {
		t.Fatalf("ListListings far: %v", err)
	}
	if len(page.Data) != 0 {
		t.Fatalf("feed = %+v, want nothing within 5 km of somewhere else", page.Data)
	}
}

// A distance needs somewhere to measure from. Answering the whole feed in creation order instead
// would be a different question than the one asked, so it is refused.
func TestListListings_DistanceNeedsAPosition(t *testing.T) {
	h := newHarnessWith("user", true)
	ctx := context.Background()

	for _, req := range []catalogapi.ListListingsRequest{
		{Sort: "distance", Page: 1, Limit: 20},
		{RadiusKM: new(5.0), Page: 1, Limit: 20},
		// Half a position is not one.
		{Latitude: new(sellerLat), Sort: "distance", Page: 1, Limit: 20},
	} {
		if got := status(t, mustErr(h.svc.ListListings(ctx, req))); got != 400 {
			t.Fatalf("status = %d for %+v, want 400", got, req)
		}
	}

	// A saved address of the buyer's works too — and one that was never geocoded is told so
	// rather than quietly answering nothing.
	viewer := catalogapi.ListListingsRequest{
		ViewerID: actor, NearContactID: new(id.ID[id.Contact](500)), Sort: "distance",
		Page: 1, Limit: 20,
	}
	if _, err := h.svc.ListListings(ctx, viewer); err != nil {
		t.Fatalf("ListListings near a saved address: %v", err)
	}
	ungeocoded := newHarnessUngeocoded(h)
	if got := status(t, mustErr(ungeocoded.svc.ListListings(ctx, viewer))); got != 422 {
		t.Fatalf("status = %d, want 422 for an address with no coordinates", got)
	}
	// And an anonymous browse cannot name one of "my" addresses.
	anon := viewer
	anon.ViewerID = 0
	if got := status(t, mustErr(h.svc.ListListings(ctx, anon))); got != 401 {
		t.Fatalf("status = %d, want 401 naming an address with no session", got)
	}
}

// A seller the account module no longer has must cost the feed a name, not the page. And the page
// pays one account read per distinct shop, not one per row: the old code did both wrong, so one
// deleted account blanked the whole browse for everybody.
func TestListListings_SellerReadsAreOncePerShopAndSurviveAMissingOne(t *testing.T) {
	h := newHarnessWith("user", true)
	ctx := context.Background()
	for _, name := range []string{"First", "Second", "Third"} {
		publish(t, h, seedListingNamed(t, h, name))
	}

	reads := 0
	live := newHarnessSellerGone(h, false, &reads)
	page, err := live.svc.ListListings(ctx, catalogapi.ListListingsRequest{Page: 1, Limit: 20})
	if err != nil {
		t.Fatalf("ListListings: %v", err)
	}
	if len(page.Data) != 3 {
		t.Fatalf("feed = %d rows, want the three live listings", len(page.Data))
	}
	if reads != 1 {
		t.Errorf("account reads = %d, want one for the whole page's single shop", reads)
	}

	gone := newHarnessSellerGone(h, true, nil)
	page, err = gone.svc.ListListings(ctx, catalogapi.ListListingsRequest{Page: 1, Limit: 20})
	if err != nil {
		t.Fatalf("ListListings with the seller gone: %v", err)
	}
	if len(page.Data) != 3 {
		t.Fatalf("feed = %d rows, want the listings to survive their seller", len(page.Data))
	}
	if page.Data[0].Seller.ID == 0 || page.Data[0].Seller.Name != "" {
		t.Errorf("seller = %+v, want the id with no name", page.Data[0].Seller)
	}
}

// Chips on a card come from the card itself: a feed page of twenty is one statement, and a client
// that had to open each listing to learn its tags would make twenty requests to draw them.
func TestListListings_TagsRideOnTheCard(t *testing.T) {
	h := newHarnessWith("user", true)
	ctx := context.Background()
	staff := newHarnessAdmin(h)
	for _, slug := range []string{"ao-thun", "uniqlo"} {
		if _, err := staff.svc.AdminPutTag(ctx, catalogapi.PutTagRequest{
			ActorID: actor, Slug: slug,
		}); err != nil {
			t.Fatalf("AdminPutTag(%s): %v", slug, err)
		}
	}

	req := createListingRequest(h, t)
	req.Name = "Có tag"
	req.Tags = []string{"uniqlo", "ao-thun"}
	tagged, err := h.svc.CreateListing(ctx, req)
	if err != nil {
		t.Fatalf("CreateListing: %v", err)
	}
	publish(t, h, tagged)
	publish(t, h, seedListingNamed(t, h, "Không tag"))

	page, err := h.svc.ListListings(ctx, catalogapi.ListListingsRequest{Page: 1, Limit: 20})
	if err != nil {
		t.Fatalf("ListListings: %v", err)
	}
	byName := map[string][]string{}
	for _, card := range page.Data {
		// Empty rather than null: the contract says an array, and a client that has to nil-check a
		// required field is one the contract lied to.
		if card.Tags == nil {
			t.Fatalf("card %q has null tags", card.Name)
		}
		byName[card.Name] = card.Tags
	}
	if got := byName["Có tag"]; len(got) != 2 {
		t.Fatalf("tags = %v, want both of the listing's own", got)
	}
	if got := byName["Không tag"]; len(got) != 0 {
		t.Fatalf("tags = %v, want none", got)
	}
}

// One run of a personalised feed is one *filtered* browse of it, not everything the account
// asks for inside the seed's lifetime: the search page holds a seed for as long as the page is
// open and changes the category, the price range and the area under it. With the filters out of
// the cache key, the second browse was served the first one's batch — a page of the category
// the shopper had just navigated away from.
func TestListListings_RecommendedCacheIsPerFilter(t *testing.T) {
	h := newHarnessWith("user", true)
	ctx := context.Background()
	buyer := id.Of[id.Account](91)
	for _, name := range []string{"Alpha bike", "Bravo lamp"} {
		publish(t, h, seedListingNamed(t, h, name))
	}
	saved := seedListingNamed(t, h, "Saved thing")
	publish(t, h, saved)
	if err := h.svc.AddFavorite(ctx, catalogapi.FavoriteRequest{ActorID: buyer, ID: saved.ID}); err != nil {
		t.Fatalf("AddFavorite: %v", err)
	}

	req := catalogapi.ListListingsRequest{
		ViewerID: buyer, Sort: "recommended", Seed: "one-run", Mode: "lexical", Page: 1, Limit: 1,
	}
	req.Query = "alpha"
	if _, err := h.svc.ListListings(ctx, req); err != nil {
		t.Fatalf("ListListings(alpha): %v", err)
	}
	req.Query = "bravo"
	page, err := h.svc.ListListings(ctx, req)
	if err != nil {
		t.Fatalf("ListListings(bravo): %v", err)
	}
	for _, card := range page.Data {
		if card.Name != "Bravo lamp" {
			t.Errorf("card = %q, want the filter the request actually carried", card.Name)
		}
	}
}

// feedCacheKey names the filter's fields one by one, so a field added to port.ListingFilter is
// silently absent from the key until somebody puts it there — and absent means one browse
// served from another browse's batch. This fails on the commit that adds the field, which is
// the only place the omission is still cheap to fix.
func TestFeedCacheKey_CoversEveryFilterField(t *testing.T) {
	const named = 26
	if got := reflect.TypeFor[port.ListingFilter]().NumField(); got != named {
		t.Errorf("port.ListingFilter has %d fields, feedCacheKey was written for %d — "+
			"add the new one to feedCacheKey (or to the list it leaves out on purpose) and update this count", got, named)
	}
}
