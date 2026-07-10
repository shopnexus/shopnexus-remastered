package order

import (
	"encoding/json"
	"log/slog"
	"time"

	"github.com/redis/rueidis"
	restate "github.com/restatedev/sdk-go"
	"go.uber.org/fx"

	"shopnexus-server/internal/infras/cache"
	"shopnexus-server/internal/infras/infra"
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
	"shopnexus-server/internal/module/order/biz/workflow/checkout"
	"shopnexus-server/internal/module/order/biz/workflow/fullfilment"
	"shopnexus-server/internal/module/order/biz/workflow/gateway"
	orderconfig "shopnexus-server/internal/module/order/config"
	orderrepo "shopnexus-server/internal/module/order/repo"
	orderecho "shopnexus-server/internal/module/order/transport/echo"
	"shopnexus-server/internal/shared/besteffort"
	"shopnexus-server/internal/shared/pgsqlc"
	"shopnexus-server/internal/shared/restatesvc"
)

// Module provides the order module dependencies. Pool/Redis/Cache/Logger are
// fx.Private — each is constructed from THIS module's own Postgres/Redis/Log
// config and invisible to other modules. The internal rueidis.Client is also
// private so NewCache and NewLocker can both consume it without leaking
// rueidis to the rest of the graph. Locker is PUBLIC because order biz
// consumes locker.Client.
var Module = fx.Module("order",
	fx.Provide(
		// one client shared by NewCache and NewLocker
		func(c *orderconfig.Config) (rueidis.Client, error) { return infra.NewRedis(c.Redis) },
		func(c *orderconfig.Config, lc fx.Lifecycle) (pgsqlc.TxBeginner, error) {
			return infra.NewPool(c.Postgres, lc)
		},
		func(c *orderconfig.Config) *slog.Logger { return infra.NewLogger(c.Log, "order") },
		NewCache,
		fx.Private,
	),
	fx.Provide(
		orderconfig.NewConfig,
		NewLocker,
		NewOrderStorage,
		// shared cores + one constructor per domain sub-handler
		base.New,
		gateway.New,
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
	fx.Provide(
		fx.Annotate(
			func(b *orderbiz.OrderHandler) restate.ServiceDefinition {
				return restatesvc.Reflect(orderbiz.NewOrderService(b))
			},
			fx.ResultTags(`group:"restate"`),
		),
		fx.Annotate(
			func(b *orderbiz.OrderHandler) besteffort.Registrar {
				return func(s *besteffort.Server) { orderbiz.RegisterOrderBestEffort(s, b) }
			},
			fx.ResultTags(`group:"besteffort"`),
		),
		fx.Annotate(
			func(wf *orderbiz.CheckoutWorkflow) restate.ServiceDefinition { return restatesvc.Reflect(wf) },
			fx.ResultTags(`group:"restate"`),
		),
		fx.Annotate(
			func(wf *orderbiz.FulfillmentWorkflow) restate.ServiceDefinition { return restatesvc.Reflect(wf) },
			fx.ResultTags(`group:"restate"`),
		),
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
	return pgsqlc.NewStorage(pool, orderrepo.New(pool))
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
