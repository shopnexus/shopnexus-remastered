package orderbiz

import (
	"context"
	stderrors "errors"
	"log/slog"

	"shopnexus-server/internal/infras/locker"
	accountbiz "shopnexus-server/internal/module/account/biz"
	catalogbiz "shopnexus-server/internal/module/catalog/biz"
	commonbiz "shopnexus-server/internal/module/common/biz"
	inventorybiz "shopnexus-server/internal/module/inventory/biz"
	orderconfig "shopnexus-server/internal/module/order/config"
	promotionbiz "shopnexus-server/internal/module/promotion/biz"
	sharedmodel "shopnexus-server/internal/shared/model"
)

// core holds the shared dependencies plus the cross-domain helpers
// (InferCurrency, CreditFromSession, hydrateOrders, enrichItems). Every domain
// sub-handler embeds *core; OrderHandler embeds it directly as well, so a
// promoted core selector resolves at depth 1 instead of being ambiguous across
// the depth-2 copies carried inside each sub-handler.
type core struct {
	cfg       *orderconfig.Config
	logger    *slog.Logger
	storage   OrderStorage
	locker    locker.Client
	account   accountbiz.AccountBiz
	catalog   catalogbiz.CatalogBiz
	inventory inventorybiz.InventoryBiz
	promotion promotionbiz.PromotionBiz
	common    commonbiz.CommonBiz
}

// Domain sub-handlers. Stateless — each is a typed view over *core, so its
// methods group by concern while sharing one dependency set.
type (
	buyerHandler      struct{ *core }
	sellerHandler     struct{ *core }
	orderQueryHandler struct{ *core }
	cartHandler       struct{ *core }
	paymentHandler    struct{ *core }
	refundHandler     struct{ *core }
	disputeHandler    struct{ *core }
	transportHandler  struct{ *core }
	reviewHandler     struct{ *core }
	dashboardHandler  struct{ *core }
)

// OrderHandler is the aggregate Restate service handler. Embedding every domain
// sub-handler promotes their methods up to satisfy OrderBiz; embedding *core
// directly keeps the shared helpers unambiguous.
type OrderHandler struct {
	*core
	*buyerHandler
	*sellerHandler
	*orderQueryHandler
	*cartHandler
	*paymentHandler
	*refundHandler
	*disputeHandler
	*transportHandler
	*reviewHandler
	*dashboardHandler
}

// NewOrderHandler wires one shared *core into every sub-handler.
func NewOrderHandler(
	cfg *orderconfig.Config,
	logger *slog.Logger,
	storage OrderStorage,
	locker locker.Client,
	account accountbiz.AccountBiz,
	catalog catalogbiz.CatalogBiz,
	inventory inventorybiz.InventoryBiz,
	promotion promotionbiz.PromotionBiz,
	common commonbiz.CommonBiz,
) (*OrderHandler, error) {
	c := &core{
		cfg:       cfg,
		logger:    logger,
		storage:   storage,
		locker:    locker,
		account:   account,
		catalog:   catalog,
		inventory: inventory,
		promotion: promotion,
		common:    common,
	}
	b := &OrderHandler{
		core:              c,
		buyerHandler:      &buyerHandler{c},
		sellerHandler:     &sellerHandler{c},
		orderQueryHandler: &orderQueryHandler{c},
		cartHandler:       &cartHandler{c},
		paymentHandler:    &paymentHandler{c},
		refundHandler:     &refundHandler{c},
		disputeHandler:    &disputeHandler{c},
		transportHandler:  &transportHandler{c},
		reviewHandler:     &reviewHandler{c},
		dashboardHandler:  &dashboardHandler{c},
	}
	return b, stderrors.Join(
		b.SetupPaymentMap(),
		b.SetupTransportMap(),
	)
}

type GetOptionsParams struct {
	Type sharedmodel.OptionType `json:"type"` // empty = all
}

func (b *OrderHandler) ServiceName() string {
	return "Order"
}

// GetOptions returns serializable Option configs (payment + transport providers).
func (b *OrderHandler) GetOptions(ctx context.Context, params GetOptionsParams) ([]sharedmodel.Option, error) {
	out := make([]sharedmodel.Option, 0)
	if params.Type == "" || params.Type == sharedmodel.OptionTypePayment {
		out = append(out, b.paymentConfigs()...)
	}
	if params.Type == "" || params.Type == sharedmodel.OptionTypeTransport {
		out = append(out, b.transportOptions()...)
	}
	return out, nil
}
