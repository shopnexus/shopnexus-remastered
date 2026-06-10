package order

import (
	"encoding/json"
	"time"

	"github.com/redis/rueidis"
	"go.uber.org/fx"

	"shopnexus-server/internal/infras/cache"
	"shopnexus-server/internal/infras/fxinfra"
	"shopnexus-server/internal/infras/locker"
	redislocker "shopnexus-server/internal/infras/locker/redis"
	orderbiz "shopnexus-server/internal/module/order/biz"
	"shopnexus-server/internal/module/order/biz/base"
	buyerorder "shopnexus-server/internal/module/order/biz/buyer_order"
	"shopnexus-server/internal/module/order/biz/cart"
	"shopnexus-server/internal/module/order/biz/dashboard"
	"shopnexus-server/internal/module/order/biz/dispute"
	orderpayment "shopnexus-server/internal/module/order/biz/payment"
	"shopnexus-server/internal/module/order/biz/refund"
	"shopnexus-server/internal/module/order/biz/review"
	sellerorder "shopnexus-server/internal/module/order/biz/seller_order"
	ordertransport "shopnexus-server/internal/module/order/biz/transport"
	wfbase "shopnexus-server/internal/module/order/biz/workflow/base"
	"shopnexus-server/internal/module/order/biz/workflow/checkout"
	"shopnexus-server/internal/module/order/biz/workflow/fullfilment"
	orderconfig "shopnexus-server/internal/module/order/config"
	orderdb "shopnexus-server/internal/module/order/db/sqlc"
	orderecho "shopnexus-server/internal/module/order/transport/echo"
	"shopnexus-server/internal/shared/pgsqlc"
)

// Module provides the order module dependencies. Pool/Redis/Cache/Logger are
// fx.Private — each is constructed from THIS module's own Postgres/Redis/Log
// config and invisible to other modules. The internal rueidis.Client is also
// private so NewCache and NewLocker can both consume it without leaking
// rueidis to the rest of the graph. Locker is PUBLIC because order biz
// consumes locker.Client.
var Module = fx.Module("order",
	fx.Provide(
		fxinfra.Redis[*orderconfig.Config], // one client shared by NewCache and NewLocker
		fxinfra.Pool[*orderconfig.Config],
		fxinfra.Logger[*orderconfig.Config]("order"),
		NewCache,
		fx.Private,
	),
	fx.Provide(
		orderconfig.NewConfig,
		NewLocker,
		NewOrderStorage,
		// shared cores + one constructor per domain sub-handler
		base.New,
		wfbase.New,
		buyerorder.New,
		sellerorder.New,
		cart.New,
		orderpayment.New,
		refund.New,
		// FulfillmentWorkflow drives the refund credit flow on auto-accept; bind
		// the single RefundHandler to fulfillment's RefundCrediter interface
		// (one-way dep avoids the fulfillment↔refund import cycle).
		func(h *refund.RefundHandler) fullfilment.RefundCrediter { return h },
		dispute.New,
		ordertransport.New,
		review.New,
		dashboard.New,
		orderbiz.NewOrderHandler,
		NewOrderBiz,
		orderecho.NewHandler,
		// workflows
		checkout.New,
		fullfilment.NewFulfillmentWorkflow,
		NewCheckoutWf,
		NewFulfillmentWf,
	),
	fx.Invoke(
		orderecho.NewHandler,
	),
)

func NewCache(rdb rueidis.Client) (cache.Client, error) {
	return cache.NewRedisStructClient(rdb, cache.Config{
		Encoder: json.Marshal,
		Decoder: json.Unmarshal,
	})
}

// NewLocker is PUBLIC — order biz needs locker.Client. The underlying
// rueidis.Client is module-private so it doesn't leak across the fx graph.
func NewLocker(rdb rueidis.Client) locker.Client {
	return redislocker.NewRedisLocker(rdb, locker.Config{
		TTL: 30 * time.Second,
	})
}

// NewOrderStorage creates a new order storage backed by PostgreSQL.
func NewOrderStorage(pool pgsqlc.TxBeginner) orderbiz.OrderStorage {
	return pgsqlc.NewStorage(pool, orderdb.New(pool))
}

// NewOrderBiz creates the order client. BestEffort calls run in-process.
func NewOrderBiz(cfg *orderconfig.Config, biz *orderbiz.OrderHandler) orderbiz.OrderBizClient {
	return orderbiz.NewOrderBizClientInProcess(cfg.Restate.IngressAddress, biz)
}

// NewCheckoutWf creates a Restate-backed client for the checkout workflow.
func NewCheckoutWf(cfg *orderconfig.Config) checkout.CheckoutWfClient {
	return checkout.NewCheckoutWorkflowRestateClient(cfg.Restate.IngressAddress)
}

// NewFulfillmentWf creates a Restate-backed client for the fulfillment workflow.
func NewFulfillmentWf(cfg *orderconfig.Config) fullfilment.FulfillmentWfClient {
	return fullfilment.NewFulfillmentWorkflowRestateClient(cfg.Restate.IngressAddress)
}
