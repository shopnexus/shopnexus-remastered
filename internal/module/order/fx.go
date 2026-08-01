package order

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	restate "github.com/restatedev/sdk-go"
	"go.uber.org/fx"

	"shopnexus/internal/config"
	infradurable "shopnexus/internal/infra/durable"
	"shopnexus/internal/infra/eventbus"
	"shopnexus/internal/infra/postgres"
	"shopnexus/internal/module/common"
	"shopnexus/internal/module/common/dbx"
	"shopnexus/internal/module/common/uploads"
	finance "shopnexus/internal/module/finance"
	"shopnexus/internal/module/order/adapter/durable"
	orderpg "shopnexus/internal/module/order/adapter/postgres"
	orderapi "shopnexus/internal/module/order/api"
	"shopnexus/internal/module/order/port"
	"shopnexus/internal/provider/storage"
	"shopnexus/internal/provider/transport"
	"shopnexus/internal/shared/id"
)

// Module wires the order service, its repository, the carrier registry it reads from its own
// schema, the evidence uploads its receipt confirmations and refunds carry, the durable
// lifecycle and the seam that drives it, and the subscriber that turns a settled payment into
// an order.
var Module = fx.Module("order",
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
		fx.Annotate(newOptions, fx.As(new(port.Options))),
		fx.Annotate(newUploadSweep, fx.ResultTags(`group:"sweeps"`)),
		newWorkflows,
		fx.Annotate(NewService, fx.As(new(orderapi.Service))),
		NewLifecycle,
		fx.Annotate(newDefinitions, fx.ResultTags(`group:"restate-definitions,flatten"`)),
		fx.Annotate(newSweep, fx.ResultTags(`group:"sweeps"`)),
	),
	// Eager, because nothing else in the graph depends on a subscription or a webhook route:
	// without these the bus would have no consumer and the carrier no path to report on.
	fx.Invoke(SubscribePaidSessions),
	fx.Invoke(WireTransportWebhooks),
)

// WireTransportWebhooks mounts the carrier's own reporting path and hands it the checkpoint
// recorder. Without it a booked parcel only ever moves when the seller says so, which is a
// shipment status that is always behind the parcel.
func WireTransportWebhooks(mux *http.ServeMux, courier transport.Client, svc orderapi.Service, log *slog.Logger) {
	recorder, ok := svc.(*Service)
	if !ok {
		return
	}
	path := courier.WireWebhooks(mux, func(ctx context.Context, r transport.WebhookResult) error {
		return recorder.RecordCarrierCheckpoint(ctx, r.TransportID, r.Status)
	})
	log.Info("transport webhook mounted", "path", path)
}

// workflowDeps takes the ingress client as optional, because the `off` deployment has none —
// and a graph that could not be built without a Restate URL would make the runtime mandatory
// by accident.
type workflowDeps struct {
	fx.In

	Config *config.Config
	Client *infradurable.Client `optional:"true"`
	Log    *slog.Logger
}

// newWorkflows picks who holds this module's timers. The selector is config, never code: a
// deployment that thinks it has durable timers and does not is found at startup rather than by
// the seller who was never paid.
func newWorkflows(deps workflowDeps) (port.Workflows, error) {
	if deps.Config.WorkflowRuntime != config.WorkflowRestate {
		return durable.NewOff(deps.Log), nil
	}
	if deps.Client == nil {
		return nil, fmt.Errorf("workflow runtime is restate but no ingress client was built")
	}
	return durable.New(deps.Client), nil
}

// newDefinitions hands this module's workflows to whoever serves them. Empty when there is no
// runtime: serving handlers nobody can invoke would only be a port to get wrong.
func newDefinitions(cfg *config.Config, l *Lifecycle) []restate.ServiceDefinition {
	if cfg.WorkflowRuntime != config.WorkflowRestate {
		return nil
	}
	return l.Definitions()
}

// newSweep is the periodic net under every timed transition, registered whether or not there
// is a runtime: with one it finds nothing, which is what makes leaving it on cheap.
func newSweep(l *Lifecycle) infradurable.Sweep { return l.Sweep }

func newPool(lc fx.Lifecycle, cfg *config.Config) (*pgxpool.Pool, error) {
	pool, err := postgres.NewPool(context.Background(), cfg.OrderDBDSN, "order")
	if err != nil {
		return nil, fmt.Errorf("open order db pool: %w", err)
	}
	lc.Append(fx.Hook{OnStop: func(context.Context) error { pool.Close(); return nil }})
	return pool, nil
}

func newRepo(pool *pgxpool.Pool) *orderpg.Repo { return orderpg.New(pool) }

func newOptions(pool *pgxpool.Pool) *dbx.Options { return dbx.NewOptions(pool) }

// newUploads is this module's own `resource` rows plus the object store. The prefix keeps
// order's objects together, so an operator holding only a key can tell what it belongs to.
func newUploads(pool *pgxpool.Pool, cfg *config.Config, client storage.Client) *uploads.Store {
	return uploads.New(dbx.NewResources(pool), client, "order", cfg.StorageUploadTTL)
}

// newUploadSweep reaps the slots nobody confirmed. Registered with the shared sweeper, because
// an abandoned upload is a row and an object that would otherwise accumulate for ever.
func newUploadSweep(store *uploads.Store) infradurable.Sweep { return store.Sweep }

// SubscribePaidSessions is what makes the money create the order. Finance publishes a
// settled session; this turns it into an order, a shipment and an escrow hold.
//
// The handler is idempotent, so a redelivered message is a no-op rather than a second order —
// which is what lets the bus be at-least-once and the durable workflow retry the same step.
func SubscribePaidSessions(bus eventbus.Client, svc orderapi.Service, log *slog.Logger) {
	eventbus.Subscribe(bus, finance.SessionPaidTopic, "order", func(ctx context.Context, event finance.SessionPaid) error {
		// Only a buyer's checkout becomes an order. A payout or a withdrawal is finance's own
		// business and has no sale behind it.
		if event.Kind != finance.KindBuyerCheckout {
			return nil
		}
		if err := svc.SettlePaidSession(ctx, id.Of[id.PaymentSession](event.SessionID)); err != nil {
			log.Error("settle paid session failed", "session_id", event.SessionID, "err", err)
			return err
		}
		return nil
	})
}
