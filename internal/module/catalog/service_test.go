package catalog_test

import (
	"context"
	"log/slog"
	"testing"

	"shopnexus/internal/infra/cache"
	accountapi "shopnexus/internal/module/account/api"
	"shopnexus/internal/module/account/api/accounttest"
	accountdomain "shopnexus/internal/module/account/domain"
	"shopnexus/internal/module/catalog"
	catalogapi "shopnexus/internal/module/catalog/api"
	"shopnexus/internal/module/catalog/domain"
	"shopnexus/internal/shared/errx"
	"shopnexus/internal/shared/id"
	"shopnexus/internal/shared/id/idtest"
)

// Fixed keys: 1 = the product_spu, 7 = its owner, 9 = an owner with no profile.
const (
	spuID   = 1
	ownerID = 7
	ghostID = 9
)

func TestMain(m *testing.M) { idtest.Install(); m.Run() }

type fakeRepo struct {
	saved  *domain.Listing
	byID   map[int64]domain.Listing
	listed []domain.Listing
}

func (f *fakeRepo) Save(_ context.Context, l *domain.Listing) error {
	l.ID = spuID
	f.saved = l
	return nil
}
func (f *fakeRepo) FindByID(_ context.Context, lid int64) (domain.Listing, error) {
	l, ok := f.byID[lid]
	if !ok {
		return domain.Listing{}, domain.ErrListingNotFound
	}
	return l, nil
}
func (f *fakeRepo) List(_ context.Context, _, _ int) ([]domain.Listing, error) {
	return f.listed, nil
}
func (f *fakeRepo) UpsertStock(_ context.Context, _ int64, _ int64) error { return nil }
func (f *fakeRepo) FindStock(_ context.Context, _ int64) (int64, error)   { return 0, nil }

// fakeAccounts answers the one call catalog makes across the module boundary: the seller's
// public page. Everything else comes from the stub, which refuses it — a catalog test that
// starts reaching for private account data should fail loudly.
type fakeAccounts struct {
	accounttest.Stub
	accounts map[id.ID[id.Account]]accountapi.PublicAccount
}

func (f *fakeAccounts) GetPublicAccount(_ context.Context, req accountapi.GetPublicAccountRequest) (accountapi.PublicAccount, error) {
	a, ok := f.accounts[req.ID]
	if !ok {
		return accountapi.PublicAccount{}, accountdomain.ErrAccountNotFound
	}
	return a, nil
}

func TestCreateListing(t *testing.T) {
	svc := catalog.NewService(&fakeRepo{}, &fakeAccounts{}, cache.NewInMemoryClient(), slog.Default())
	got, err := svc.CreateListing(context.Background(), catalogapi.CreateListingRequest{OwnerID: id.Of[id.Account](ownerID), Title: "Bàn", Price: 500})
	if err != nil {
		t.Fatalf("CreateListing: %v", err)
	}
	if got.ID != id.Of[id.ProductSPU](spuID) || got.Status != domain.StatusActive {
		t.Fatalf("unexpected listing: %+v", got)
	}
}

func TestGetListing_EnrichesSeller(t *testing.T) {
	repo := &fakeRepo{byID: map[int64]domain.Listing{
		spuID: {ID: spuID, OwnerID: ownerID, Title: "Bàn", Price: 500, Status: domain.StatusActive},
	}}
	accounts := &fakeAccounts{accounts: map[id.ID[id.Account]]accountapi.PublicAccount{
		id.Of[id.Account](ownerID): {ID: id.Of[id.Account](ownerID), Name: "Alice"},
	}}
	svc := catalog.NewService(repo, accounts, cache.NewInMemoryClient(), slog.Default())

	got, err := svc.GetListing(context.Background(), catalogapi.GetListingRequest{ID: id.Of[id.ProductSPU](spuID)})
	if err != nil {
		t.Fatalf("GetListing: %v", err)
	}
	if got.Seller.DisplayName != "Alice" {
		t.Fatalf("seller = %+v, want Alice", got.Seller)
	}
}

func TestGetListing_NotFound(t *testing.T) {
	svc := catalog.NewService(&fakeRepo{byID: map[int64]domain.Listing{}}, &fakeAccounts{}, cache.NewInMemoryClient(), slog.Default())
	_, err := svc.GetListing(context.Background(), catalogapi.GetListingRequest{ID: id.Of[id.ProductSPU](404)})
	if status, _, _, ok := errx.Decompose(err); !ok || status != 404 {
		t.Fatalf("expected NotFound, got %v", err)
	}
}

// Design decision: if seller enrichment fails, still return the listing with an
// empty seller name instead of failing the whole request.
func TestGetListing_EnrichFailsButStillReturns(t *testing.T) {
	repo := &fakeRepo{byID: map[int64]domain.Listing{
		spuID: {ID: spuID, OwnerID: ghostID, Title: "Bàn", Price: 500, Status: domain.StatusActive},
	}}
	// empty fakeAccounts -> GetPublicAccount returns NotFound for ghostID
	svc := catalog.NewService(repo, &fakeAccounts{}, cache.NewInMemoryClient(), slog.Default())

	got, err := svc.GetListing(context.Background(), catalogapi.GetListingRequest{ID: id.Of[id.ProductSPU](spuID)})
	if err != nil {
		t.Fatalf("must not fail when enrichment fails, got %v", err)
	}
	if got.Seller.ID != id.Of[id.Account](ghostID) || got.Seller.DisplayName != "" {
		t.Fatalf("seller should keep ID but have empty DisplayName, got %+v", got.Seller)
	}
}

func TestGetListing_ServedFromCache(t *testing.T) {
	repo := &fakeRepo{byID: map[int64]domain.Listing{
		spuID: {ID: spuID, OwnerID: ownerID, Title: "Bàn", Price: 500, Status: domain.StatusActive},
	}}
	accounts := &fakeAccounts{accounts: map[id.ID[id.Account]]accountapi.PublicAccount{
		id.Of[id.Account](ownerID): {ID: id.Of[id.Account](ownerID), Name: "Alice"},
	}}
	svc := catalog.NewService(repo, accounts, cache.NewInMemoryClient(), slog.Default())

	// First call populates the cache.
	if _, err := svc.GetListing(context.Background(), catalogapi.GetListingRequest{ID: id.Of[id.ProductSPU](spuID)}); err != nil {
		t.Fatalf("first GetListing: %v", err)
	}
	// Remove it from the repo; a cache hit must still return the listing.
	delete(repo.byID, spuID)

	got, err := svc.GetListing(context.Background(), catalogapi.GetListingRequest{ID: id.Of[id.ProductSPU](spuID)})
	if err != nil {
		t.Fatalf("cached GetListing: %v", err)
	}
	if got.ID != id.Of[id.ProductSPU](spuID) || got.Seller.DisplayName != "Alice" {
		t.Fatalf("expected listing served from cache, got %+v", got)
	}
}

func TestListListings_ReturnsMapped(t *testing.T) {
	repo := &fakeRepo{listed: []domain.Listing{
		{ID: spuID, OwnerID: ownerID, Title: "Bàn", Price: 500, Status: domain.StatusActive},
		{ID: spuID + 1, OwnerID: ownerID, Title: "Ghế", Price: 300, Status: domain.StatusActive},
	}}
	accounts := &fakeAccounts{accounts: map[id.ID[id.Account]]accountapi.PublicAccount{
		id.Of[id.Account](ownerID): {ID: id.Of[id.Account](ownerID), Name: "Alice"},
	}}
	svc := catalog.NewService(repo, accounts, cache.NewInMemoryClient(), slog.Default())

	got, err := svc.ListListings(context.Background(), catalogapi.ListListingsRequest{})
	if err != nil {
		t.Fatalf("ListListings: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Seller.DisplayName != "Alice" {
		t.Fatalf("expected enriched seller, got %+v", got[0].Seller)
	}
}
