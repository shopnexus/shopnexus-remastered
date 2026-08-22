package catalog

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx"

	"shopnexus/internal/config"
	"shopnexus/internal/infra/durable"
	"shopnexus/internal/infra/eventbus"
	"shopnexus/internal/infra/postgres"
	catalogpg "shopnexus/internal/module/catalog/adapter/postgres"
	catalogapi "shopnexus/internal/module/catalog/api"
	"shopnexus/internal/module/catalog/port"
	"shopnexus/internal/module/common"
	"shopnexus/internal/module/common/dbx"
	"shopnexus/internal/module/common/uploads"
	"shopnexus/internal/module/order"
	"shopnexus/internal/provider/storage"
)

// Module wires the catalog service, its Postgres-backed repository, and the uploads its
// listings' photos land in.
var Module = fx.Module("catalog",
	// Private, and in a Provide of its own because fx.Private applies to every constructor in
	// the same call. All three are this module's own: two modules each providing a bare
	// *pgxpool.Pool, a bare *uploads.Store or a bare common.Uploads into the root graph is a
	// conflict rather than one of each per module — only this module's own service may see them.
	fx.Provide(fx.Private,
		newPool,
		newUploads,
		fx.Annotate(func(s *uploads.Store) common.Uploads { return s }),
	),
	fx.Provide(
		fx.Annotate(newRepo, fx.As(new(port.Repository))),
		fx.Annotate(newUploadSweep, fx.ResultTags(`group:"sweeps"`)),
		fx.Annotate(newInterestSweep, fx.ResultTags(`group:"sweeps"`)),
		fx.Annotate(NewService, fx.As(new(catalogapi.Service))),
	),
	// Without this the bus would have no consumer, and a shopper's actions would move nothing
	// but observability's popularity score.
	fx.Invoke(SubscribeListingInteractions),
	// Without this a purchase would move nothing at all — order publishes the fact and this
	// module is what turns it into personalisation's strongest signal.
	fx.Invoke(SubscribeOrderPlaced),
)

func newPool(lc fx.Lifecycle, cfg *config.Config) (*pgxpool.Pool, error) {
	pool, err := postgres.NewPool(context.Background(), cfg.CatalogDBDSN, "catalog")
	if err != nil {
		return nil, fmt.Errorf("open catalog db pool: %w", err)
	}
	lc.Append(fx.Hook{OnStop: func(context.Context) error { pool.Close(); return nil }})
	return pool, nil
}

func newRepo(pool *pgxpool.Pool) *catalogpg.Repo { return catalogpg.New(pool) }

// newUploads is this module's own `resource` rows plus the object store. The prefix keeps
// catalog's objects together, so an operator holding only a key can tell what it belongs to.
func newUploads(pool *pgxpool.Pool, cfg *config.Config, stores *storage.Registry) *uploads.Store {
	return uploads.New(dbx.NewResources(pool), stores, "catalog", cfg.StorageUploadTTL)
}

// newUploadSweep reaps the slots nobody confirmed. Registered with the shared sweeper, because
// an abandoned upload is a row and an object that would otherwise accumulate for ever.
func newUploadSweep(store *uploads.Store) durable.Sweep { return store.Sweep }

// newInterestSweep is the net under the recompute a wishlist write already runs: an account
// whose saved listing was only embedded afterwards, or whose inline recompute failed, is
// found here instead of waiting for their next save.
func newInterestSweep(repo port.Repository) durable.Sweep {
	return func(ctx context.Context, log *slog.Logger) { sweepInterests(ctx, repo, log) }
}

// SubscribeListingInteractions is this module's own consumer of its own fact: a shopper's
// action, turned into a listing_signal row for personalisation to read. Anonymous actions
// (AccountID 0) are dropped here — they have no account for interestSignals to attach to, and
// observability's independent subscriber is what still counts them toward popularity.
func SubscribeListingInteractions(bus eventbus.Client, repo port.Repository, log *slog.Logger) {
	eventbus.SubscribeBatch(bus, ListingInteractionTopic, "catalog",
		func(ctx context.Context, events []ListingInteraction) error {
			signals := make([]port.ListingSignal, 0, len(events))
			for _, e := range events {
				if e.AccountID == 0 {
					continue
				}
				signals = append(signals, port.ListingSignal{
					AccountID: e.AccountID, ListingID: e.ListingID, Type: e.Type,
				})
			}
			if err := repo.InsertListingSignals(ctx, signals); err != nil {
				log.Error("insert listing signals", "err", err)
				return err
			}
			return nil
		}, eventbus.WithBatchSize(50), eventbus.WithLinger(2*time.Second))
}

// SubscribeOrderPlaced turns a purchase into the buyer's strongest positive signal. Order
// publishes the whole fact, lines included, so this module reads what it needs from the event
// rather than reaching back into order's own tables for it — the coupling a published fact
// exists to avoid.
func SubscribeOrderPlaced(bus eventbus.Client, repo port.Repository, log *slog.Logger) {
	eventbus.SubscribeBatch(bus, order.OrderPlacedTopic, "catalog",
		func(ctx context.Context, events []order.OrderPlaced) error {
			var signals []port.ListingSignal
			for _, e := range events {
				for _, line := range e.Lines {
					signals = append(signals, port.ListingSignal{
						AccountID: e.BuyerID, ListingID: line.ListingID, Type: catalogapi.InteractionPurchase,
					})
				}
			}
			if err := repo.InsertListingSignals(ctx, signals); err != nil {
				log.Error("insert purchase signals", "err", err)
				return err
			}
			return nil
		}, eventbus.WithBatchSize(50), eventbus.WithLinger(2*time.Second))
}
