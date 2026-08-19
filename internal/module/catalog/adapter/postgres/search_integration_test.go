//go:build integration

package postgres_test

import (
	"context"
	"slices"
	"testing"

	pgadapter "shopnexus/internal/module/catalog/adapter/postgres"
	"shopnexus/internal/module/catalog/port"
)

// liveListing is one published, approved listing with a known embedding: an angle from the probe
// in the dense half, exact tokens in the sparse one (nil for a row no sparse leg can see), and
// whatever tags the case boosts on.
func liveListing(t *testing.T, repo *pgadapter.Repo, categoryID int64, name string,
	dense float32, sparse any, tags ...string) int64 {
	t.Helper()
	ctx := context.Background()
	l := newListingFor(t, repo, categoryID, unique(name))
	l.Tags = append(l.Tags, tags...)
	if err := l.Publish(); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if err := l.Approve(""); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if err := repo.SaveListing(ctx, l, testSeller); err != nil {
		t.Fatalf("SaveListing: %v", err)
	}
	const q = `INSERT INTO listing_embedding (listing_id, dense, sparse)
	           VALUES ($1, $2::vector, $3::sparsevec)
	           ON CONFLICT (listing_id) DO UPDATE
	             SET dense = EXCLUDED.dense, sparse = EXCLUDED.sparse`
	if _, err := poolOf(t).Exec(ctx, q, l.ID, denseAxis(dense), sparse); err != nil {
		t.Fatalf("insert embedding: %v", err)
	}
	return l.ID
}

// ids is the answer in page order, which is what an assertion about ranking needs.
func ids(rows []port.ListingSummary) []int64 {
	out := make([]int64, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.ID)
	}
	return out
}

// Both legs must rank, each must be served by its own index, and a row both legs found has to
// outrank a row only one of them did — which is the whole reason to fuse. A listing that only the
// sparse half can see (the exact words, no semantic neighbourhood) has to reach the page, because
// for two years it could not: nothing read the column.
func TestRepo_SearchFusesBothLegs(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	category := createCategory(t, repo, unique("cat-"), nil)
	createTag(t, repo, "handmade", nil)

	// near: close in dense space, nothing in common lexically.
	near := liveListing(t, repo, category.ID, "Near ", 1, "{9:1}/250048")
	// wordy: far in dense space — below the dense leg's floor, so that leg cannot see it — but it
	// is the one carrying the query's tokens. Its indices are one-based, as the column is, so the
	// probe's zero-based 1 is this row's 2.
	wordy := liveListing(t, repo, category.ID, "Wordy ", 0, "{2:1,3:1}/250048")
	// both: a little behind near in dense space and ahead of wordy lexically, so the only thing
	// that can put it first is the two contributions adding up.
	both := liveListing(t, repo, category.ID, "Both ", 0.9, "{2:1.5,3:1.5}/250048")

	probe := port.Probe{Dense: axis1024(1), Sparse: map[uint32]float32{1: 1, 2: 1}}
	base := port.ListingFilter{
		CategoryID: category.ID,
		Sort:       port.SortRelevance,
		Terms:      []port.Term{{Weight: 1, Probe: &probe}},
		Limit:      20,
	}
	rows, _, err := repo.ListListings(ctx, base)
	if err != nil {
		t.Fatalf("ListListings: %v", err)
	}
	for _, row := range rows {
		if row.Score == nil {
			t.Errorf("listing %d came back from a search with no score", row.ID)
		}
	}
	order := ids(rows)
	if !slices.Contains(order, near) {
		t.Error("the dense leg contributed nothing")
	}
	if !slices.Contains(order, wordy) {
		t.Error("the sparse leg contributed nothing — this is the leg that has never run")
	}
	if len(order) == 0 || order[0] != both {
		t.Errorf("order = %v, want the row both legs found (%d) first", order, both)
	}
	if slices.Index(order, near) > slices.Index(order, wordy) {
		t.Errorf("order = %v, want the near listing (%d) above the far one (%d)", order, near, wordy)
	}

	// The fused pool is also what the other sorts page: "newest, but still about what I searched
	// for". Every one of them has to be a statement Postgres accepts, and one that counts.
	for _, order := range []string{
		port.SortNewest, port.SortRating, port.SortPriceAsc, port.SortPriceDesc,
		port.SortBestSelling,
	} {
		f := base
		f.Sort = order
		reranked, total, err := repo.ListListings(ctx, f)
		if err != nil {
			t.Fatalf("ListListings(sort=%s): %v", order, err)
		}
		if total != int64(len(reranked)) {
			t.Errorf("sort=%s counted %d over %d rows, want the fused pool's own size",
				order, total, len(reranked))
		}
	}

	// The ceiling the caps allow: four boost probes, two demotes and a predicate — twelve ANN legs
	// and a pool union over eight of them. A statement this size has to be one Postgres accepts.
	ceiling := base
	ceiling.Terms = []port.Term{
		{Weight: 1, Probe: &probe}, {Weight: 0.5, Probe: &probe}, {Weight: 0.33, Probe: &probe},
		{Weight: 1, Probe: &probe},
		{Weight: -1, Probe: &probe}, {Weight: -0.5, Probe: &probe},
		{Weight: 0.6, Predicate: &port.Predicate{Kind: port.PredicateCondition, Value: "used"}},
	}
	if _, _, err := repo.ListListings(ctx, ceiling); err != nil {
		t.Fatalf("ListListings(six probes and a predicate): %v", err)
	}
}

// A boost predicate adjusts a row the probe retrieved and can never introduce one. Both halves are
// load-bearing: a category or tag boost has neither a floor nor a limit, so unrestricted it injects
// every active row satisfying it at rank 1 — ahead of a genuine probe hit — and with no positive
// probe at all it would answer the whole catalogue.
func TestRepo_SearchBoostPredicateAdjustsWithoutRetrieving(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	category := createCategory(t, repo, unique("cat-"), nil)
	createTag(t, repo, "handmade", nil)
	boosted := createTag(t, repo, "vip-boost", nil)

	hit := liveListing(t, repo, category.ID, "Hit ", 1, nil)
	alsoNear := liveListing(t, repo, category.ID, "Also near ", 0.85, nil, boosted.Slug)
	farBoosted := liveListing(t, repo, category.ID, "Far boosted ", 0, nil, boosted.Slug)

	probe := port.Probe{Dense: axis1024(1)}
	tagBoost := port.Term{
		Weight:    0.6,
		Predicate: &port.Predicate{Kind: port.PredicateTag, Value: boosted.Slug},
	}
	base := port.ListingFilter{CategoryID: category.ID, Sort: port.SortRelevance, Limit: 20}

	f := base
	f.Terms = []port.Term{{Weight: 1, Probe: &probe}, tagBoost}
	rows, _, err := repo.ListListings(ctx, f)
	if err != nil {
		t.Fatalf("ListListings(probe + boost): %v", err)
	}
	order := ids(rows)
	if slices.Contains(order, farBoosted) {
		t.Errorf("order = %v, want the boosted row no probe retrieved (%d) left out", order, farBoosted)
	}
	if !slices.Equal(order, []int64{alsoNear, hit}) {
		t.Errorf("order = %v, want the boost to lift the retrieved row it applies to (%d, then %d)",
			order, alsoNear, hit)
	}

	// No positive probe: nothing was retrieved, so there is nothing for a predicate to adjust.
	for _, name := range []string{"predicate alone", "predicate and a demote"} {
		f := base
		f.Terms = []port.Term{tagBoost}
		if name == "predicate and a demote" {
			f.Terms = append(f.Terms, port.Term{Weight: -1, Probe: &probe})
		}
		rows, _, err := repo.ListListings(ctx, f)
		if err != nil {
			t.Fatalf("ListListings(%s): %v", name, err)
		}
		if len(rows) != 0 {
			t.Errorf("%s answered %v, want an empty page rather than the catalogue", name, ids(rows))
		}
	}
}

// A demote adjusts a retrieved row's place and must not put its own nearest rows on the page. With
// sort=relevance an introduced demote fills the page below the genuine hits; with sort=newest the
// pool is reordered by created_at, so the newest *demoted* listing becomes row 1 and is counted.
func TestRepo_SearchDemoteDoesNotIntroduceRows(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	category := createCategory(t, repo, unique("cat-"), nil)
	createTag(t, repo, "handmade", nil)

	hit := liveListing(t, repo, category.ID, "Hit ", 1, nil)
	mid := liveListing(t, repo, category.ID, "Mid ", 0.85, nil)
	// far is what the demote phrase is nearest to, and it is the newest row in the category.
	far := liveListing(t, repo, category.ID, "Far ", 0, nil)

	query := port.Term{Weight: 1, Probe: &port.Probe{Dense: axis1024(1)}}
	away := port.Term{Weight: -1, Probe: &port.Probe{Dense: axis1024(0)}}
	base := port.ListingFilter{CategoryID: category.ID, Limit: 20}

	f := base
	f.Sort, f.Terms = port.SortRelevance, []port.Term{query, away}
	rows, _, err := repo.ListListings(ctx, f)
	if err != nil {
		t.Fatalf("ListListings(probe + demote): %v", err)
	}
	if order := ids(rows); !slices.Equal(order, []int64{hit, mid}) {
		t.Errorf("order = %v, want only what the query retrieved (%d, %d)", order, hit, mid)
	}

	f.Sort = port.SortNewest
	rows, total, err := repo.ListListings(ctx, f)
	if err != nil {
		t.Fatalf("ListListings(sort=newest): %v", err)
	}
	if slices.Contains(ids(rows), far) {
		t.Errorf("sort=newest answered %v, want the demoted row (%d) nowhere on the page",
			ids(rows), far)
	}
	if total != 2 {
		t.Errorf("total = %d, want the two retrieved rows", total)
	}

	// The other half of the rule: a demote the pool does reach still lowers that row. This one
	// leans towards the query, so both retrieved rows are inside its floor.
	f.Sort = port.SortRelevance
	f.Terms = []port.Term{query, {Weight: -1, Probe: &port.Probe{Dense: axis1024(0.9)}}}
	rows, _, err = repo.ListListings(ctx, f)
	if err != nil {
		t.Fatalf("ListListings(probe + near demote): %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %v, want both retrieved rows still on the page", ids(rows))
	}
	for _, row := range rows {
		if row.ID == mid && (row.Score == nil || *row.Score >= 0) {
			t.Errorf("mid score = %v, want the demote to have taken it below zero", row.Score)
		}
	}
}

// A probe with no sparse half must not rank on the sparse leg: an empty sparsevec is at distance
// zero from every row, so the leg would deal in noise and its floor — a share of that same
// zero — would keep all of it.
func TestRepo_SearchLeavesOutAHalfWithNothingInIt(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	category := createCategory(t, repo, unique("cat-"), nil)
	createTag(t, repo, "handmade", nil)

	dense := liveListing(t, repo, category.ID, "Dense only ", 1, nil)

	probe := port.Probe{Dense: axis1024(1)}
	rows, _, err := repo.ListListings(ctx, port.ListingFilter{
		CategoryID: category.ID,
		Sort:       port.SortRelevance,
		Terms:      []port.Term{{Weight: 1, Probe: &probe}},
		Limit:      20,
	})
	if err != nil {
		t.Fatalf("ListListings: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != dense {
		t.Fatalf("rows = %v, want the one dense hit", ids(rows))
	}
}

// axis1024 is a unit vector along one axis, as a probe rather than a literal.
func axis1024(first float32) port.Vector {
	v := make(port.Vector, 1024)
	v[0], v[1] = first, 1-first
	return v
}
