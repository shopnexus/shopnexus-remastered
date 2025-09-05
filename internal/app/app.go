package app

import (
	"shopnexus-remastered/config"
	"shopnexus-remastered/internal/logger"
	"shopnexus-remastered/internal/module/account"
	"shopnexus-remastered/internal/module/auth"
	"shopnexus-remastered/internal/module/catalog"

	"go.uber.org/fx"
)

// Module combines all internal modules
var Module = fx.Module("main",
	// Infrastructure
	fx.Provide(
		NewConfig,
		NewDatabase,
		NewEcho,
	),

	// Business modules
	account.Module,
	auth.Module,
	catalog.Module,

	// HTTP server
	fx.Invoke(
		SetupLogger,
		SetupEcho,
		StartHTTPServer,
	),
)

// NewConfig provides the application configuration
func NewConfig() *config.Config {
	return config.GetConfig()
}

func SetupLogger() {
	logger.InitLogger()
}
