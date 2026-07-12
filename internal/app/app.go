package app

import (
	"fmt"
	"log/slog"
	"os"

	"go.uber.org/fx"

	"shopnexus-server/config"
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

// Modules maps a service name to its fx module so one binary can run the whole
// monolith or a single module per pod (see cmd/server --module).
var Modules = map[string]fx.Option{
	"common":    common.Module,
	"account":   account.Module,
	"catalog":   catalog.Module,
	"inventory": inventory.Module,
	"order":     order.Module,
	"promotion": promotion.Module,
	"analytic":  analytic.Module,
	"chat":      chat.Module,
}

// ModuleNames is the canonical (dependency-friendly) order.
var ModuleNames = []string{"common", "account", "catalog", "inventory", "order", "promotion", "analytic", "chat"}

// Build composes the app for the named modules. Empty selection = all modules
// (monolith). Panics on an unknown name.
func Build(selected ...string) fx.Option {
	if len(selected) == 0 {
		selected = ModuleNames
	}
	opts := []fx.Option{
		fx.Provide(config.New, NewEcho, NewRateLimiter),
	}
	for _, name := range selected {
		m, ok := Modules[name]
		if !ok {
			panic(fmt.Sprintf("unknown module %q (known: %v)", name, ModuleNames))
		}
		opts = append(opts, m)
	}
	opts = append(opts, fx.Invoke(SetupLogger, SetupRestate, SetupBestEffort, SetupEcho, SetupHTTPServer))
	return fx.Module("main", opts...)
}

// Module is the full monolith (all modules).
var Module = Build()

// SetupLogger sets the process-wide slog.Default.
func SetupLogger(cfg *config.Config) {
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
