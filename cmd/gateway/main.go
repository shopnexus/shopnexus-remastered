package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/redis/rueidis"
	"go.uber.org/fx"
	"go.uber.org/fx/fxevent"

	"shopnexus/internal/config"
	"shopnexus/internal/gateway"
	"shopnexus/internal/infra/cache"
	"shopnexus/internal/infra/eventbus"
	"shopnexus/internal/module/account"
	"shopnexus/internal/module/catalog"
	"shopnexus/internal/module/chat"
	"shopnexus/internal/module/common"
	"shopnexus/internal/module/finance"
	"shopnexus/internal/module/observability"
	"shopnexus/internal/module/order"
	"shopnexus/internal/module/trust"
	"shopnexus/internal/shared/id"
	"shopnexus/internal/shared/logger"
	"shopnexus/internal/shared/token"
	"shopnexus/internal/shared/validation"
)

func main() {
	fx.New(
		// Base providers (not domain modules).
		fx.Provide(
			validation.Default,
			loadConfig,
			newLogger,
			newTokens,
			newBus,
			newCache,
		),
		// Domain modules — each wires its own service + repository.
		account.Module,
		catalog.Module,
		order.Module,
		chat.Module,
		common.Module,
		finance.Module,
		trust.Module,
		// Analytics + observability (product events, HTTP/runtime metrics into TimescaleDB).
		observability.Module,
		// Transport.
		gateway.Module,
		// Before the server accepts traffic: marshalling an id without a cipher
		// panics, and fx runs every Invoke before OnStart hooks.
		fx.Invoke(installIDCipher),
		// Route fx's own logs through slog.
		fx.WithLogger(func(log *slog.Logger) fxevent.Logger {
			return &fxevent.SlogLogger{Logger: log}
		}),
	).Run()
}

func loadConfig(v *validator.Validate) (*config.Config, error) {
	return config.Load(v)
}

func newLogger(cfg *config.Config) *slog.Logger {
	return logger.New(logger.Options{Level: cfg.LogLevel, Service: "gateway"})
}

func newTokens(cfg *config.Config) *token.Manager {
	return token.NewManager(cfg.JWTSecret, 24*time.Hour)
}

func installIDCipher(cfg *config.Config) error {
	if err := id.SetCipher([]byte(cfg.IDCipherKey)); err != nil {
		return fmt.Errorf("install id cipher: %w", err)
	}
	return nil
}

func redisClient(cfg *config.Config) (rueidis.Client, error) {
	client, err := rueidis.NewClient(rueidis.ClientOption{
		InitAddress: []string{cfg.RedisAddr},
		Password:    cfg.RedisPassword,
	})
	if err != nil {
		return nil, fmt.Errorf("create redis client: %w", err)
	}
	return client, nil
}

// newBus provides the event bus backed by Redis Streams. It owns its own
// rueidis client and closes it on shutdown.
func newBus(lc fx.Lifecycle, cfg *config.Config, log *slog.Logger) (eventbus.Client, error) {
	rdb, err := redisClient(cfg)
	if err != nil {
		return nil, err
	}
	b := eventbus.NewRedis(rdb, log)
	lc.Append(fx.Hook{OnStop: func(context.Context) error { return b.Close() }})
	return b, nil
}

// newCache provides the Redis-backed cache. It owns its own rueidis client and
// closes it on shutdown.
func newCache(lc fx.Lifecycle, cfg *config.Config) (cache.Client, error) {
	rdb, err := redisClient(cfg)
	if err != nil {
		return nil, err
	}
	c, err := cache.NewRedisStructClient(rdb, cache.Config{})
	if err != nil {
		rdb.Close()
		return nil, fmt.Errorf("init redis cache: %w", err)
	}
	lc.Append(fx.Hook{OnStop: func(context.Context) error { return c.Close() }})
	return c, nil
}
