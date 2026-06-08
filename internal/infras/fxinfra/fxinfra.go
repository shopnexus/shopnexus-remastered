// Package fxinfra provides the per-module infra fx providers (logger, pg
// pool, redis client, cache, bus) written once and instantiated per module:
//
//	fxinfra.Providers[*catalogconfig.Config]("catalog")
//
// Each instantiation is keyed on that module's own Config type, so providers
// stay fx.Private and every module still builds its infra from its own config
// values — only the glue is shared, matching how modules already share
// infras/pg, infras/cache and infras/bus.
package fxinfra

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"

	"github.com/redis/rueidis"
	"go.uber.org/fx"

	"shopnexus-server/config"
	"shopnexus-server/internal/infras/bus"
	"shopnexus-server/internal/infras/cache"
	"shopnexus-server/internal/infras/pg"
	"shopnexus-server/internal/infras/rankedset"
	"shopnexus-server/internal/shared/pgsqlc"
)

// Providers bundles the standard infra providers as one fx.Private block.
// Modules with a custom shape (e.g. order shares one rueidis.Client between
// cache and locker) compose the individual providers instead.
func Providers[C config.HasShared](module string) fx.Option {
	return fx.Provide(
		Logger[C](module),
		Pool[C],
		Cache[C],
		Bus[C],
		RankedSet[C],
		fx.Private,
	)
}

// Logger returns the module logger constructor; every record carries the
// module name.
func Logger[C config.HasShared](module string) func(C) *slog.Logger {
	return func(cfg C) *slog.Logger {
		l := cfg.SharedConfig().Log
		var level slog.Level
		switch l.Level {
		case "debug":
			level = slog.LevelDebug
		case "warn":
			level = slog.LevelWarn
		case "error":
			level = slog.LevelError
		default:
			level = slog.LevelInfo
		}
		h := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			Level:       level,
			AddSource:   l.AddSource,
			ReplaceAttr: nil,
		})
		return slog.New(h).With(slog.String("module", module))
	}
}

// Pool builds the module's pgx pool, pinged on start and closed on stop.
func Pool[C config.HasShared](cfg C, lc fx.Lifecycle) (pgsqlc.TxBeginner, error) {
	p := cfg.SharedConfig().Postgres
	pool, err := pg.New(pg.Options{
		Url:             p.Url,
		Host:            p.Host,
		Port:            p.Port,
		Username:        p.Username,
		Password:        p.Password,
		Database:        p.Database,
		MaxConnections:  p.MaxConnections,
		MaxConnIdleTime: p.MaxConnIdleTime,
	})
	if err != nil {
		return nil, err
	}
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error { return pool.Ping(ctx) },
		OnStop:  func(context.Context) error { pool.Close(); return nil },
	})
	return pool, nil
}

// Redis builds the module's rueidis client.
// ! Using the same redis instance for all other infras: bus, cache, rate limiter, etc. !
func Redis[C config.HasShared](cfg C) (rueidis.Client, error) {
	r := cfg.SharedConfig().Redis
	rdb, err := rueidis.NewClient(rueidis.ClientOption{
		InitAddress: []string{fmt.Sprintf("%s:%s", r.Host, r.Port)},
		Password:    r.Password,
		SelectDB:    int(r.DB),
	})
	if err != nil {
		return nil, fmt.Errorf("redis client: %w", err)
	}
	return rdb, nil
}

// Cache builds the module's JSON struct cache over its own rueidis client,
// pinged on start and closed on stop.
func Cache[C config.HasShared](cfg C, lc fx.Lifecycle) (cache.Client, error) {
	rdb, err := Redis(cfg)
	if err != nil {
		return nil, err
	}
	c, err := cache.NewRedisStructClient(rdb, cache.Config{
		Encoder: json.Marshal,
		Decoder: json.Unmarshal,
	})
	if err != nil {
		return nil, err
	}
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error { return c.Ping() },
		OnStop:  func(context.Context) error { return c.Close() },
	})
	return c, nil
}

// RankedSet builds the module's ranked set: "memory" is an in-process store
// (dev / single instance), "redis" runs on a sorted set over the module's own
// rueidis client, pinged on start and closed on stop.
func RankedSet[C config.HasShared](cfg C, lc fx.Lifecycle) (rankedset.Client, error) {
	var (
		c   rankedset.Client
		err error
	)
	switch cfg.SharedConfig().RankedSet.Transport {
	case "memory":
		c = rankedset.NewMemoryRankedSet(rankedset.Config{
			Encoder: json.Marshal,
			Decoder: json.Unmarshal,
		})
	case "redis":
		rdb, rerr := Redis(cfg)
		if rerr != nil {
			return nil, rerr
		}
		c, err = rankedset.NewRedisRankedSet(rdb, rankedset.Config{
			Encoder: json.Marshal,
			Decoder: json.Unmarshal,
		})
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("rankedset: unknown transport %q", cfg.SharedConfig().RankedSet.Transport)
	}
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error { return c.Ping() },
		OnStop:  func(context.Context) error { return c.Close() },
	})
	return c, nil
}

// Bus builds the module's event bus client: "memory" returns the shared
// in-process bus (its lifecycle is owned by MemoryTransport), "redis" builds
// a Redis Streams bus over the module's own connection.
func Bus[C config.HasShared](cfg C, logger *slog.Logger, lc fx.Lifecycle) (bus.Client, error) {
	if cfg.SharedConfig().Bus.Transport == "memory" {
		logger.Info("using in-memory bus transport; events won't persist across restarts")
		return bus.NewMemory(logger), nil
	}
	rdb, err := Redis(cfg)
	if err != nil {
		return nil, fmt.Errorf("bus: %w", err)
	}
	b := bus.NewRedis(rdb, logger)
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error { return b.Ping() },
		OnStop:  func(context.Context) error { return b.Close() },
	})
	return b, nil
}
