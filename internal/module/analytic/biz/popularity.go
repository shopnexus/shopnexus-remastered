package analyticbiz

import (
	"fmt"

	restate "github.com/restatedev/sdk-go"

	analyticdb "shopnexus-server/internal/module/analytic/db/sqlc"
	analyticmodel "shopnexus-server/internal/module/analytic/model"
	"shopnexus-server/internal/shared/paginate"

	"github.com/google/uuid"
)

// HandlePopularityEvent updates product popularity scores based on an interaction event.
func (b *AnalyticHandler) HandlePopularityEvent(ctx restate.Context, event analyticmodel.Interaction) error {
	if event.RefType != analyticmodel.InteractionRefTypeProduct {
		return nil
	}

	weight, ok := b.popularityWeights[event.EventType]
	if !ok {
		return nil
	}

	var viewCount, purchaseCount, favoriteCount, cartCount, reviewCount int64
	switch event.EventType {
	case analyticmodel.EventView, analyticmodel.EventViewBounce:
		viewCount = 1
	case analyticmodel.EventPurchase:
		purchaseCount = 1
	case analyticmodel.EventAddToFavorites:
		favoriteCount = 1
	case analyticmodel.EventAddToCart, analyticmodel.EventRemoveFromCart:
		cartCount = 1
	case analyticmodel.EventWriteReview, analyticmodel.EventRatingHigh, analyticmodel.EventRatingMedium,
		analyticmodel.EventRatingLow:
		reviewCount = 1
	}

	if _, err := b.storage.Querier().UpsertProductPopularity(ctx, analyticdb.UpsertProductPopularityParams{
		ID:            event.RefID,
		Score:         weight,
		ViewCount:     viewCount,
		PurchaseCount: purchaseCount,
		FavoriteCount: favoriteCount,
		CartCount:     cartCount,
		ReviewCount:   reviewCount,
	}); err != nil {
		return fmt.Errorf("upsert product popularity: %w", err)
	}

	return nil
}

// GetProductPopularity returns the popularity metrics for the given product SPU.
func (b *AnalyticHandler) GetProductPopularity(
	ctx restate.Context,
	spuID uuid.UUID,
) (analyticdb.AnalyticProductPopularity, error) {
	return b.storage.Querier().GetProductPopularityByID(ctx, spuID)
}

// ListTopProductPopularity returns the top products ranked by popularity score.
func (b *AnalyticHandler) ListTopProductPopularity(
	ctx restate.Context,
	params paginate.Params,
) ([]analyticdb.AnalyticProductPopularity, error) {
	params = params.Constrain()
	return b.storage.Querier().ListTopProductPopularity(ctx, analyticdb.ListTopProductPopularityParams{
		Limit:  params.Limit,
		Offset: params.Offset(),
	})
}
