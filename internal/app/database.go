package app

import (
	"context"

	"shopnexus-remastered/config"
	"shopnexus-remastered/internal/client/pgxpool"
	"shopnexus-remastered/internal/logger"
	pgxsqlc "shopnexus-remastered/internal/utils/pgx/sqlc"

	"go.uber.org/fx"
)

// NewDatabase creates a new database connection
func NewDatabase(lc fx.Lifecycle, cfg *config.Config) (*pgxsqlc.Storage, error) {
	pool, err := pgxpool.New(pgxpool.Options{
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

	// Add lifecycle hooks for cleanup
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			if err := pool.Ping(ctx); err != nil {
				logger.Log.Sugar().Errorf("Failed to ping database: %v", err)
				return err
			}
			logger.Log.Sugar().Infof("Connected to database %s at %s:%d",
				cfg.Postgres.Database,
				cfg.Postgres.Host,
				cfg.Postgres.Port,
			)
			return nil
		},
		OnStop: func(ctx context.Context) error {
			logger.Log.Sugar().Info("Closing database connection...")
			pool.Close()
			return nil
		},
	})

	return pgxsqlc.NewStorage(pool), nil
}
