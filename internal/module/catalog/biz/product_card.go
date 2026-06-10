package catalogbiz

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	rand2 "math/rand/v2"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"
	"github.com/samber/lo"

	"shopnexus-server/internal/infras/cache"
	accountbiz "shopnexus-server/internal/module/account/biz"
	accountmodel "shopnexus-server/internal/module/account/model"
	catalogdb "shopnexus-server/internal/module/catalog/db/sqlc"
	catalogmodel "shopnexus-server/internal/module/catalog/model"
	commonbiz "shopnexus-server/internal/module/common/biz"
	commondb "shopnexus-server/internal/module/common/db/sqlc"
	inventorybiz "shopnexus-server/internal/module/inventory/biz"
	inventorydb "shopnexus-server/internal/module/inventory/db/sqlc"
	promotionbiz "shopnexus-server/internal/module/promotion/biz"
	promotionmodel "shopnexus-server/internal/module/promotion/model"
	"shopnexus-server/internal/shared/paginate"
	"shopnexus-server/internal/shared/validator"
)

func (b *CatalogHandler) buildProductCards(
	ctx context.Context,
	spuIDs []uuid.UUID,
	accountID uuid.NullUUID,
) (map[uuid.UUID]*catalogmodel.ProductCard, error) {
	var zero map[uuid.UUID]*catalogmodel.ProductCard
	var productMap = make(map[uuid.UUID]*catalogmodel.ProductCard)

	listSpu, err := b.storage.Querier().ListProductSpu(ctx, catalogdb.ListProductSpuParams{
		Id: spuIDs,
	})
	if err != nil {
		return zero, fmt.Errorf("list product spus: %w", err)
	}
	spus := listSpu.Data
	spuMap := lo.SliceToMap(spus, func(spu catalogdb.CatalogProductSpu) (uuid.UUID, promotionmodel.PromoSpu) {
		return spu.ID, promotionmodel.PromoSpu{ID: spu.ID, CategoryID: spu.CategoryID}
	})

	// Get featured SKUs for each spu
	var featuredIDs []uuid.UUID
	for _, spu := range spus {
		if spu.FeaturedSkuID.Valid {
			featuredIDs = append(featuredIDs, spu.FeaturedSkuID.UUID)
		}
	}

	// Get featured SKUs
	featuredSkus, err := b.ListProductSku(ctx, ListProductSkuParams{
		ID: featuredIDs,
	})
	if err != nil {
		return zero, fmt.Errorf("list product skus: %w", err)
	}

	// map[spuID]FeaturedSKU — Taken (sold count) already hydrated, no extra stock query
	featuredMap := lo.KeyBy(featuredSkus, func(row catalogmodel.ProductSku) uuid.UUID { return row.SpuID })

	// Build price request inputs for featured SKUs
	requestPrices := make([]catalogmodel.RequestOrderPrice, 0, len(featuredSkus))
	for _, sku := range featuredSkus {
		requestPrices = append(requestPrices, catalogmodel.RequestOrderPrice{
			SkuID:     sku.ID,
			SpuID:     sku.SpuID,
			UnitPrice: sku.Price,
			Quantity:  1,
			ShipCost:  0,
		})
	}

	priceMap, err := b.promotion.CalculatePromotedPrices(
		ctx,
		promotionbiz.CalculatePromotedPricesParams{Prices: requestPrices, SpuMap: spuMap},
	)
	if err != nil {
		return zero, fmt.Errorf("calculate promoted prices: %w", err)
	}

	ratingMap, err := b.getRatingsMap(ctx, spuIDs)
	if err != nil {
		return zero, fmt.Errorf("db list rating: %w", err)
	}

	// Get first image of the product
	resourcesMap, err := b.common.GetResources(ctx, commonbiz.GetResourcesParams{
		RefType: commondb.CommonResourceRefTypeProductSpu,
		RefIDs:  spuIDs,
	})
	if err != nil {
		return zero, fmt.Errorf("get product resources: %w", err)
	}

	// Map promotion codes to ProductCardPromo per SPU
	promoCardsMap := make(map[uuid.UUID][]catalogmodel.ProductCardPromo)
	for _, featured := range featuredSkus {
		price := priceMap[featured.ID]
		if price == nil || len(price.PromotionCodes) == 0 {
			continue
		}

		promoCardsMap[featured.SpuID] = lo.Map(
			price.PromotionCodes,
			func(code string, _ int) catalogmodel.ProductCardPromo {
				return catalogmodel.ProductCardPromo{
					Title: code,
				}
			},
		)
	}

	// Check favorites for authenticated user
	var favoriteSet map[uuid.UUID]bool
	if accountID.Valid {
		favoriteSet, _ = b.account.Guaranteed().CheckFavorites(
			ctx,
			accountbiz.CheckFavoritesParams{AccountID: accountID.UUID, SpuIDs: spuIDs},
		)
	}

	for _, spu := range spus {
		featured := featuredMap[spu.ID]
		rating := ratingMap[spu.ID]
		resources := resourcesMap[spu.ID]

		priceValue := featured.Price
		originalPrice := featured.Price
		if priceInfo := priceMap[featured.ID]; priceInfo != nil {
			originalPrice = priceInfo.Request.UnitPrice
			if priceInfo.ProductCost != 0 {
				priceValue = priceInfo.ProductCost
			}
		}

		productMap[spu.ID] = &catalogmodel.ProductCard{
			ID:          spu.ID,
			Slug:        spu.Slug,
			SellerID:    spu.AccountID,
			CategoryID:  spu.CategoryID,
			Name:        spu.Name,
			Description: spu.Description,
			IsEnabled:   spu.IsEnabled,
			Currency:    spu.Currency,
			DateCreated: spu.DateCreated,
			DateUpdated: spu.DateUpdated,

			Promotions:    promoCardsMap[spu.ID],
			Price:         priceValue,
			OriginalPrice: originalPrice,
			Rating: catalogmodel.Rating{
				Score: float32(rating.Score),
				Total: int(rating.Count),
			},
			IsFavorite: favoriteSet[spu.ID],
			Resources:  resources,
			Sold:       featured.Taken,
		}
	}

	return productMap, nil
}

type GetProductCardParams struct {
	AccountID uuid.NullUUID `validate:"omitnil"` // optional, for is_favorite
	SpuID     uuid.UUID     `validate:"required"`
}

// GetProductCard returns a single product card by SPU ID.
func (b *CatalogHandler) GetProductCard(
	ctx context.Context,
	params GetProductCardParams,
) (*catalogmodel.ProductCard, error) {
	if err := validator.Validate(params); err != nil {
		return nil, fmt.Errorf("validate get product card: %w", err)
	}

	productCardMap, err := b.buildProductCards(ctx, []uuid.UUID{params.SpuID}, params.AccountID)
	if err != nil {
		return nil, fmt.Errorf("build product card: %w", err)
	}

	card, ok := productCardMap[params.SpuID]
	if !ok || card == nil {
		return nil, catalogmodel.ErrProductNotFound
	}

	return card, nil
}

type ListProductCardParams struct {
	paginate.Params

	AccountID       uuid.NullUUID `validate:"omitnil"` // optional, for is_favorite
	SellerID        uuid.NullUUID `validate:"omitnil"`
	CategoryID      []uuid.UUID   `validate:"omitempty"`
	Tags            []string      `validate:"omitempty"`
	Search          null.String   `validate:"omitnil,min=1,max=100"`
	PriceMin        null.Float    `validate:"omitnil,gte=0"`
	PriceMax        null.Float    `validate:"omitnil,gte=0"`
	DateCreatedFrom null.Int      `validate:"omitnil,gte=0"`
	DateCreatedTo   null.Int      `validate:"omitnil,gte=0"`
}

// ListProductCard returns paginated product cards with optional search and vendor filter.
// When a search query is provided, pgvector handles both semantic ranking and scalar
// filtering in a single SQL query. When browsing (no search), Postgres handles filtering
// and pagination.
func (b *CatalogHandler) ListProductCard(
	ctx context.Context,
	params ListProductCardParams,
) (paginate.PaginateResult[catalogmodel.ProductCard], error) {
	var zero paginate.PaginateResult[catalogmodel.ProductCard]

	if err := validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate list product card: %w", err)
	}

	var spuIDs []uuid.UUID
	var total int64

	if params.Search.Valid {
		// --- Search path: pgvector handles ranking + filtering ---
		searchParams := SearchParams{
			Params:          params.Params,
			Query:           params.Search.String,
			Tags:            params.Tags,
			IsEnabled:       null.BoolFrom(true),
			PriceMin:        params.PriceMin,
			PriceMax:        params.PriceMax,
			DateCreatedFrom: params.DateCreatedFrom,
			DateCreatedTo:   params.DateCreatedTo,
		}
		if params.SellerID.Valid {
			searchParams.AccountID = []uuid.UUID{params.SellerID.UUID}
		}
		if len(params.CategoryID) > 0 {
			searchParams.CategoryID = params.CategoryID
		}

		results, err := b.Search(ctx, searchParams)
		if err != nil {
			b.logger.Error("failed to search products",
				slog.String("query", params.Search.String),
				slog.Any("error", err),
			)
			// Fallback to Postgres ILIKE search
			return b.listProductCardFromDB(ctx, params)
		}

		spuIDs = lo.Map(results.Data, func(p catalogmodel.ProductRecommend, _ int) uuid.UUID { return p.ID })
		total = results.Total.Int64 // real count from the hybrid-search count query
	} else {
		// --- Browse path: Postgres handles filtering + pagination ---
		return b.listProductCardFromDB(ctx, params)
	}

	if len(spuIDs) == 0 {
		return paginate.PaginateResult[catalogmodel.ProductCard]{
			PageParams: params.Params,
			Data:       []catalogmodel.ProductCard{},
			Total:      null.IntFrom(0),
		}, nil
	}

	// Enrich ranked IDs into full product cards
	productCardMap, err := b.buildProductCards(ctx, spuIDs, params.AccountID)
	if err != nil {
		return zero, fmt.Errorf("build product cards: %w", err)
	}

	products := make([]catalogmodel.ProductCard, 0, len(spuIDs))
	for _, id := range spuIDs {
		if card := productCardMap[id]; card != nil {
			products = append(products, *card)
		}
	}

	return paginate.PaginateResult[catalogmodel.ProductCard]{
		PageParams: params.Params,
		Data:       products,
		Total:      null.IntFrom(total),
	}, nil
}

// listProductCardFromDB is the Postgres-only path for browsing (no search query)
// or the vector-search fallback. Pagination + sort (offset or keyset cursor) run
// through the shared list runtime via ListProductCardBrowse; this layer only
// resolves the tag pre-filter and enriches the page into cards.
func (b *CatalogHandler) listProductCardFromDB(
	ctx context.Context,
	params ListProductCardParams,
) (paginate.PaginateResult[catalogmodel.ProductCard], error) {
	var zero paginate.PaginateResult[catalogmodel.ProductCard]

	browseArg := catalogdb.ListProductCardBrowseParams{
		Params: params.Params,
		Search: params.Search, // ILIKE fallback; unset on a normal browse
	}
	if params.SellerID.Valid {
		browseArg.AccountID = []uuid.UUID{params.SellerID.UUID}
	}
	if len(params.CategoryID) > 0 {
		browseArg.CategoryID = params.CategoryID
	}

	// Tag pre-filter via join table. Fetch ALL tag-matched ids (no limit) so the
	// browse query sorts + paginates over the whole matched set, not a slice.
	if len(params.Tags) > 0 {
		tagRows, err := b.storage.Querier().
			SearchCountProductSpuByTags(ctx, catalogdb.SearchCountProductSpuByTagsParams{
				Tags:     params.Tags,
				TagCount: int32(len(params.Tags)),
			})
		if err != nil {
			return zero, fmt.Errorf("db search by tags: %w", err)
		}
		if len(tagRows) == 0 {
			return zero, nil
		}
		browseArg.ID = lo.Map(
			tagRows,
			func(r catalogdb.SearchCountProductSpuByTagsRow, _ int) uuid.UUID { return r.ID },
		)
	}

	res, err := b.storage.Querier().ListProductCardBrowse(ctx, browseArg)
	if err != nil {
		return zero, fmt.Errorf("db list product card browse: %w", err)
	}

	spuIDs := lo.Map(res.Data, func(s catalogdb.CatalogProductSpu, _ int) uuid.UUID { return s.ID })
	productCardMap, err := b.buildProductCards(ctx, spuIDs, params.AccountID)
	if err != nil {
		return zero, fmt.Errorf("build product cards: %w", err)
	}

	products := make([]catalogmodel.ProductCard, 0, len(spuIDs))
	for _, id := range spuIDs {
		if card := productCardMap[id]; card != nil {
			products = append(products, *card)
		}
	}

	return paginate.PaginateResult[catalogmodel.ProductCard]{
		PageParams: res.PageParams,
		Data:       products,
		Total:      res.Total,
		NextCursor: res.NextCursor,
	}, nil
}

type ListRecommendedProductCardParams struct {
	Account accountmodel.AuthenticatedAccount `validate:"omitempty"`
	Limit   int                               `validate:"omitempty,min=1,max=100"`
}

// ListRecommendedProductCard returns personalized product card recommendations for the authenticated user.
func (b *CatalogHandler) ListRecommendedProductCard(
	ctx context.Context,
	params ListRecommendedProductCardParams,
) ([]catalogmodel.ProductCard, error) {
	var zero []catalogmodel.ProductCard

	if err := validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate list recommended: %w", err)
	}

	pool, err := b.getRecommendPool(ctx, params.Account)
	if err != nil {
		return zero, fmt.Errorf("recommend pool: %w", err)
	}

	// Shuffle the whole pool, then take a fresh window. FE dedups repeats across
	// infinite-scroll pages.
	rand2.Shuffle(len(pool), func(i, j int) {
		pool[i], pool[j] = pool[j], pool[i]
	})
	if params.Limit < len(pool) {
		pool = pool[:params.Limit]
	}

	cardMap, err := b.buildProductCards(
		ctx,
		pool,
		uuid.NullUUID{UUID: params.Account.ID, Valid: params.Account.ID != uuid.Nil},
	)
	if err != nil {
		return zero, fmt.Errorf("build product cards: %w", err)
	}

	cards := make([]catalogmodel.ProductCard, 0, len(pool))
	for _, id := range pool {
		if card := cardMap[id]; card != nil {
			cards = append(cards, *card)
		}
	}
	return cards, nil
}

// getRecommendPool returns up to CacheRecommendSize ranked SPU ids for the account:
// personalized (pgvector) first, trending backfill after. Cached in KV with a TTL
// and invalidated on interaction events (see AddInteractions).
func (b *CatalogHandler) getRecommendPool(
	ctx context.Context,
	account accountmodel.AuthenticatedAccount,
) ([]uuid.UUID, error) {
	key := fmt.Sprintf(catalogmodel.CacheKeyRecommendPool, account.ID)

	var pool []uuid.UUID
	if err := b.cache.Get(ctx, key, &pool); err == nil {
		return pool, nil
	} else if !errors.Is(err, cache.ErrCacheMiss) {
		b.logger.Error("get recommend pool",
			slog.String("account_id", account.ID.String()), slog.Any("error", err))
	}

	// Personalized recommendations (pgvector ANN). Guests have no interests.
	if account.ID != uuid.Nil {
		recs, err := b.GetRecommendations(ctx, GetRecommendationsParams{
			Account: account,
			Limit:   catalogmodel.CacheRecommendSize,
		})
		if err != nil {
			b.logger.Error("get recommendations",
				slog.String("account_id", account.ID.String()), slog.Any("error", err))
		}
		for _, r := range recs {
			pool = append(pool, r.ID)
		}
	}

	// Backfill with trending (most-sold) SPUs, skipping ones already picked.
	if len(pool) < catalogmodel.CacheRecommendSize {
		trending, err := b.trendingSpuIDs(ctx, catalogmodel.CacheRecommendSize)
		if err != nil {
			return nil, err
		}
		seen := lo.SliceToMap(pool, func(id uuid.UUID) (uuid.UUID, struct{}) {
			return id, struct{}{}
		})
		for _, id := range trending {
			if _, ok := seen[id]; ok {
				continue
			}
			pool = append(pool, id)
			if len(pool) >= catalogmodel.CacheRecommendSize {
				break
			}
		}
	}

	if err := b.cache.Set(ctx, key, pool, catalogmodel.RecommendPoolTTL); err != nil {
		b.logger.Error("set recommend pool",
			slog.String("account_id", account.ID.String()), slog.Any("error", err))
	}
	return pool, nil
}

// trendingSpuIDs returns unique SPU ids backing the most-taken (best-selling) SKUs.
func (b *CatalogHandler) trendingSpuIDs(ctx context.Context, limit int32) ([]uuid.UUID, error) {
	stocks, err := b.inventory.ListMostTakenSku(ctx, inventorybiz.ListMostTakenSkuParams{
		Params:  paginate.Params{Limit: null.Int32From(limit)},
		RefType: inventorydb.InventoryStockRefTypeProductSku,
	})
	if err != nil {
		return nil, fmt.Errorf("list most taken sku: %w", err)
	}

	skuIDs := lo.Map(stocks, func(s inventorydb.InventoryStock, _ int) uuid.UUID { return s.RefID })
	skus, err := b.storage.Querier().ListProductSku(ctx, catalogdb.ListProductSkuParams{Id: skuIDs})
	if err != nil {
		return nil, fmt.Errorf("db list product sku: %w", err)
	}
	return lo.UniqMap(skus.Data, func(s catalogdb.CatalogProductSku, _ int) uuid.UUID { return s.SpuID }), nil
}
