package catalogapi

import (
	"strings"

	"shopnexus/internal/shared/id"
)

// The published slug is the name's slug with the listing's id body glued onto the end:
//
//	ao-thun-tay-lo-59p7ay3se5h95
//
// The text in front is for the person reading the link; the segment behind the last hyphen
// is what resolves it. Two things make that split exact. slugify collapses every run of
// non-alphanumerics into a single hyphen, so a name can never produce the separator twice
// in a row; and the id is always appended last, so the final segment is the id even when
// the name's own last word looks like one.
//
// The id body travels without its `lst_` prefix, which keeps the underscore out — the rule
// the wire contract uses to tell an opaque id from a slug, so a route can accept both.
//
// It lives in the api package because both sides of that rule belong together and both are
// published: the service composes, the gateway resolves.

// PublicSlug builds the slug a link carries. nameSlug is the frozen `listing.slug` column —
// the name as it was at creation, so renaming a listing does not rewrite links already shared.
func PublicSlug(listingID id.ID[id.Listing], nameSlug string) string {
	body := strings.TrimPrefix(listingID.String(), listingIDPrefix())
	if nameSlug == "" {
		// A name of only punctuation slugifies to nothing. Gluing anyway would leave a
		// leading hyphen, which is a slug shape nothing else in the catalog produces.
		return body
	}
	return nameSlug + "-" + body
}

// ParseListingRef resolves either form of a listing reference: the opaque id a write
// addresses the listing by, or the public slug a link carries. An id always has an
// underscore after its prefix and a slug never has one, so the two cannot be confused.
func ParseListingRef(ref string) (id.ID[id.Listing], error) {
	if strings.Contains(ref, "_") {
		return id.Parse[id.Listing](ref)
	}
	// The id is the last segment. id.Parse rejects a body that is not 13 characters of the
	// alphabet, so a slug with no id on the end fails here rather than resolving to a
	// listing nobody asked for.
	return id.Parse[id.Listing](listingIDPrefix() + ref[strings.LastIndexByte(ref, '-')+1:])
}

func listingIDPrefix() string { return id.Prefix[id.Listing]() + "_" }
