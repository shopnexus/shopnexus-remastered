package order

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/bytedance/sonic"
	"github.com/redis/rueidis"
	"go.uber.org/fx"

	"shopnexus-server/internal/infras/cache"
	"shopnexus-server/internal/infras/locker"
	redislocker "shopnexus-server/internal/infras/locker/redis"
	"shopnexus-server/internal/infras/pg"
	orderbiz "shopnexus-server/internal/module/order/biz"
	orderconfig "shopnexus-server/internal/module/order/config"
	orderdb "shopnexus-server/internal/module/order/db/sqlc"
	orderecho "shopnexus-server/internal/module/order/transport/echo"
	"shopnexus-server/internal/shared/pgsqlc"
)

// Module provides the order module dependencies. Pool/Redis/Cache/Logger are
// fx.Private — each is constructed from THIS module's own Postgres/Redis/Log
// config and invisible to other modules. The internal rueidis.Client is also
// private so NewCache and NewLocker can both consume it without leaking
// rueidis to the rest of the graph. Locker is PUBLIC because order biz
// consumes locker.Client.
var Module = fx.Module("order",
	fx.Provide(
		NewRedis,
		NewPool,
		NewCache,
		NewLogger,
		fx.Private,
	),
	fx.Provide(
		orderconfig.NewConfig,
		NewLocker,
		NewOrderStorage,
		orderbiz.NewOrderHandler,
		NewOrderBiz,
		orderecho.NewHandler,
		orderbiz.NewCheckoutWorkflow,
		orderbiz.NewConfirmWorkflow,
		orderbiz.NewPayoutWorkflow,
		orderbiz.NewRefundWorkflow,
	),
	fx.Invoke(
		orderecho.NewHandler,
	),
)

func NewPool(cfg *orderconfig.Config, lc fx.Lifecycle) (pgsqlc.TxBeginner, error) {
	pool, err := pg.New(pg.Options{
		Url:             cfg.Postgres.Url,
		Host:            cfg.Postgres.Host,
		Port:            cfg.Postgres.Port,
		Username:        cfg.Postgres.Username,
		Password:        cfg.Postgres.Password,
		Database:        cfg.Postgres.Database,
		MaxConnections:  cfg.Postgres.MaxConnections,
		MaxConnIdleTime: cfg.Postgres.MaxConnIdleTime,
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

// NewRedis is fx.Private: only NewCache and NewLocker (both module-internal)
// share this rueidis.Client; nothing outside the order module sees it.
func NewRedis(cfg *orderconfig.Config) (rueidis.Client, error) {
	rdb, err := rueidis.NewClient(rueidis.ClientOption{
		InitAddress: []string{fmt.Sprintf("%s:%s", cfg.Redis.Host, cfg.Redis.Port)},
		Password:    cfg.Redis.Password,
	})
	if err != nil {
		return nil, err
	}
	return rdb, nil
}

func NewCache(rdb rueidis.Client) (cache.Client, error) {
	return cache.NewRedisStructClient(rdb, cache.Config{
		Encoder: sonic.Marshal,
		Decoder: sonic.Unmarshal,
	})
}

func NewLogger(cfg *orderconfig.Config) *slog.Logger {
	return buildLogger(cfg.Log.Level, cfg.Log.AddSource, "order")
}

// buildLogger is the shared module-logger constructor — copied across module
// fx.go files to keep each module fully self-describing (no shared helper).
func buildLogger(levelStr string, addSource bool, module string) *slog.Logger {
	var level slog.Level
	switch levelStr {
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
		Level:     level,
		AddSource: addSource,
	})
	return slog.New(h).With(slog.String("module", module))
}

// NewLocker is PUBLIC — order biz needs locker.Client. The underlying
// rueidis.Client is module-private so it doesn't leak across the fx graph.
func NewLocker(rdb rueidis.Client) locker.Client {
	return redislocker.NewRedisLocker(rdb, locker.Config{
		TTL: 30 * time.Second,
	})
}

// NewOrderStorage creates a new order storage backed by PostgreSQL.
func NewOrderStorage(pool pgsqlc.TxBeginner) orderbiz.OrderStorage {
	return pgsqlc.NewStorage(pool, orderdb.New(pool))
}

// NewOrderBiz creates a Restate-backed client for the order module.
func NewOrderBiz(cfg *orderconfig.Config) orderbiz.OrderBiz {
	return orderbiz.NewOrderRestateClient(cfg.Restate.IngressAddress)
}
