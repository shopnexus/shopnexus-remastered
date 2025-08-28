package app

import (
	"shopnexus-remastered/config"
	"shopnexus-remastered/internal/logger"
	"shopnexus-remastered/internal/module/account"
	"shopnexus-remastered/internal/module/auth"

	"go.uber.org/fx"
)

func init() {
	logger.InitLogger()
}

// Module combines all internal modules
var Module = fx.Module("main",
	// Infrastructure
	fx.Provide(
		NewConfig,
		NewDatabase,
		NewEcho,
		NewValidator,
	),

	// Business modules
	account.Module,
	auth.Module,

	// HTTP server
	fx.Invoke(
		RegisterRoutes,
		StartHTTPServer,
	),
)

// NewConfig provides the application configuration
func NewConfig() *config.Config {
	return config.GetConfig()
}
