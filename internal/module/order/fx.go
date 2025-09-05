package order

import (
	orderbiz "shopnexus-remastered/internal/module/order/biz"
	orderecho "shopnexus-remastered/internal/module/order/transport/echo"

	"go.uber.org/fx"
)

// Module provides the order module dependencies
var Module = fx.Module("order",
	fx.Provide(
		orderbiz.NewOrderBiz,
		orderecho.NewHandler,
	),
)
