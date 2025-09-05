package promotion

import (
	promotionbiz "shopnexus-remastered/internal/module/promotion/biz"
	promotionecho "shopnexus-remastered/internal/module/promotion/transport/echo"

	"go.uber.org/fx"
)

// Module provides the promotion module dependencies
var Module = fx.Module("promotion",
	fx.Provide(
		promotionbiz.NewPromotionBiz,
		promotionecho.NewHandler,
	),
)
