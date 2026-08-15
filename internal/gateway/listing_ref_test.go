package gateway_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	openapi "shopnexus/api"
	catalogapi "shopnexus/internal/module/catalog/api"
	"shopnexus/internal/module/catalog/api/catalogtest"
	"shopnexus/internal/shared/id"
)

// recordingCat answers GetListing by reporting which listing the route resolved to, which is
// the whole question here: the path segment is a slug and the service takes an id.
type recordingCat struct {
	catalogtest.Stub
	got id.ID[id.Listing]
}

func (c *recordingCat) GetListing(_ context.Context, req catalogapi.GetListingRequest) (catalogapi.ListingDetail, error) {
	c.got = req.ID
	return catalogapi.ListingDetail{ID: req.ID}, nil
}

// A link carries the public slug, so the read route has to resolve it — the id it names is on
// the end of it. The opaque id keeps working, because an order item and a ticket reference a
// listing by id and have no slug to build a link from.
func TestRouter_GetListingAcceptsBothARefAndAnID(t *testing.T) {
	listingID := id.Of[id.Listing](8842)

	for _, ref := range []string{
		listingID.String(),
		catalogapi.PublicSlug(listingID, "ao-thun-tay-lo"),
	} {
		cat := &recordingCat{}
		r, _, _ := newRouterWithCatalog(cat)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, openapi.BasePath+"/listings/"+ref, nil))

		if rec.Code != http.StatusOK {
			t.Fatalf("GET /listings/%s: status = %d, want 200 (%s)", ref, rec.Code, rec.Body)
		}
		if cat.got != listingID {
			t.Fatalf("GET /listings/%s resolved to %v, want %v", ref, cat.got, listingID)
		}
	}
}

// A write addresses the listing, never its public slug: the id is the only stable handle, and
// accepting a slug on a mutation would let a stale link edit whatever now sits behind it.
func TestRouter_DeleteListingRefusesASlug(t *testing.T) {
	cat := &recordingCat{}
	r, tm, sessions := newRouterWithCatalog(cat)
	rec := httptest.NewRecorder()

	ref := catalogapi.PublicSlug(id.Of[id.Listing](8842), "ao-thun-tay-lo")
	req := httptest.NewRequest(http.MethodDelete, openapi.BasePath+"/listings/"+ref, nil)
	req.Header.Set("Authorization", "Bearer "+bearer(t, tm, sessions, 1))
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}
