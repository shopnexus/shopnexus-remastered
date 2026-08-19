// Package observabilityapi is the published contract of the observability service.
//
// Unlike every other module's api package, nothing here is a gateway route: observability
// has no HTTP surface and no OpenAPI fragment, because nobody outside this backend has ever
// asked it a question — it is driven by the middleware, the sampler and the bus. This
// interface exists for exactly one in-process caller so far, catalog's `sort=trending`, which
// is also why it stayed unwritten as long as it did: an interface with one caller is a bet on
// a second one, and this is the day that bet paid off.
package observabilityapi

import (
	"context"

	"shopnexus/internal/shared/id"
)

type Service interface {
	// TopPopular answers the platform's most-engaged listings, most popular first — the raw
	// material for catalog's `sort=trending` and for a personalised feed's own fallback when
	// an account has no interests to rank against yet. Best-effort from the caller's side:
	// this module is not on the critical path of a browse, so a caller that cannot reach it
	// degrades to newest rather than failing the request.
	TopPopular(ctx context.Context, req TopPopularRequest) ([]id.ID[id.Listing], error)
}

// TopPopularRequest pages the ranking. Offset plus Limit, not a cursor: the ranking a page
// asks for changes on its own clock as new interactions land, so two different pages have
// never been a promise this read makes — the caller already knows this from
// ListingPage.Meta.TotalCount being nil for both `sort=trending` and `sort=recommended`.
type TopPopularRequest struct {
	Offset int `validate:"gte=0"`
	Limit  int `validate:"required,min=1,max=100"`
}
