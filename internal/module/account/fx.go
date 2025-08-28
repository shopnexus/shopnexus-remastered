package account

import (
	accountbiz "shopnexus-remastered/internal/module/account/biz"
	accountecho "shopnexus-remastered/internal/module/account/transport/echo"

	"go.uber.org/fx"
)

// Module provides the account module dependencies
var Module = fx.Module("account",
	fx.Provide(
		accountbiz.NewAccountBiz,
		accountecho.NewHandler,
	),
)
