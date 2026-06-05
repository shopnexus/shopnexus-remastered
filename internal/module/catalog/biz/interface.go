package catalogbiz

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"shopnexus-server/internal/infras/cache"
	"shopnexus-server/internal/infras/milvus"
	restateclient "shopnexus-server/internal/infras/restate"
	accountbiz "shopnexus-server/internal/module/account/biz"
	analyticmodel "shopnexus-server/internal/module/analytic/model"
	catalogconfig "shopnexus-server/internal/module/catalog/config"
	catalogdb "shopnexus-server/internal/module/catalog/db/sqlc"
	catalogmodel "shopnexus-server/internal/module/catalog/model"
	commonbiz "shopnexus-server/internal/module/common/biz"
	inventorybiz "shopnexus-server/internal/module/inventory/biz"
	promotionbiz "shopnexus-server/internal/module/promotion/biz"
	"shopnexus-server/internal/provider/llm"
	"shopnexus-server/internal/shared/paginate"
	"shopnexus-server/internal/shared/pgsqlc"
)

// CatalogBiz is the client interface for CatalogHandler, which is used by other modules to call CatalogHandler methods.
//
//go:generate go run shopnexus-server/cmd/genrestate -interface CatalogBiz -service Catalog
type CatalogBiz interface {
	// Product Detail
	GetProductDetail(ctx context.Context, params GetProductDetailParams) (catalogmodel.ProductDetail, error)

	// Product Card
	GetProductCard(ctx context.Context, params GetProductCardParams) (*catalogmodel.ProductCard, error)
	ListProductCard(
		ctx context.Context,
		params ListProductCardParams,
	) (paginate.PaginateResult[catalogmodel.ProductCard], error)
	ListRecommendedProductCard(
		ctx context.Context,
		params ListRecommendedProductCardParams,
	) ([]catalogmodel.ProductCard, error)

	// Product SPU
	GetProductSpu(ctx context.Context, params GetProductSpuParams) (catalogmodel.ProductSpu, error)
	ListProductSpu(
		ctx context.Context,
		params ListProductSpuParams,
	) (paginate.PaginateResult[catalogmodel.ProductSpu], error)
	CreateProductSpu(ctx context.Context, params CreateProductSpuParams) (catalogmodel.ProductSpu, error)
	UpdateProductSpu(ctx context.Context, params UpdateProductSpuParams) (catalogmodel.ProductSpu, error)
	DeleteProductSpu(ctx context.Context, params DeleteProductSpuParams) error

	// Product SKU
	ListProductSku(ctx context.Context, params ListProductSkuParams) ([]catalogmodel.ProductSku, error)
	CreateProductSku(ctx context.Context, params CreateProductSkuParams) (catalogmodel.ProductSku, error)
	UpdateProductSku(ctx context.Context, params UpdateProductSkuParams) (catalogmodel.ProductSku, error)
	DeleteProductSku(ctx context.Context, params DeleteProductSkuParams) error

	// Comment
	ListComment(ctx context.Context, params ListCommentParams) (paginate.PaginateResult[catalogmodel.Comment], error)
	CreateComment(ctx context.Context, params CreateCommentParams) (catalogmodel.Comment, error)
	UpdateComment(ctx context.Context, params UpdateCommentParams) (catalogmodel.Comment, error)
	DeleteComment(ctx context.Context, params DeleteCommentParams) error
	ListReviewableOrders(ctx context.Context, params ListReviewableOrdersParams) ([]catalogmodel.ReviewableOrder, error)

	// Tag
	ListTag(ctx context.Context, params ListTagParams) (paginate.PaginateResult[catalogdb.CatalogTag], error)
	GetTag(ctx context.Context, params GetTagParams) (catalogdb.CatalogTag, error)

	// Category
	ListCategory(
		ctx context.Context,
		params ListCategoryParams,
	) (paginate.PaginateResult[catalogmodel.Category], error)

	// Search
	Search(ctx context.Context, params SearchParams) ([]catalogmodel.ProductRecommend, error)
	GetRecommendations(ctx context.Context, params GetRecommendationsParams) ([]catalogmodel.ProductRecommend, error)

	// Vendor Stats
	GetVendorStats(ctx context.Context, params GetVendorStatsParams) (VendorStats, error)
}

type CatalogStorage = pgsqlc.Storage[*catalogdb.Queries]

// CatalogHandler implements the core business logic for the catalog module.
type CatalogHandler struct {
	cfg           *catalogconfig.Config
	logger        *slog.Logger
	cache         cache.Client
	restateClient *restateclient.Client
	storage       CatalogStorage
	common        commonbiz.CommonBiz
	account       accountbiz.AccountBiz
	inventory     inventorybiz.InventoryBiz
	promotion     promotionbiz.PromotionBiz

	// Vector search (replaces searchClient)
	milvus       *milvus.Client
	llm          llm.Client
	denseWeight  float32
	sparseWeight float32
	batchSize    int

	// Event buffering (moved from SearchClient)
	mu       sync.Mutex
	buffer   []analyticmodel.Interaction
	syncLock sync.Mutex
}

// NewCatalogHandler creates a new CatalogHandler with the given dependencies.
func NewCatalogHandler(
	cfg *catalogconfig.Config,
	logger *slog.Logger,
	storage CatalogStorage,
	cache cache.Client,
	restateClient *restateclient.Client,
	common commonbiz.CommonBiz,
	account accountbiz.AccountBiz,
	inventory inventorybiz.InventoryBiz,
	promotion promotionbiz.PromotionBiz,
	milvusClient *milvus.Client,
	llmClient llm.Client,
) *CatalogHandler {
	b := &CatalogHandler{
		cfg:           cfg,
		logger:        logger,
		cache:         cache,
		restateClient: restateClient,
		storage:       storage,
		common:        common,
		account:       account,
		inventory:     inventory,
		promotion:     promotion,

		milvus:       milvusClient.WithTimeout(5 * time.Second),
		llm:          llmClient,
		denseWeight:  cfg.Search.DenseWeight,
		sparseWeight: cfg.Search.SparseWeight,
		batchSize:    cfg.Search.InteractionBatchSize,
	}

	// Setup Milvus collections
	if err := b.SetupMilvusCollections(context.Background()); err != nil {
		b.logger.Error("Failed to setup Milvus collections", "error", err)
	}

	if err := b.SetupCron(); err != nil {
		b.logger.Error("Failed to setup cron", "error", err)
	}

	return b
}

func (h *CatalogHandler) ServiceName() string {
	return "Catalog"
}
