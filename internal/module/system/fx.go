package system

import (
	systembiz "shopnexus-remastered/internal/module/system/biz"
	systemecho "shopnexus-remastered/internal/module/system/transport/echo"

	"go.uber.org/fx"
)

// Module provides the system module dependencies
var Module = fx.Module("system",
	fx.Provide(
		systembiz.NewSystemBiz,
		systemecho.NewHandler,
	),
)
