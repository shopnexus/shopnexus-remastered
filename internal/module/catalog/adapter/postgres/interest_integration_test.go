//go:build integration

package postgres_test

import (
	"context"
	"slices"
	"testing"

	catalogapi "shopnexus/internal/module/catalog/api"
	"shopnexus/internal/module/catalog/domain"
	"shopnexus/internal/module/catalog/port"
)

// interestSignals unions the wishlist with listing_signal, and every recompute in production
// failed for a fortnight because each branch of that union carried its own ORDER BY and LIMIT —
// which Postgres refuses outright. The fake reimplements the fold in Go, so it accepted the
// statement no database ever would; only a real one can say the query parses at all.
func TestRepo_RecomputeInterestsFoldsBothSources(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	pool := poolOf(t)
	// Two categories, because a category is what groups an account's interests into slots: one
	// of each proves the fold produced two rather than averaging everything into one taste.
	saves := createCategory(t, repo, unique("cat-saved-"), nil)
	views := createCategory(t, repo, unique("cat-viewed-"), nil)
	createTag(t, repo, "handmade", nil)
	buyer := testSeller + 8_000_000

	publish := func(categoryID int64, name string, first float32) *domain.Listing {
		t.Helper()
		l := newListingFor(t, repo, categoryID, unique(name))
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
	saved := publish(saves.ID, "Saved ", 1)
	viewed := publish(views.ID, "Viewed ", 0)

	if err := repo.AddFavorite(ctx, buyer, saved.ID); err != nil {
		t.Fatalf("AddFavorite: %v", err)
	}
	if err := repo.InsertListingSignals(ctx, []port.ListingSignal{
		{AccountID: buyer, ListingID: viewed.ID, Type: catalogapi.InteractionView},
	}); err != nil {
		t.Fatalf("InsertListingSignals: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `DELETE FROM listing_signal WHERE account_id = $1`, buyer); err != nil {
			t.Logf("cleanup signals: %v", err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM account_interest WHERE account_id = $1`, buyer); err != nil {
			t.Logf("cleanup interests: %v", err)
		}
	})

	// An account with a saved listing and a signalled one is behind its sources, so the sweep
	// has to name it before anything is computed.
	stale, err := repo.StaleInterests(ctx, 500)
	if err != nil {
		t.Fatalf("StaleInterests: %v", err)
	}
	if !slices.Contains(stale, buyer) {
		t.Fatalf("stale accounts = %v, want the buyer in it before the first recompute", stale)
	}

	if err := repo.RecomputeInterests(ctx, buyer, signalWeights()); err != nil {
		t.Fatalf("RecomputeInterests: %v", err)
	}

	interests, err := repo.Interests(ctx, buyer)
	if err != nil {
		t.Fatalf("Interests: %v", err)
	}
	if len(interests) != 2 {
		t.Fatalf("interests = %d, want one slot per category — the wishlist's and the signal's", len(interests))
	}
	// Strength is stored as a share of the whole signal, which is what lets the feed hand each
	// interest a slice of the page without knowing how the numbers were arrived at.
	var total float64
	for _, in := range interests {
		if len(in.Vector) != 1024 {
			t.Errorf("interest vector width = %d, want the column's 1024", len(in.Vector))
		}
		total += in.Weight
	}
	if total < 0.999 || total > 1.001 {
		t.Errorf("shares sum to %v, want 1", total)
	}
	// A save weighs more than a view, and the slots come back strongest first.
	if interests[0].Weight <= interests[1].Weight {
		t.Errorf("shares = %v, %v, want the wishlist's slot first", interests[0].Weight, interests[1].Weight)
	}
	// And with the slots current, the sweep must not pick the account up again — a pass on a
	// healthy platform reads one query and writes nothing.
	stale, err = repo.StaleInterests(ctx, 500)
	if err != nil {
		t.Fatalf("StaleInterests after recompute: %v", err)
	}
	if slices.Contains(stale, buyer) {
		t.Errorf("stale accounts = %v, want the buyer gone once its slots are current", stale)
	}
}

// signalWeights is what the service hands the repo: catalogapi.InteractionWeight narrowed to the
// types personalisation may average. Built from the published map rather than typed out, so a
// retuned weight cannot leave the tests measuring a scale nothing uses.
func signalWeights() map[string]float64 {
	out := make(map[string]float64, len(catalogapi.PositiveInteractionTypes))
	for _, t := range catalogapi.PositiveInteractionTypes {
		out[t] = catalogapi.InteractionWeight[t]
	}
	return out
}
