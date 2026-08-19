package catalog_test

import (
	"context"
	"slices"
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

// The model's answer becomes signals, and the shopper's own words stay in the mix underneath —
// so a model that misreads the query narrows the ranking rather than replacing it.
func TestUnderstand_SignalsPlusTheRawQuery(t *testing.T) {
	h := newHarnessWith("user", true)
	ctx := context.Background()
	h.seedCategory(t, "Áo nam")
	publish(t, h, seedListingNamed(t, h, "Áo thun Uniqlo nam"))
	h.models.answer = `{
	  "boosts": [
	    {"attr": "probes", "value": ["áo thun Uniqlo nam"]},
	    {"attr": "category", "value": ["Áo nam"]},
	    {"attr": "price", "value": [{"lt": 500000}]}
	  ],
	  "demotes": [{"attr": "probes", "value": ["áo khoác"]}],
	  "understood": "áo thun nam Uniqlo, dưới 500k"
	}`

	page, err := h.svc.ListListings(ctx, catalogapi.ListListingsRequest{
		Query: "ao thun unilo", Page: 1, Limit: 20,
	})
	if err != nil {
		t.Fatalf("ListListings: %v", err)
	}
	if page.Understood != "áo thun nam Uniqlo, dưới 500k" {
		t.Errorf("understood = %q, want the model's own sentence", page.Understood)
	}
	terms := h.repo.lastFilter.Terms
	var probes, predicates, negative int
	for _, term := range terms {
		switch {
		case term.Probe != nil:
			probes++
		case term.Predicate != nil:
			predicates++
		}
		if term.Weight < 0 {
			negative++
		}
	}
	// The model's probe, its demote, and the raw query the server always appends.
	if probes != 3 {
		t.Errorf("probe terms = %d, want the model's two plus the raw query", probes)
	}
	if predicates != 2 {
		t.Errorf("predicate terms = %d, want the category and the price bound", predicates)
	}
	if negative != 1 {
		t.Errorf("negative terms = %d, want the demote and nothing else", negative)
	}
	if !slices.Contains(page.Probes, "ao thun unilo") {
		t.Errorf("probes = %v, want the shopper's own words among them", page.Probes)
	}
}

// A model answering nothing usable leaves the search exactly as it would have been without one.
// That is the property that makes base retrieval and smart search one code path.
func TestUnderstand_EmptyAnswerIsBaseRetrieval(t *testing.T) {
	h := newHarnessWith("user", true)
	ctx := context.Background()
	publish(t, h, seedListingNamed(t, h, "Áo thun Uniqlo nam"))
	h.models.answer = `{"boosts": [], "demotes": [], "understood": ""}`

	page, err := h.svc.ListListings(ctx, catalogapi.ListListingsRequest{
		Query: "áo thun", Page: 1, Limit: 20,
	})
	if err != nil {
		t.Fatalf("ListListings: %v", err)
	}
	terms := h.repo.lastFilter.Terms
	if len(terms) != 1 || terms[0].Probe == nil {
		t.Fatalf("terms = %+v, want the raw query alone", terms)
	}
	if !slices.Equal(page.Probes, []string{"áo thun"}) {
		t.Errorf("probes = %v, want the shopper's own words alone", page.Probes)
	}
}

// A browse with no query answers the zero values, not null: the contract says an array, and a
// client that has to nil-check a required field is one the contract lied to.
func TestListListings_BrowseAnswersEmptySearchFields(t *testing.T) {
	h := newHarnessWith("user", true)
	publish(t, h, seedListingNamed(t, h, "Áo thun Uniqlo nam"))

	page, err := h.svc.ListListings(context.Background(), catalogapi.ListListingsRequest{Page: 1, Limit: 20})
	if err != nil {
		t.Fatalf("ListListings: %v", err)
	}
	if page.Understood != "" || page.Probes == nil || len(page.Probes) != 0 {
		t.Errorf("understood = %q, probes = %#v; want empty and non-nil", page.Understood, page.Probes)
	}
}

// A model that puts the shopper's own words in `demotes` must not take base retrieval away with
// them: the raw query is appended anyway, and the page is still ranked towards it rather than
// away from it. Without this the statement carries one negative probe of the query and
// `ORDER BY score DESC` answers whatever is least like what was typed.
func TestUnderstand_ADemotedQueryDoesNotSwallowTheRawProbe(t *testing.T) {
	h := newHarnessWith("user", true)
	ctx := context.Background()
	publish(t, h, seedListingNamed(t, h, "Áo thun Uniqlo nam"))
	h.models.answer = `{
	  "boosts": [],
	  "demotes": [{"attr": "probes", "value": ["áo thun"]}],
	  "understood": "áo thun"
	}`

	page, err := h.svc.ListListings(ctx, catalogapi.ListListingsRequest{
		Query: "áo thun", Page: 1, Limit: 20,
	})
	if err != nil {
		t.Fatalf("ListListings: %v", err)
	}
	var positive int
	for _, term := range h.repo.lastFilter.Terms {
		if term.Probe != nil && term.Weight > 0 {
			positive++
		}
	}
	if positive != 1 {
		t.Fatalf("positive probes = %d, want the raw query appended despite the demote", positive)
	}
	if !slices.Equal(page.Probes, []string{"áo thun"}) {
		t.Errorf("probes = %v, want the shopper's own words as what was searched for", page.Probes)
	}
}
