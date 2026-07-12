package analyticbiz

import (
	"context"
	"log/slog"

	"shopnexus-server/config"
	"shopnexus-server/internal/infras/bus"
	analyticdb "shopnexus-server/internal/module/analytic/db/sqlc"
	analyticmodel "shopnexus-server/internal/module/analytic/model"
	promotionbiz "shopnexus-server/internal/module/promotion/biz"
	"shopnexus-server/internal/shared/paginate"
	"shopnexus-server/internal/shared/pgsqlc"

	"github.com/google/uuid"
	restate "github.com/restatedev/sdk-go"
)

// AnalyticBiz is the client interface for AnalyticBizHandler, which is used by other modules to call AnalyticBizHandler methods.
//
//go:generate go run shopnexus-server/cmd/genrestate -interface AnalyticBiz -service Analytic
type AnalyticBiz interface {
	// Interaction
	CreateInteraction(ctx restate.Context, params CreateInteractionParams) error

	// Popularity
	HandlePopularityEvent(ctx restate.Context, event analyticmodel.Interaction) error
	GetProductPopularity(ctx context.Context, spuID uuid.UUID) (analyticdb.AnalyticProductPopularity, error)
	ListTopProductPopularity(
		ctx context.Context,
		params paginate.Params,
	) ([]analyticdb.AnalyticProductPopularity, error)
}

type AnalyticStorage = pgsqlc.Storage[*analyticdb.Queries]

// AnalyticHandler implements the core business logic for the analytic module.
type AnalyticHandler struct {
	logger            *slog.Logger
	storage           AnalyticStorage
	promotion         promotionbiz.PromotionBizClient
	bus               bus.Client // interaction events fan out via pub/sub topics
	popularityWeights map[analyticmodel.Event]float64
}

func (b *AnalyticHandler) ServiceName() string {
	return "Analytic"
}

// NewAnalyticHandler creates a new AnalyticHandler with the given dependencies.
func NewAnalyticHandler(
	cfg *config.Config,
	logger *slog.Logger,
	storage AnalyticStorage,
	promotionBiz promotionbiz.PromotionBizClient,
	busClient bus.Client,
) *AnalyticHandler {
	return &AnalyticHandler{
		logger:            logger,
		storage:           storage,
		promotion:         promotionBiz,
		bus:               busClient,
		popularityWeights: weightMap(cfg.PopularityWeights),
	}
}
