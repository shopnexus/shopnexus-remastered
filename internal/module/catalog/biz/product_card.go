package catalogbiz

import (
	"errors"
	"fmt"
	"log/slog"

	restate "github.com/restatedev/sdk-go"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"
	"github.com/samber/lo"
	"github.com/samber/lo/mutable"

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
	"shopnexus-server/internal/shared/paginate"
	"shopnexus-server/internal/shared/validator"
)

func (b *CatalogHandler) buildProductCards(
	ctx restate.Context,
	spuIDs []uuid.UUID,
	accountID uuid.NullUUID,
) (map[uuid.UUID]*catalogmodel.ProductCard, error) {
	var zero map[uuid.UUID]*catalogmodel.ProductCard
	var productMap = make(map[uuid.UUID]*catalogmodel.ProductCard)

	listSpu, err := b.ListProductSpu(ctx, ListProductSpuParams{
		ID: spuIDs,
	})
	if err != nil {
		return zero, fmt.Errorf("list product spus: %w", err)
	}
	spus := listSpu.Data
	spuMap := lo.KeyBy(spus, func(spu catalogmodel.ProductSpu) uuid.UUID { return spu.ID })

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

	// map[spuID]FeaturedSKU
	featuredMap := lo.KeyBy(featuredSkus, func(row catalogmodel.ProductSku) uuid.UUID { return row.SpuID })

	// Get stock (taken/sold) for featured SKUs
	featuredStocks, err := b.inventory.ListStock(ctx, inventorybiz.ListStockParams{
		RefType: []inventorydb.InventoryStockRefType{inventorydb.InventoryStockRefTypeProductSku},
		RefID:   featuredIDs,
	})
	if err != nil {
		return zero, fmt.Errorf("list featured stock: %w", err)
	}
	// map[refID (skuID)] -> taken
	stockTakenMap := lo.KeyBy(featuredStocks.Data, func(s inventorydb.InventoryStock) uuid.UUID { return s.RefID })
	// map[spuID] -> taken (through featured sku)
	soldMap := make(map[uuid.UUID]int64)
	for spuID, featured := range featuredMap {
		if stock, ok := stockTakenMap[featured.ID]; ok {
			soldMap[spuID] = stock.Taken
		}
	}

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
		favoriteSet, _ = b.account.CheckFavorites(
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
			CategoryID:  spu.Category.ID,
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
			Sold:       soldMap[spu.ID],
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
	ctx restate.Context,
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
	ctx restate.Context,
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

// listProductCardFromDB is the Postgres-only path for browsing (no search query) or as fallback.
func (b *CatalogHandler) listProductCardFromDB(
	ctx restate.Context,
	params ListProductCardParams,
) (paginate.PaginateResult[catalogmodel.ProductCard], error) {
	var zero paginate.PaginateResult[catalogmodel.ProductCard]

	searchArg := catalogdb.SearchCountProductSpuParams{
		Limit:  params.Limit,
		Offset: params.Offset(),
	}
	if params.SellerID.Valid {
		searchArg.AccountID = []uuid.UUID{params.SellerID.UUID}
	}
	if len(params.CategoryID) > 0 {
		searchArg.CategoryID = params.CategoryID
	}

	// Tag pre-filter via join table
	if len(params.Tags) > 0 {
		tagRows, err := b.storage.Querier().
			SearchCountProductSpuByTags(ctx, catalogdb.SearchCountProductSpuByTagsParams{
				Tags:     params.Tags,
				TagCount: int32(len(params.Tags)),
				Limit:    params.Limit,
				Offset:   params.Offset(),
			})
		if err != nil {
			return zero, fmt.Errorf("db search by tags: %w", err)
		}
		if len(tagRows) == 0 {
			return paginate.PaginateResult[catalogmodel.ProductCard]{
				PageParams: params.Params,
				Data:       []catalogmodel.ProductCard{},
				Total:      null.IntFrom(0),
			}, nil
		}
		searchArg.ID = lo.Map(
			tagRows,
			func(r catalogdb.SearchCountProductSpuByTagsRow, _ int) uuid.UUID { return r.ID },
		)
	}

	// ILIKE fallback when search is set but vector search failed
	if params.Search.Valid {
		searchArg.Name = params.Search
		searchArg.Description = params.Search
		searchArg.Slug = params.Search
	}

	var total null.Int64

	rows, err := b.storage.Querier().SearchCountProductSpu(ctx, searchArg)
	if err != nil {
		return zero, fmt.Errorf("db search product spu: %w", err)
	}
	if len(rows) > 0 {
		total.SetValid(rows[0].TotalCount)
	}

	spuIDs := lo.Map(
		rows,
		func(r catalogdb.SearchCountProductSpuRow, _ int) uuid.UUID { return r.CatalogProductSpu.ID },
	)

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
		Total:      total,
	}, nil
}

type ListRecommendedProductCardParams struct {
	Account accountmodel.AuthenticatedAccount `validate:"omitempty"`
	Limit   int                               `validate:"omitempty,min=1,max=100"`
}

// ListRecommendedProductCard returns personalized product card recommendations for the authenticated user.
func (b *CatalogHandler) ListRecommendedProductCard(
	ctx restate.Context,
	params ListRecommendedProductCardParams,
) ([]catalogmodel.ProductCard, error) {
	var zero []catalogmodel.ProductCard
	var rcmProducts []catalogmodel.ProductRecommend
	var err error

	if err := validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate list recommended: %w", err)
	}

	// Get current feed offset
	var feedOffset int64 = 0
	if err = b.cache.Get(
		ctx,
		fmt.Sprintf(catalogmodel.CacheKeyRecommendOffset, params.Account.ID),
		&feedOffset,
	); err != nil && !errors.Is(err, cache.ErrCacheMiss) {
		b.logger.Error("failed to get feed offset for account",
			slog.String("account_id", params.Account.ID.String()),
			slog.Any("error", err),
		)
	}
	// Retrieve all recommended products from cache
	if err := b.cache.ZRevRangeByScore(
		ctx,
		fmt.Sprintf(catalogmodel.CacheKeyRecommendProduct, params.Account.ID),
		&rcmProducts,
		cache.ZRangeOptions{
			Offset: null.IntFrom(feedOffset),
			Limit:  null.IntFrom(int64(params.Limit)),
		},
	); err != nil {
		return zero, fmt.Errorf("get recommended products: %w", err)
	}
	feedOffset += int64(len(rcmProducts))

	// if current feed offset is exceeding the size or there is no recommendation in cache, refresh the feed
	if feedOffset >= catalogmodel.CacheRecommendSize || len(rcmProducts) == 0 {
		recommendations, err := b.GetRecommendations(ctx, GetRecommendationsParams{
			Account: params.Account,
			Limit:   catalogmodel.CacheRecommendSize,
		})
		if err != nil {
			b.logger.Error("failed to get recommendations for account",
				slog.String("account_id", params.Account.ID.String()),
				slog.Any("error", err),
			)
		}
		// Reset feed offset
		feedOffset = 0

		// Remove all old recommendations
		if err = b.cache.Delete(
			ctx,
			fmt.Sprintf(catalogmodel.CacheKeyRecommendProduct, params.Account.ID),
		); err != nil {
			b.logger.Error(
				"failed to reset feed offset for account",
				slog.String("account_id", params.Account.ID.String()),
				slog.Any("error", err),
			)
		}

		// Adding new feed
		for _, p := range recommendations {
			if err = b.cache.ZAdd(
				ctx,
				fmt.Sprintf(catalogmodel.CacheKeyRecommendProduct, params.Account.ID),
				p,
				float64(p.Score),
			); err != nil {
				return zero, fmt.Errorf("cache recommended product: %w", err)
			}
		}
	}

	// Update feed offset in cache
	if err = b.cache.Set(
		ctx,
		fmt.Sprintf(catalogmodel.CacheKeyRecommendOffset, params.Account.ID),
		feedOffset,
		0,
	); err != nil {
		b.logger.Error(
			"failed to update feed offset for account",
			slog.String("account_id", params.Account.ID.String()),
			slog.Any("error", err),
		)
	}

	// Amount of most sold products to fill the recommendations
	amount := int32(params.Limit - len(rcmProducts))
	if amount > 0 {
		mostSolds, err := b.inventory.ListMostTakenSku(ctx, inventorybiz.ListMostTakenSkuParams{
			Params: paginate.Params{
				Limit: null.Int32From(amount * 100),
			},
			RefType: inventorydb.InventoryStockRefTypeProductSku,
		})
		if err != nil {
			return zero, fmt.Errorf("list most taken sku: %w", err)
		}
		// Take random amount of shuffled most sold products
		mutable.Shuffle(mostSolds)
		if int32(len(mostSolds)) > amount {
			mostSolds = mostSolds[:amount]
		}

		skuIDs := lo.Map(mostSolds, func(p inventorydb.InventoryStock, _ int) uuid.UUID { return p.RefID })
		skus, err := b.storage.Querier().ListProductSku(ctx, catalogdb.ListProductSkuParams{
			Id: skuIDs,
		})
		if err != nil {
			return zero, fmt.Errorf("db list product sku: %w", err)
		}

		uniqueSpuIDs := lo.UniqMap(skus.Data, func(s catalogdb.CatalogProductSku, _ int) uuid.UUID { return s.SpuID })
		rcmProducts = append(
			rcmProducts,
			lo.Map(uniqueSpuIDs, func(spuID uuid.UUID, _ int) catalogmodel.ProductRecommend {
				return catalogmodel.ProductRecommend{
					ID:    spuID,
					Score: 0, // most sold has score 0
				}
			})...)
	}

	productCardMap, err := b.buildProductCards(
		ctx,
		lo.Map(rcmProducts, func(p catalogmodel.ProductRecommend, _ int) uuid.UUID { return p.ID }),
		uuid.NullUUID{UUID: params.Account.ID, Valid: true},
	)
	if err != nil {
		return zero, fmt.Errorf("build product cards: %w", err)
	}

	products := []catalogmodel.ProductCard{}
	for _, rcm := range rcmProducts {
		if productCardMap[rcm.ID] != nil {
			products = append(products, *productCardMap[rcm.ID])
		}
	}

	return products, nil
}
