// Package infra provides the per-module infra provider funcs (logger, pg
// pool, redis client, cache, bus, ranked set) written once and called from
// each module's fx.go with that module's own config values:
//
//	infra.NewPool(cfg.Postgres, lc)
//
// Each module builds its infra from its own config leaves and keeps the
// providers fx.Private, so every module can provide the same types (e.g.
// pgsqlc.TxBeginner) without colliding — only the construction glue is shared.
package infra

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

// NewLogger builds the module logger; every record carries the module name.
func NewLogger(l config.Log, module string) *slog.Logger {
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

// NewPool builds the module's pgx pool, pinged on start and closed on stop.
func NewPool(p config.Postgres, lc fx.Lifecycle) (pgsqlc.TxBeginner, error) {
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

// NewRedis builds the module's rueidis client.
// ! Using the same redis instance for all other infras: bus, cache, rate limiter, etc. !
func NewRedis(r config.Redis) (rueidis.Client, error) {
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

// NewCache builds the module's JSON struct cache over its own rueidis client,
// pinged on start and closed on stop.
func NewCache(r config.Redis, lc fx.Lifecycle) (cache.Client, error) {
	rdb, err := NewRedis(r)
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

// NewRankedSet builds the module's ranked set: "memory" is an in-process store
// (dev / single instance), "redis" runs on a sorted set over the module's own
// rueidis client, pinged on start and closed on stop.
func NewRankedSet(rs config.RankedSet, r config.Redis, lc fx.Lifecycle) (rankedset.Client, error) {
	var (
		c   rankedset.Client
		err error
	)
	switch rs.Transport {
	case "memory":
		c = rankedset.NewMemoryRankedSet(rankedset.Config{
			Encoder: json.Marshal,
			Decoder: json.Unmarshal,
		})
	case "redis":
		rdb, rerr := NewRedis(r)
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
		return nil, fmt.Errorf("rankedset: unknown transport %q", rs.Transport)
	}
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error { return c.Ping() },
		OnStop:  func(context.Context) error { return c.Close() },
	})
	return c, nil
}

// NewBus builds the module's event bus client: "memory" returns the shared
// in-process bus (its lifecycle is owned by MemoryTransport), "redis" builds
// a Redis Streams bus over the module's own connection.
func NewBus(b config.Bus, r config.Redis, logger *slog.Logger, lc fx.Lifecycle) (bus.Client, error) {
	if b.Transport == "memory" {
		logger.Info("using in-memory bus transport; events won't persist across restarts")
		return bus.NewMemory(logger), nil
	}
	rdb, err := NewRedis(r)
	if err != nil {
		return nil, fmt.Errorf("bus: %w", err)
	}
	client := bus.NewRedis(rdb, logger)
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error { return client.Ping() },
		OnStop:  func(context.Context) error { return client.Close() },
	})
	return client, nil
}
