package catalogapi_test

import (
	"testing"

	catalogapi "shopnexus/internal/module/catalog/api"
	"shopnexus/internal/shared/id"
	"shopnexus/internal/shared/id/idtest"
)

func TestMain(m *testing.M) {
	idtest.Install()
	m.Run()
}

// The published slug is the name's slug with the listing's id glued on, so the text a buyer
// reads is a name and the part that resolves is an id.
func TestPublicSlug_GluesTheIDOntoTheName(t *testing.T) {
	listingID := id.Of[id.Listing](8842)
	got := catalogapi.PublicSlug(listingID, "ao-thun-tay-lo")

	want := "ao-thun-tay-lo-" + listingID.String()[len("lst_"):]
	if got != want {
		t.Fatalf("PublicSlug = %q, want %q", got, want)
	}
}

// A listing whose name slugified to nothing still needs a resolvable ref, so the id stands
// alone rather than producing a leading hyphen.
func TestPublicSlug_NamelessListingIsTheIDAlone(t *testing.T) {
	listingID := id.Of[id.Listing](8842)
	got := catalogapi.PublicSlug(listingID, "")

	want := listingID.String()[len("lst_"):]
	if got != want {
		t.Fatalf("PublicSlug = %q, want %q", got, want)
	}
}

// Both forms address the same listing: the opaque id a write uses, and the slug a link
// carries. Told apart by the underscore, which a slug never contains.
func TestParseListingRef_AcceptsBothForms(t *testing.T) {
	listingID := id.Of[id.Listing](8842)

	for _, ref := range []string{
		listingID.String(),
		catalogapi.PublicSlug(listingID, "ao-thun-tay-lo"),
		catalogapi.PublicSlug(listingID, ""),
	} {
		got, err := catalogapi.ParseListingRef(ref)
		if err != nil {
			t.Fatalf("ParseListingRef(%q): %v", ref, err)
		}
		if got != listingID {
			t.Fatalf("ParseListingRef(%q) = %v, want %v", ref, got, listingID)
		}
	}
}

// The id is always the last segment, so a name whose own last word is a well-formed id body
// still resolves to the listing rather than to whatever that word decodes to.
func TestParseListingRef_NameEndingInAnIDBodyResolvesToTheListing(t *testing.T) {
	listingID := id.Of[id.Listing](8842)
	decoy := id.Of[id.Listing](7).String()[len("lst_"):]

	got, err := catalogapi.ParseListingRef(catalogapi.PublicSlug(listingID, "phone-"+decoy))
	if err != nil {
		t.Fatalf("ParseListingRef: %v", err)
	}
	if got != listingID {
		t.Fatalf("ParseListingRef = %v, want %v", got, listingID)
	}
}

func TestParseListingRef_RejectsGarbage(t *testing.T) {
	for _, ref := range []string{
		"",
		"ao-thun-tay-lo",           // no id segment at all
		"ao-thun-tay-lo-notanid",   // last segment is not an id body
		"acc_59p7ay3se5h95",        // an id of another kind
		"ao-thun-lst_59p7ay3se5h9", // a prefix glued in the middle
	} {
		if _, err := catalogapi.ParseListingRef(ref); err == nil {
			t.Fatalf("ParseListingRef(%q) = nil error, want one", ref)
		}
	}
}
