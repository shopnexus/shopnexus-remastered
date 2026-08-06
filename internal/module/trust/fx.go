package trust

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx"

	"shopnexus/internal/config"
	"shopnexus/internal/infra/durable"
	"shopnexus/internal/infra/eventbus"
	"shopnexus/internal/infra/postgres"
	"shopnexus/internal/module/common"
	"shopnexus/internal/module/common/dbx"
	"shopnexus/internal/module/common/uploads"
	"shopnexus/internal/module/order"
	trustpg "shopnexus/internal/module/trust/adapter/postgres"
	trustapi "shopnexus/internal/module/trust/api"
	"shopnexus/internal/module/trust/domain"
	"shopnexus/internal/module/trust/port"
	"shopnexus/internal/provider/storage"
	"shopnexus/internal/shared/id"
)

// Module wires the trust service, its Postgres-backed repository, the review photo uploads,
// the blind-window reveal, and the subscriber that folds a finished order into both parties'
// reputation.
var Module = fx.Module("trust",
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
		fx.Annotate(NewService, fx.As(new(trustapi.Service))),
		NewReveal,
		fx.Annotate(newSweep, fx.ResultTags(`group:"sweeps"`)),
	),
	// Eager, because nothing else in the graph depends on a subscription: without this the
	// bus would have no consumer until something happened to ask for the service.
	fx.Invoke(SubscribeSettledOrders),
	fx.Invoke(SubscribeResolvedRefunds),
	fx.Invoke(SubscribeEscalatedRefunds),
	fx.Invoke(SubscribeUnconfirmedOrders),
)

func newPool(lc fx.Lifecycle, cfg *config.Config) (*pgxpool.Pool, error) {
	pool, err := postgres.NewPool(context.Background(), cfg.TrustDBDSN, "trust")
	if err != nil {
		return nil, fmt.Errorf("open trust db pool: %w", err)
	}
	lc.Append(fx.Hook{OnStop: func(context.Context) error { pool.Close(); return nil }})
	return pool, nil
}

func newRepo(pool *pgxpool.Pool) *trustpg.Repo { return trustpg.New(pool) }

// newUploads is this module's own `resource` rows plus the object store. The prefix keeps
// trust's objects together, so an operator holding only a key can tell what it belongs to.
func newUploads(pool *pgxpool.Pool, cfg *config.Config, stores *storage.Registry) *uploads.Store {
	return uploads.New(dbx.NewResources(pool), stores, "trust", cfg.StorageUploadTTL)
}

// newUploadSweep reaps the slots nobody confirmed, so an abandoned review photo is not a row and
// an object that accumulate for ever.
func newUploadSweep(store *uploads.Store) durable.Sweep { return store.Sweep }

// SubscribeSettledOrders keeps the completed and cancelled counters on a reputation —
// "152 completed, 3 cancelled" says something an average cannot.
//
// Order is the authority and this is a mirror. Delivery is at-least-once, so the order id
// travels with the outcome and the service records it in the same transaction as the bump: a
// redelivery counts nothing, and a message that never arrived is still the one gap a recount
// has to close.
func SubscribeSettledOrders(bus eventbus.Client, svc trustapi.Service, log *slog.Logger) {
	eventbus.Subscribe(bus, order.OrderSettledTopic, "trust", func(ctx context.Context, event order.OrderSettled) error {
		req := trustapi.RecordOrderOutcomeRequest{
			OrderID:   id.Of[id.Order](event.OrderID),
			BuyerID:   id.Of[id.Account](event.BuyerID),
			SellerID:  id.Of[id.Account](event.SellerID),
			Completed: event.Completed,
		}
		if err := svc.RecordOrderOutcome(ctx, req); err != nil {
			log.Error("record order outcome failed", "order_id", event.OrderID, "err", err)
			return err
		}
		return nil
	})
}

// SubscribeResolvedRefunds closes the ticket a refund dispute opened, once order has decided it.
// The verdict moves money, so order is where it is made; this is the requester's half of it.
func SubscribeResolvedRefunds(bus eventbus.Client, svc trustapi.Service, log *slog.Logger) {
	eventbus.Subscribe(bus, order.RefundResolvedTopic, "trust", func(ctx context.Context, event order.RefundResolved) error {
		req := trustapi.RecordRefundVerdictRequest{
			OrderID:     id.Of[id.Order](event.OrderID),
			RefundID:    id.Of[id.Refund](event.RefundID),
			ModeratorID: id.Of[id.Account](event.ModeratorID),
			BuyerWins:   event.BuyerWins,
			Note:        event.Note,
		}
		if err := svc.RecordRefundVerdict(ctx, req); err != nil {
			log.Error("record refund verdict failed", "refund_id", event.RefundID, "err", err)
			return err
		}
		return nil
	})
}

// SubscribeEscalatedRefunds opens the ticket for a refund whose seller ran out of time. Order
// moved the case to `disputed` itself and published this; the buyer never has to know they were
// meant to chase it, which is the whole point of the change.
//
// The requester is the buyer: they raised the refund and they are the one owed an answer. Filed
// against the order, like every refund-dispute ticket, so a seller's later complaint about what
// came back lands in the same queue against the same sale.
func SubscribeEscalatedRefunds(bus eventbus.Client, svc trustapi.Service, log *slog.Logger) {
	eventbus.Subscribe(bus, order.RefundEscalatedTopic, "trust", func(ctx context.Context, event order.RefundEscalated) error {
		req := trustapi.OpenTicketRequest{
			ActorID: id.Of[id.Account](event.BuyerID),
			Kind:    domain.KindRefundDispute,
			Subject: "Refund not answered by the seller",
			RefID:   id.Of[id.Order](event.OrderID).String(),
		}
		// No opening message: nobody wrote one. A body here would appear in the thread as the
		// buyer's words, and they never said them — the moderator opens the conversation.
		if _, err := svc.OpenTicket(ctx, req); err != nil {
			// A ticket that already exists is the answer, not a failure: this is redelivered like
			// every other event, and the requester may have raised one themselves in the meantime.
			if errors.Is(err, domain.ErrTicketExists) {
				return nil
			}
			log.Error("open ticket for escalated refund failed",
				"refund_id", event.RefundID, "order_id", event.OrderID, "err", err)
			return err
		}
		return nil
	})
}

// SubscribeUnconfirmedOrders opens the ticket for a seller who never accepted a paid order. Order
// does not void the sale and does not post the goods — neither the money nor the stock is the
// platform's to dispose of — so a human takes it from here.
func SubscribeUnconfirmedOrders(bus eventbus.Client, svc trustapi.Service, log *slog.Logger) {
	eventbus.Subscribe(bus, order.OrderConfirmationLapsedTopic, "trust", func(ctx context.Context, event order.OrderConfirmationLapsed) error {
		req := trustapi.OpenTicketRequest{
			ActorID: id.Of[id.Account](event.BuyerID),
			Kind:    domain.KindOrderIssue,
			Subject: "Seller has not accepted this order",
			RefID:   id.Of[id.Order](event.OrderID).String(),
		}
		if _, err := svc.OpenTicket(ctx, req); err != nil {
			if errors.Is(err, domain.ErrTicketExists) {
				return nil
			}
			log.Error("open ticket for unconfirmed order failed", "order_id", event.OrderID, "err", err)
			return err
		}
		return nil
	})
}

// newSweep registers the blind-window pass with the shared sweeper: one interval for every
// module's catch-up work rather than a ticker each.
func newSweep(r *Reveal) durable.Sweep { return r.Sweep }
