//go:build integration

package postgres_test

import (
	"context"
	"math"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

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
		Limit:      20,
	}
	rows, _, err := repo.ListListings(ctx, full)
	if err != nil {
		t.Fatalf("ListListings(every filter): %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("the live listing matched nothing")
	}
	if rows[0].Score != nil {
		t.Error("a browse answered a score — nothing ranked it")
	}
	if rows[0].Price == 0 {
		t.Error("the card has no price — the lateral join found no variant")
	}

	// The same filters through the search statement, which carries this very feedWhere inside each
	// of its ANN legs: every branch above has to be legal there too. The listing has no embedding,
	// so what this asserts is the statement — TestRepo_SearchFusesBothLegs asserts the ranking.
	searched := full
	searched.Sort = port.SortRelevance
	searched.Terms = []port.Term{{
		Weight: 1,
		Probe:  &port.Probe{Dense: axis1024(1), Sparse: map[uint32]float32{1: 1}},
	}}
	if _, _, err := repo.ListListings(ctx, searched); err != nil {
		t.Errorf("ListListings(search, every filter): %v", err)
	}

	// Every sort has to be a statement Postgres accepts.
	for _, order := range []string{
		port.SortNewest, port.SortRating, port.SortPriceAsc, port.SortPriceDesc,
		port.SortBestSelling, port.SortRelevance, port.SortRecommended,
	} {
		f := port.ListingFilter{ViewerID: testSeller, SellerID: testSeller, Sort: order, Limit: 5}
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

// The personalised feed reads the catalogue once per interest and merges what comes back, so
// what a test has to prove is that every interest reaches the page — including the weakest,
// which a single best-score pass would have buried — and that the two exclusions hold.
func TestRepo_ListListingsMergesEveryInterest(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	pool := poolOf(t)
	category := createCategory(t, repo, unique("cat-"), nil)
	createTag(t, repo, "handmade", nil)
	// A buyer of their own, so the wishlist and the interests below belong to nobody else.
	buyer := testSeller + 7_000_000

	// Two directions in embedding space, and several listings along each. More than one per
	// direction, because the merge is about ranks within an interest.
	publish := func(name string, first float32) *domain.Listing {
		t.Helper()
		l := newListingFor(t, repo, category.ID, unique(name))
		if err := l.Publish(); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		if err := l.Approve(""); err != nil {
			t.Fatalf("Approve: %v", err)
		}
		if err := repo.SaveListing(ctx, l, testSeller); err != nil {
			t.Fatalf("SaveListing: %v", err)
		}
		const q = `INSERT INTO listing_embedding (listing_id, dense) VALUES ($1, $2::vector)
		           ON CONFLICT (listing_id) DO UPDATE SET dense = EXCLUDED.dense`
		if _, err := pool.Exec(ctx, q, l.ID, denseAxis(first)); err != nil {
			t.Fatalf("insert embedding: %v", err)
		}
		return l
	}
	near := []*domain.Listing{publish("Near a ", 1), publish("Near b ", 0.99), publish("Near c ", 0.98)}
	far := []*domain.Listing{publish("Far a ", 0), publish("Far b ", 0.01)}
	saved := publish("Saved ", 1)
	if err := repo.AddFavorite(ctx, buyer, saved.ID); err != nil {
		t.Fatalf("AddFavorite: %v", err)
	}

	toward := func(first float32) port.Vector {
		v := make(port.Vector, 1024)
		v[0], v[1] = first, 1-first
		return v
	}
	// Lopsided on purpose: the second interest is a tenth of the signal, which is exactly the
	// one a "best score wins" ranking never shows.
	feed := func(seed string) []port.ListingSummary {
		t.Helper()
		rows, _, err := repo.ListListings(ctx, port.ListingFilter{
			ViewerID:   buyer,
			CategoryID: category.ID,
			Sort:       port.SortRecommended,
			Seed:       seed,
			Interests: []port.Interest{
				{Vector: toward(1), Weight: 0.9},
				{Vector: toward(0), Weight: 0.1},
			},
			Limit: 20,
		})
		if err != nil {
			t.Fatalf("ListListings(recommended, seed %q): %v", seed, err)
		}
		return rows
	}
	rows := feed("seed-a")

	got := make(map[int64]bool, len(rows))
	for _, row := range rows {
		got[row.ID] = true
	}
	for _, l := range append(append([]*domain.Listing{}, near...), far...) {
		if !got[l.ID] {
			t.Errorf("listing %d is missing — an interest reached none of the page", l.ID)
		}
	}
	if got[saved.ID] {
		t.Error("a listing already on the wishlist came back as a recommendation")
	}

	// One seed is one feed: page two is drawn from the same ordering as page one, or a reader
	// paging down sees a card twice and never sees the one it displaced.
	ids := func(rows []port.ListingSummary) string {
		out := make([]string, 0, len(rows))
		for _, row := range rows {
			out = append(out, strconv.FormatInt(row.ID, 10))
		}
		return strings.Join(out, ",")
	}
	if again := ids(feed("seed-a")); again != ids(rows) {
		t.Errorf("the same seed drew a different feed:\n %s\n %s", ids(rows), again)
	}
	// And a different seed is a different feed, which is the whole point of drawing one.
	var differs bool
	for _, seed := range []string{"seed-b", "seed-c", "seed-d"} {
		if ids(feed(seed)) != ids(rows) {
			differs = true
			break
		}
	}
	if !differs {
		t.Error("three other seeds all drew the identical order — the draw is not reading the seed")
	}

	// The seller's own goods are not something to recommend them — not through an interest,
	// and not through the fresh source either, which is the one that carries no vector.
	own, _, err := repo.ListListings(ctx, port.ListingFilter{
		ViewerID:   testSeller,
		CategoryID: category.ID,
		Sort:       port.SortRecommended,
		Seed:       "seed-a",
		Interests:  []port.Interest{{Vector: toward(1), Weight: 1}},
		Limit:      20,
	})
	if err != nil {
		t.Fatalf("ListListings(own): %v", err)
	}
	if len(own) != 0 {
		t.Errorf("own = %+v, want none: every listing here belongs to the caller", own)
	}
}

// A listing nothing has embedded yet is unreachable through an interest — there is no distance
// to measure — and it is also the newest thing on the platform. The fresh source is what puts
// it in front of somebody, and without it the feed can only ever answer its own past.
func TestRepo_ListListingsRecommendsWhatHasNoVectorYet(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	pool := poolOf(t)
	category := createCategory(t, repo, unique("cat-"), nil)
	createTag(t, repo, "handmade", nil)
	buyer := testSeller + 9_000_000

	publish := func(name string) *domain.Listing {
		t.Helper()
		l := newListingFor(t, repo, category.ID, unique(name))
		if err := l.Publish(); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		if err := l.Approve(""); err != nil {
			t.Fatalf("Approve: %v", err)
		}
		if err := repo.SaveListing(ctx, l, testSeller); err != nil {
			t.Fatalf("SaveListing: %v", err)
		}
		return l
	}
	matched := publish("Matched ")
	const q = `INSERT INTO listing_embedding (listing_id, dense) VALUES ($1, $2::vector)
	           ON CONFLICT (listing_id) DO UPDATE SET dense = EXCLUDED.dense`
	if _, err := pool.Exec(ctx, q, matched.ID, denseAxis(1)); err != nil {
		t.Fatalf("insert embedding: %v", err)
	}
	// Posted after it, and never embedded.
	unembedded := publish("Just posted ")

	probe := make(port.Vector, 1024)
	probe[0] = 1
	rows, _, err := repo.ListListings(ctx, port.ListingFilter{
		ViewerID:   buyer,
		CategoryID: category.ID,
		Sort:       port.SortRecommended,
		Seed:       "seed-a",
		Interests:  []port.Interest{{Vector: probe, Weight: 1}},
		Limit:      20,
	})
	if err != nil {
		t.Fatalf("ListListings: %v", err)
	}
	var sawFresh bool
	for _, row := range rows {
		if row.ID == unembedded.ID {
			sawFresh = true
			if row.Score != nil {
				t.Errorf("score = %v, want null: nothing measured this card against anything", *row.Score)
			}
		}
	}
	if !sawFresh {
		t.Error("a newly posted listing with no vector never reached the feed")
	}
}

// The slots are rebuilt from the wishlist, capped at NumInterests, strongest first, and the
// weights are shares of the whole signal — that last part is what the merge above divides by.
func TestRepo_RecomputeInterests(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	pool := poolOf(t)
	buyer := testSeller + 8_000_000
	createTag(t, repo, "handmade", nil)

	// Five categories, so the cap has something to cut, and a different number of saved
	// listings in each so the order is a fact rather than a coin toss.
	counts := []int{5, 4, 3, 2, 1}
	for _, n := range counts {
		category := createCategory(t, repo, unique("cat-"), nil)
		for i := 0; i < n; i++ {
			l := newListingFor(t, repo, category.ID, unique("Interest "))
			if err := l.Publish(); err != nil {
				t.Fatalf("Publish: %v", err)
			}
			if err := l.Approve(""); err != nil {
				t.Fatalf("Approve: %v", err)
			}
			if err := repo.SaveListing(ctx, l, testSeller); err != nil {
				t.Fatalf("SaveListing: %v", err)
			}
			const q = `INSERT INTO listing_embedding (listing_id, dense) VALUES ($1, $2::vector)
			           ON CONFLICT (listing_id) DO UPDATE SET dense = EXCLUDED.dense`
			if _, err := pool.Exec(ctx, q, l.ID, denseAxis(1)); err != nil {
				t.Fatalf("insert embedding: %v", err)
			}
			if err := repo.AddFavorite(ctx, buyer, l.ID); err != nil {
				t.Fatalf("AddFavorite: %v", err)
			}
		}
	}

	// An account nobody has computed yet is stale, which is what the sweep looks for.
	stale, err := repo.StaleInterests(ctx, 1000)
	if err != nil {
		t.Fatalf("StaleInterests: %v", err)
	}
	if !slices.Contains(stale, buyer) {
		t.Error("an account with a wishlist and no slots was not reported stale")
	}

	if err := repo.RecomputeInterests(ctx, buyer, signalWeights()); err != nil {
		t.Fatalf("RecomputeInterests: %v", err)
	}
	interests, err := repo.Interests(ctx, buyer)
	if err != nil {
		t.Fatalf("Interests: %v", err)
	}
	if len(interests) != domain.NumInterests {
		t.Fatalf("interests = %d, want %d: five categories capped to the slot count",
			len(interests), domain.NumInterests)
	}
	var total float64
	for i, in := range interests {
		if len(in.Vector) != 1024 {
			t.Errorf("interest %d has %d dimensions, want 1024", i, len(in.Vector))
		}
		if i > 0 && in.Weight > interests[i-1].Weight {
			t.Errorf("interest %d is stronger than the one before it", i)
		}
		total += in.Weight
	}
	if math.Abs(total-1) > 1e-6 {
		t.Errorf("weights sum to %v, want 1: the merge divides a rank by a share", total)
	}

	// Recomputed, the account is no longer behind its wishlist.
	stale, err = repo.StaleInterests(ctx, 1000)
	if err != nil {
		t.Fatalf("StaleInterests: %v", err)
	}
	if slices.Contains(stale, buyer) {
		t.Error("an account recomputed just now is still reported stale")
	}

	// A wishlist emptied leaves no slots behind, or the feed keeps ranking against a taste
	// the account has withdrawn.
	if _, err := pool.Exec(ctx, `DELETE FROM favorite WHERE account_id = $1`, buyer); err != nil {
		t.Fatalf("clear wishlist: %v", err)
	}
	if err := repo.RecomputeInterests(ctx, buyer, signalWeights()); err != nil {
		t.Fatalf("RecomputeInterests(empty): %v", err)
	}
	if interests, err = repo.Interests(ctx, buyer); err != nil || len(interests) != 0 {
		t.Fatalf("interests = %+v, %v; want none", interests, err)
	}
}

// denseAxis is a 1024-wide literal leaning `first` toward one axis and the rest toward
// another, which is how these tests place a listing at a known angle from a probe.
func denseAxis(first float32) string {
	out := make([]byte, 0, 4096)
	out = append(out, '[')
	for i := range 1024 {
		if i > 0 {
			out = append(out, ',')
		}
		switch i {
		case 0:
			out = append(out, []byte(strconv.FormatFloat(float64(first), 'f', -1, 32))...)
		case 1:
			out = append(out, []byte(strconv.FormatFloat(float64(1-first), 'f', -1, 32))...)
		default:
			out = append(out, '0')
		}
	}
	return string(append(out, ']'))
}

// A personalised feed's probes come from the account and have nothing to do with `q`, so the
// query is a filter on the name there and the only path where it still is one: a search carries
// its words as terms and ranks them instead.
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

	// Both embedded, because a personalised feed ranks by vector and a listing without one has
	// no place in the ranking at all — so without this the lexical filter would look like it
	// held when nothing had reached it.
	pool := poolOf(t)
	for _, l := range []*domain.Listing{match, other} {
		const q = `INSERT INTO listing_embedding (listing_id, dense) VALUES ($1, $2::vector)
		           ON CONFLICT (listing_id) DO UPDATE SET dense = EXCLUDED.dense`
		if _, err := pool.Exec(ctx, q, l.ID, denseAxis(1)); err != nil {
			t.Fatalf("insert embedding: %v", err)
		}
	}

	probe := make(port.Vector, 1024)
	probe[0] = 1
	const query = "ao thun uniqlo"
	// Scoped to this test's own category. The dev database is shared and seeded, and once the
	// embedder has run over that seed every listing has a vector — so an unscoped assertion of
	// "only mine came back" tests the neighbours rather than the probe.
	rows, _, err := repo.ListListings(ctx, port.ListingFilter{
		Query: query, Sort: port.SortRecommended,
		Interests:  []port.Interest{{Vector: probe, Weight: 1}},
		CategoryID: category.ID, Limit: 20,
	})
	if err != nil {
		t.Fatalf("ListListings: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != match.ID {
		t.Fatalf("rows = %+v, want only the listing matching %q", rows, query)
	}
}

// `q` with sort=recommended is a filter on the name, not a ranking: that feed ranks by the
// account's interests, so the words have nothing to rank against. Diacritic-insensitive on both
// sides, because this is the one search path that still sees a shopper's raw typing — an LLM
// normalises a probe, and nothing normalises this.
func TestRepo_RecommendedFiltersByNameWithoutDiacritics(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	pool := poolOf(t)
	category := createCategory(t, repo, unique("cat-"), nil)
	createTag(t, repo, "handmade", nil)
	buyer := testSeller + 9_000_000

	publish := func(name string) *domain.Listing {
		t.Helper()
		l := newListingFor(t, repo, category.ID, name+unique(" "))
		if err := l.Publish(); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		if err := l.Approve(""); err != nil {
			t.Fatalf("Approve: %v", err)
		}
		if err := repo.SaveListing(ctx, l, testSeller); err != nil {
			t.Fatalf("SaveListing: %v", err)
		}
		const q = `INSERT INTO listing_embedding (listing_id, dense) VALUES ($1, $2::vector)
		           ON CONFLICT (listing_id) DO UPDATE SET dense = EXCLUDED.dense`
		if _, err := pool.Exec(ctx, q, l.ID, denseAxis(1)); err != nil {
			t.Fatalf("insert embedding: %v", err)
		}
		return l
	}
	wanted := publish("Áo thun Uniqlo")
	other := publish("Nồi cơm điện")

	interest := make(port.Vector, 1024)
	interest[0] = 1
	rows, _, err := repo.ListListings(ctx, port.ListingFilter{
		ViewerID:   buyer,
		CategoryID: category.ID,
		Sort:       port.SortRecommended,
		Seed:       "one-run",
		Query:      "ao thun",
		Interests:  []port.Interest{{Vector: interest, Weight: 1}},
		Limit:      20,
	})
	if err != nil {
		t.Fatalf("ListListings: %v", err)
	}
	var ids []int64
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	if !slices.Contains(ids, wanted.ID) {
		t.Errorf("rows = %v, want the listing whose name matches the no-diacritic query", ids)
	}
	if slices.Contains(ids, other.ID) {
		t.Errorf("rows = %v, want the query to have filtered the rest out", ids)
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
		Query: "probe", Terms: []port.Term{{Weight: 1, Probe: &port.Probe{Dense: probe}}},
		Sort: port.SortRelevance, Limit: 5,
	}); err != nil {
		t.Fatalf("ListListings(hybrid): %v", err)
	}

	// An id nobody else can be using. A fixed 4242 passed for as long as the only rows in this
	// schema were fixtures, and stopped the moment dev/bulkseed put a million listings and ten
	// thousand accounts in it — account 4242 was `bulk_buyer_004242`, with 136 favourites of its
	// own, and this assertion is about owning the account rather than about the query.
	buyer := time.Now().UnixNano()
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
