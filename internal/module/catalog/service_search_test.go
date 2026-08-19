package catalog_test

import (
	"context"
	"testing"

	catalogapi "shopnexus/internal/module/catalog/api"
	"shopnexus/internal/shared/id"
)

// A probe is both halves of one pass over the text. The sparse half used to be dropped on the
// floor, which is why the sparse index has never served a search.
func TestSearch_ProbeCarriesBothHalves(t *testing.T) {
	h := newHarnessWith("user", true)
	ctx := context.Background()
	publish(t, h, seedListingNamed(t, h, "Áo thun Uniqlo"))

	if _, err := h.svc.ListListings(ctx, catalogapi.ListListingsRequest{
		ViewerID: id.Of[id.Account](1), Query: "áo thun", Page: 1, Limit: 20,
	}); err != nil {
		t.Fatalf("ListListings: %v", err)
	}
	probe := h.repo.lastFilter.Terms
	if len(probe) == 0 {
		t.Fatal("the filter carried no term; a search must reach the adapter as at least one probe")
	}
	if probe[0].Probe == nil {
		t.Fatal("the first term is not a probe")
	}
	if len(probe[0].Probe.Dense) == 0 {
		t.Error("probe has no dense half")
	}
	if len(probe[0].Probe.Sparse) == 0 {
		t.Error("probe has no sparse half — the sparse leg cannot rank without it")
	}
}

// A misspelled, no-diacritic query still reaches the adapter as a probe: the knowledge base is
// assembled from it and the search runs, whatever the model then makes of it.
func TestSearch_GarbledQueryStillSearches(t *testing.T) {
	h := newHarnessWith("user", true)
	ctx := context.Background()
	h.seedCategory(t, "Áo nam")
	h.seedTag(t, "uniqlo")
	publish(t, h, seedListingNamed(t, h, "Áo thun Uniqlo nam"))

	if _, err := h.svc.ListListings(ctx, catalogapi.ListListingsRequest{
		Query: "ao thun unilo", Page: 1, Limit: 20,
	}); err != nil {
		t.Fatalf("ListListings: %v", err)
	}
	if len(h.repo.lastFilter.Terms) == 0 {
		t.Error("no term reached the adapter")
	}
}
