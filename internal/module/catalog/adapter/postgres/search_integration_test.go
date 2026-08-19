//go:build integration

package postgres_test

import (
	"context"
	"testing"

	"shopnexus/internal/module/catalog/port"
)

// Both legs must rank, and each must be served by its own index. A listing that only the sparse
// half can see — the exact words, no semantic neighbourhood — has to reach the page, because for
// two years it could not: nothing read the column.
func TestRepo_SearchFusesBothLegs(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	pool := poolOf(t)
	category := createCategory(t, repo, unique("cat-"), nil)
	createTag(t, repo, "handmade", nil)

	live := func(name string, dense float32, sparse string) int64 {
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
		const q = `INSERT INTO listing_embedding (listing_id, dense, sparse)
		           VALUES ($1, $2::vector, $3::sparsevec)
		           ON CONFLICT (listing_id) DO UPDATE
		             SET dense = EXCLUDED.dense, sparse = EXCLUDED.sparse`
		if _, err := pool.Exec(ctx, q, l.ID, denseAxis(dense), sparse); err != nil {
			t.Fatalf("insert embedding: %v", err)
		}
		return l.ID
	}
	// near: close in dense space, nothing in common lexically.
	near := live("Near ", 1, "{9:1}/250048")
	// wordy: far in dense space, but it is the one carrying the query's tokens. Its indices are
	// one-based, as the column is, so the probe's zero-based 1 is this row's 2.
	wordy := live("Wordy ", 0, "{2:1,3:1}/250048")

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
	seen := map[int64]bool{}
	for _, row := range rows {
		seen[row.ID] = true
		if row.Score == nil {
			t.Errorf("listing %d came back from a search with no score", row.ID)
		}
	}
	if !seen[near] {
		t.Error("the dense leg contributed nothing")
	}
	if !seen[wordy] {
		t.Error("the sparse leg contributed nothing — this is the leg that has never run")
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
}

// A probe with no sparse half must not rank on the sparse leg: an empty sparsevec is at distance
// zero from every row, so the leg would deal in noise and its floor — a share of that same
// zero — would keep all of it.
func TestRepo_SearchLeavesOutAHalfWithNothingInIt(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	pool := poolOf(t)
	category := createCategory(t, repo, unique("cat-"), nil)
	createTag(t, repo, "handmade", nil)

	l := newListingFor(t, repo, category.ID, unique("Dense only "))
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
	           VALUES ($1, $2::vector, NULL)
	           ON CONFLICT (listing_id) DO UPDATE
	             SET dense = EXCLUDED.dense, sparse = NULL`
	if _, err := pool.Exec(ctx, q, l.ID, denseAxis(1)); err != nil {
		t.Fatalf("insert embedding: %v", err)
	}

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
	if len(rows) != 1 || rows[0].ID != l.ID {
		t.Fatalf("rows = %+v, want the one dense hit", rows)
	}
}

// axis1024 is a unit vector along one axis, as a probe rather than a literal.
func axis1024(first float32) port.Vector {
	v := make(port.Vector, 1024)
	v[0], v[1] = first, 1-first
	return v
}
