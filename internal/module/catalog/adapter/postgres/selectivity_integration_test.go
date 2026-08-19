//go:build integration

package postgres_test

import (
	"context"
	"strconv"
	"testing"

	"shopnexus/internal/module/catalog/domain"
	"shopnexus/internal/module/catalog/port"
)

// The counts have to be over what a shopper can be shown and nothing else, and the read has to
// agree with a direct query — the whole point of the table is that a weight is scaled by a real
// share, and a count over draft rows or over a subtree would scale it by a share the predicate
// never matches.
func TestRepo_RefreshSignalSelectivityCountsTheActiveCatalogue(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	pool := poolOf(t)
	category := createCategory(t, repo, unique("cat-selectivity-"), nil)
	createTag(t, repo, "handmade", nil)

	// Two active listings under the category, one of them tagged, plus a draft that must not be
	// counted: it is not part of the catalogue a signal narrows.
	liveListing(t, repo, category.ID, "Counted ", 1, nil, "handmade")
	liveListing(t, repo, category.ID, "Counted too ", 1, nil)
	draft := newListingFor(t, repo, category.ID, unique("Draft "))
	if err := repo.SaveListing(ctx, draft, testSeller); err != nil {
		t.Fatalf("SaveListing draft: %v", err)
	}

	if err := repo.RefreshSignalSelectivity(ctx); err != nil {
		t.Fatalf("RefreshSignalSelectivity: %v", err)
	}
	sel, err := repo.SignalSelectivity(ctx)
	if err != nil {
		t.Fatalf("SignalSelectivity: %v", err)
	}

	categoryKey := domain.SelectivityKey{
		Kind: port.PredicateCategory,
		Key:  strconv.FormatInt(category.ID, 10),
	}
	if got := sel.Counts[categoryKey]; got != 2 {
		t.Errorf("category count = %d, want the two active listings and not the draft", got)
	}
	if got := sel.Counts[domain.SelectivityKey{Kind: port.PredicateTag, Key: "handmade"}]; got < 1 {
		t.Errorf("tag count = %d, want at least the one tagged listing", got)
	}

	// The total is derived from the condition rows rather than counted again, so it has to equal
	// a direct count of the active catalogue — the invariant being that condition is NOT NULL.
	var active int64
	const count = `SELECT count(*) FROM listing WHERE deleted_at IS NULL AND status = 'active'`
	if err := pool.QueryRow(ctx, count).Scan(&active); err != nil {
		t.Fatalf("count active listings: %v", err)
	}
	if sel.Total != active {
		t.Errorf("total = %d, want the %d active listings a direct count answers", sel.Total, active)
	}
	// And the count the read answered is the count the table holds for that key.
	var stored int64
	const one = `SELECT listings FROM signal_selectivity WHERE kind = $1 AND key = $2`
	if err := pool.QueryRow(ctx, one, categoryKey.Kind, categoryKey.Key).Scan(&stored); err != nil {
		t.Fatalf("read stored count: %v", err)
	}
	if stored != sel.Counts[categoryKey] {
		t.Errorf("stored %d, read %d — the projection and the row disagree", stored, sel.Counts[categoryKey])
	}

	// A key whose last listing is gone has to disappear, or a weight would go on being scaled by
	// a share of a catalogue that no longer holds it. Deleting by hand rather than through
	// SoftDeleteListing: what is under test is the refresh replacing the set, not the delete.
	if _, err := pool.Exec(ctx, `DELETE FROM listing WHERE category_id = $1`, category.ID); err != nil {
		t.Fatalf("delete listings: %v", err)
	}
	if err := repo.RefreshSignalSelectivity(ctx); err != nil {
		t.Fatalf("RefreshSignalSelectivity after delete: %v", err)
	}
	sel, err = repo.SignalSelectivity(ctx)
	if err != nil {
		t.Fatalf("SignalSelectivity after delete: %v", err)
	}
	if _, still := sel.Counts[categoryKey]; still {
		t.Errorf("category %v still counted after its listings went", categoryKey)
	}
}
