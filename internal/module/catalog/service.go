// Package catalog implements catalogapi.Service.
package catalog

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"shopnexus/internal/infra/cache"
	accountapi "shopnexus/internal/module/account/api"
	catalogapi "shopnexus/internal/module/catalog/api"
	"shopnexus/internal/module/catalog/domain"
	"shopnexus/internal/module/catalog/port"
	"shopnexus/internal/shared/id"
)

// listingCacheTTL is how long an enriched listing stays cached.
const listingCacheTTL = 5 * time.Minute

type Service struct {
	repo     port.Repository
	accounts accountapi.Service
	cache    cache.Client
	log      *slog.Logger
}

func NewService(repo port.Repository, accounts accountapi.Service, c cache.Client, log *slog.Logger) *Service {
	return &Service{repo: repo, accounts: accounts, cache: c, log: log}
}

var _ catalogapi.Service = (*Service)(nil)

func (s *Service) CreateListing(ctx context.Context, req catalogapi.CreateListingRequest) (catalogapi.Listing, error) {
	l, err := domain.NewListing(req.OwnerID.Int64(), req.Title, req.Price)
	if err != nil {
		return catalogapi.Listing{}, err
	}
	if err := s.repo.Save(ctx, &l); err != nil {
		return catalogapi.Listing{}, fmt.Errorf("save listing: %w", err)
	}
	return s.toAPIListing(ctx, l), nil
}

func (s *Service) GetListing(ctx context.Context, req catalogapi.GetListingRequest) (catalogapi.Listing, error) {
	// Keyed on the raw id, not the opaque form: the cache is internal, and this
	// keeps the key stable and cheap regardless of the cipher.
	key := "listing:" + strconv.FormatInt(req.ID.Int64(), 10)

	// Cache hit: return the enriched listing without touching the DB or account service.
	var cached catalogapi.Listing
	if err := s.cache.Get(ctx, key, &cached); err == nil {
		return cached, nil
	} else if !errors.Is(err, cache.ErrCacheMiss) {
		s.log.Warn("cache get failed", "key", key, "err", err)
	}

	l, err := s.repo.FindByID(ctx, req.ID.Int64())
	if err != nil {
		return catalogapi.Listing{}, fmt.Errorf("find listing: %w", err)
	}

	out := s.toAPIListing(ctx, l)
	if err := s.cache.Set(ctx, key, out, listingCacheTTL); err != nil {
		s.log.Warn("cache set failed", "key", key, "err", err)
	}
	return out, nil
}

func (s *Service) ListListings(ctx context.Context, req catalogapi.ListListingsRequest) ([]catalogapi.Listing, error) {
	limit := req.Limit
	if limit == 0 {
		limit = 20
	}
	rows, err := s.repo.List(ctx, limit, req.Offset)
	if err != nil {
		return nil, fmt.Errorf("list listings: %w", err)
	}
	out := make([]catalogapi.Listing, 0, len(rows))
	for _, l := range rows {
		out = append(out, s.toAPIListing(ctx, l))
	}
	return out, nil
}

func (s *Service) SetStock(ctx context.Context, req catalogapi.SetStockRequest) (catalogapi.Stock, error) {
	if err := s.repo.UpsertStock(ctx, req.ProductID.Int64(), req.Quantity); err != nil {
		return catalogapi.Stock{}, fmt.Errorf("upsert stock: %w", err)
	}
	return catalogapi.Stock{ProductID: req.ProductID, Quantity: req.Quantity}, nil
}

func (s *Service) GetStock(ctx context.Context, req catalogapi.GetStockRequest) (catalogapi.Stock, error) {
	qty, err := s.repo.FindStock(ctx, req.ProductID.Int64())
	if err != nil {
		return catalogapi.Stock{}, fmt.Errorf("find stock: %w", err)
	}
	return catalogapi.Stock{ProductID: req.ProductID, Quantity: qty}, nil
}

// toAPIListing maps domain -> api and enriches the seller via the account service (service-to-service).
func (s *Service) toAPIListing(ctx context.Context, l domain.Listing) catalogapi.Listing {
	ownerID := id.Of[id.Account](l.OwnerID)
	seller := catalogapi.Seller{ID: ownerID}
	// The public view, not the caller's own: a listing shows its seller's shop page, and
	// nothing an account keeps private has any business in a catalog response.
	p, err := s.accounts.GetPublicAccount(ctx, accountapi.GetPublicAccountRequest{ID: ownerID})
	if err != nil {
		s.log.Warn("enrich seller failed", "owner_id", l.OwnerID, "err", err)
	} else {
		seller.DisplayName = p.Name
	}
	return catalogapi.Listing{
		ID:     id.Of[id.Listing](l.ID),
		Title:  l.Title,
		Price:  l.Price,
		Status: l.Status,
		Seller: seller,
	}
}
