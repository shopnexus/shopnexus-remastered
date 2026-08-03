package chat

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx"

	"shopnexus/internal/config"
	"shopnexus/internal/infra/durable"
	"shopnexus/internal/infra/eventbus"
	"shopnexus/internal/infra/postgres"
	chatpg "shopnexus/internal/module/chat/adapter/postgres"
	chatapi "shopnexus/internal/module/chat/api"
	"shopnexus/internal/module/chat/port"
	"shopnexus/internal/module/common"
	"shopnexus/internal/module/common/dbx"
	"shopnexus/internal/module/common/uploads"
	"shopnexus/internal/provider/storage"
	"shopnexus/internal/shared/realtime"
)

// Module wires the chat service, its Postgres-backed repository, and the uploads a message
// attachment lands in.
var Module = fx.Module("chat",
	// Private, and in a Provide of its own because fx.Private applies to every constructor in
	// the same call. All three are this module's own: two modules each providing a bare
	// *pgxpool.Pool, a bare *uploads.Store or a bare common.Uploads into the root graph is a
	// conflict rather than one of each per module — only this module's own service may see them.
	fx.Provide(fx.Private,
		newPool,
		newUploads,
		fx.Annotate(func(s *uploads.Store) common.Uploads { return s }),
		// NewService takes the realtime.Fanout interface, so it can be tested without a
		// bus; wiring it needs the concrete *eventbus.NATS, never eventbus.Client — that
		// interface is the Redis domain-event bus and has no Broadcast at all.
		fx.Annotate(func(bus *eventbus.NATS) realtime.Fanout { return bus }),
	),
	fx.Provide(
		fx.Annotate(newRepo, fx.As(new(port.Repository))),
		fx.Annotate(newUploadSweep, fx.ResultTags(`group:"sweeps"`)),
		fx.Annotate(NewService, fx.As(new(chatapi.Service))),
	),
)

func newPool(lc fx.Lifecycle, cfg *config.Config) (*pgxpool.Pool, error) {
	pool, err := postgres.NewPool(context.Background(), cfg.ChatDBDSN, "chat")
	if err != nil {
		return nil, fmt.Errorf("open chat db pool: %w", err)
	}
	lc.Append(fx.Hook{OnStop: func(context.Context) error { pool.Close(); return nil }})
	return pool, nil
}

func newRepo(pool *pgxpool.Pool) *chatpg.Repo { return chatpg.New(pool) }

// newUploads is this module's own `resource` rows plus the object store. The prefix keeps
// chat's objects together, so an operator holding only a key can tell what it belongs to.
func newUploads(pool *pgxpool.Pool, cfg *config.Config, stores *storage.Registry) *uploads.Store {
	return uploads.New(dbx.NewResources(pool), stores, "chat", cfg.StorageUploadTTL)
}

// newUploadSweep reaps the slots nobody confirmed, so an abandoned upload is not a row and an
// object that accumulate for ever.
func newUploadSweep(store *uploads.Store) durable.Sweep { return store.Sweep }
