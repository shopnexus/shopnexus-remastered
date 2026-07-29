package chat

import (
	"context"
	"fmt"

	"go.uber.org/fx"

	"shopnexus/internal/config"
	"shopnexus/internal/infra/postgres"
	chatpg "shopnexus/internal/module/chat/adapter/postgres"
	chatapi "shopnexus/internal/module/chat/api"
	"shopnexus/internal/module/chat/port"
)

// Module wires the chat service and its Postgres-backed repository.
var Module = fx.Module("chat",
	fx.Provide(
		fx.Annotate(newRepo, fx.As(new(port.Repository))),
		fx.Annotate(NewService, fx.As(new(chatapi.Service))),
	),
)

func newRepo(lc fx.Lifecycle, cfg *config.Config) (*chatpg.Repo, error) {
	pool, err := postgres.NewPool(context.Background(), cfg.ChatDBDSN, "chat")
	if err != nil {
		return nil, fmt.Errorf("open chat db pool: %w", err)
	}
	lc.Append(fx.Hook{OnStop: func(context.Context) error { pool.Close(); return nil }})
	return chatpg.New(pool), nil
}
