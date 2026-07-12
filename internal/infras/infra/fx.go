package infra

import (
	"log/slog"

	"go.uber.org/fx"

	"shopnexus-server/config"
	"shopnexus-server/internal/infras/bus"
	"shopnexus-server/internal/infras/cache"
	"shopnexus-server/internal/infras/rankedset"
	"shopnexus-server/internal/shared/pgsqlc"
)

// StandardModule provides the infra a typical module needs — logger, pool,
// cache, bus, rankedset — each built from the shared *config.Config and scoped
// fx.Private so every module gets its OWN instance without colliding in the
// graph. Modules with special needs (e.g. order's shared rueidis client for
// cache+locker) wire their infra directly instead of using this.
//
// Providers are lazy: a module that doesn't consume (say) bus never constructs
// it, so no connection is opened.
func StandardModule(name string) fx.Option {
	return fx.Provide(
		func(c *config.Config) *slog.Logger { return NewLogger(c.Log, name) },
		func(c *config.Config, lc fx.Lifecycle) (pgsqlc.TxBeginner, error) {
			return NewPool(c.Postgres, lc)
		},
		func(c *config.Config, lc fx.Lifecycle) (cache.Client, error) {
			return NewCache(c.Redis, lc)
		},
		func(c *config.Config, logger *slog.Logger, lc fx.Lifecycle) (bus.Client, error) {
			return NewBus(c.Bus, c.Redis, logger, lc)
		},
		func(c *config.Config, lc fx.Lifecycle) (rankedset.Client, error) {
			return NewRankedSet(c.RankedSet, c.Redis, lc)
		},
		fx.Private,
	)
}
