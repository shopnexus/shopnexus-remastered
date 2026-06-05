package promotionbiz

import (
	"context"
	"log/slog"

	catalogmodel "shopnexus-server/internal/module/catalog/model"
	promotiondb "shopnexus-server/internal/module/promotion/db/sqlc"
	promotionmodel "shopnexus-server/internal/module/promotion/model"
	"shopnexus-server/internal/shared/paginate"
	"shopnexus-server/internal/shared/pgsqlc"

	"github.com/google/uuid"
)

// PromotionBiz is the client interface for PromotionHandler, which is used by other modules to call PromotionHandler methods.
//
//go:generate go run shopnexus-server/cmd/genrestate -interface PromotionBiz -service Promotion
type PromotionBiz interface {
	// Promotion
	GetPromotion(ctx context.Context, params GetPromotionParams) (promotionmodel.Promotion, error)
	ListPromotion(
		ctx context.Context,
		params ListPromotionParams,
	) (paginate.PaginateResult[promotionmodel.Promotion], error)
	CreatePromotion(ctx context.Context, params CreatePromotionParams) (promotionmodel.Promotion, error)
	UpdatePromotion(ctx context.Context, params UpdatePromotionParams) (promotionmodel.Promotion, error)
	DeletePromotion(ctx context.Context, params DeletePromotionParams) error
	CalculatePromotedPrices(
		ctx context.Context,
		params CalculatePromotedPricesParams,
	) (map[uuid.UUID]*catalogmodel.OrderPrice, error)
}

type PromotionStorage = pgsqlc.Storage[*promotiondb.Queries]

// PromotionHandler implements the core business logic for the promotion module.
type PromotionHandler struct {
	logger  *slog.Logger
	storage PromotionStorage
}

func (b *PromotionHandler) ServiceName() string {
	return "Promotion"
}

// NewPromotionHandler creates a new PromotionHandler with the given dependencies.
func NewPromotionHandler(logger *slog.Logger, storage PromotionStorage) *PromotionHandler {
	return &PromotionHandler{
		logger:  logger,
		storage: storage,
	}
}
