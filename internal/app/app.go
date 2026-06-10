package app

import (
	"log/slog"
	"os"

	"go.uber.org/fx"

	appconfig "shopnexus-server/internal/app/config"
	"shopnexus-server/internal/infras/ratelimit"
	"shopnexus-server/internal/module/account"
	"shopnexus-server/internal/module/analytic"
	"shopnexus-server/internal/module/catalog"
	"shopnexus-server/internal/module/chat"
	"shopnexus-server/internal/module/common"
	"shopnexus-server/internal/module/inventory"
	"shopnexus-server/internal/module/order"
	"shopnexus-server/internal/module/promotion"
)

var Module = fx.Module("main",
	fx.Provide(
		appconfig.NewConfig,
		NewEcho,
		NewRateLimiter,
	),

	common.Module,
	account.Module,
	catalog.Module,
	inventory.Module,
	order.Module,
	promotion.Module,
	analytic.Module,
	chat.Module,

	fx.Invoke(
		SetupLogger,
		SetupRestate,
		SetupBestEffort,
		SetupEcho,
		SetupHTTPServer,
	),
)

// SetupLogger sets the process-wide slog.Default.
func SetupLogger(cfg *appconfig.Config) {
	var level slog.Level
	switch cfg.Log.Level {
	case "debug":
		level = slog.LevelDebug
	case "info":
		level = slog.LevelInfo
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level:     level,
		AddSource: cfg.Log.AddSource,
	})))
}

func NewRateLimiter() *ratelimit.Factory {
	return ratelimit.NewFactory(nil)
}
