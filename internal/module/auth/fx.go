package auth

import (
	authbiz "shopnexus-remastered/internal/module/auth/biz"
	authecho "shopnexus-remastered/internal/module/auth/transport/echo"

	"go.uber.org/fx"
)

// Module provides the auth module dependencies
var Module = fx.Module("auth",
	fx.Provide(
		authbiz.NewAuthBiz,
		authecho.NewHandler,
	),
)
