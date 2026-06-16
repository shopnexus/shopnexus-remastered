package base

import (
	"context"
	"fmt"
	"log/slog"

	accountbiz "shopnexus-server/internal/module/account/biz"
	analyticbiz "shopnexus-server/internal/module/analytic/biz"
	catalogbiz "shopnexus-server/internal/module/catalog/biz"
	commonbiz "shopnexus-server/internal/module/common/biz"
	inventorybiz "shopnexus-server/internal/module/inventory/biz"
	orderconfig "shopnexus-server/internal/module/order/config"
	ordermodel "shopnexus-server/internal/module/order/model"
	orderrepo "shopnexus-server/internal/module/order/repo"
	sharedcurrency "shopnexus-server/internal/shared/currency"
	"shopnexus-server/internal/shared/pgsqlc"

	"github.com/google/uuid"
)

// OrderStorage is the order module's storage handle.
type OrderStorage = pgsqlc.Storage[*orderrepo.Repository]

type Base struct {
	Cfg     *orderconfig.Config
	Logger  *slog.Logger
	Storage OrderStorage

	account   accountbiz.AccountBizClient
	analytic  analyticbiz.AnalyticBizClient
	catalog   catalogbiz.CatalogBizClient
	common    commonbiz.CommonBizClient
	inventory inventorybiz.InventoryBizClient
}

// New wires the shared dependency set consumed by every domain sub-handler.
func New(
	cfg *orderconfig.Config,
	logger *slog.Logger,
	storage OrderStorage,
	account accountbiz.AccountBizClient,
	analytic analyticbiz.AnalyticBizClient,
	catalog catalogbiz.CatalogBizClient,
	common commonbiz.CommonBizClient,
	inventory inventorybiz.InventoryBizClient,
) *Base {
	return &Base{
		Cfg:       cfg,
		Logger:    logger,
		Storage:   storage,
		account:   account,
		analytic:  analytic,
		catalog:   catalog,
		common:    common,
		inventory: inventory,
	}
}

// Account and Inventory expose the cross-module clients to sub-handlers that
// embed *Base (e.g. the refund credit flow). No restate.Context param, so
// restate.Reflect never binds them.
func (b *Base) Account() accountbiz.AccountBizClient       { return b.account }
func (b *Base) Inventory() inventorybiz.InventoryBizClient { return b.inventory }

// Notify sends an in-app notification one-way via the Account module.
func (b *Base) Notify(ctx context.Context, params accountbiz.CreateNotificationParams) error {
	return b.account.Send().CreateNotification(ctx, params)
}

// TrackInteractions records analytic interactions one-way.
func (b *Base) TrackInteractions(ctx context.Context, interactions ...analyticbiz.CreateInteraction) error {
	return b.analytic.Send().CreateInteraction(ctx, analyticbiz.CreateInteractionParams{
		Interactions: interactions,
	})
}

// GetHydratedOrder returns a single order by ID with all items and payment
// details. Backs both the buyer and seller single-order endpoints.
func (b *Base) GetHydratedOrder(ctx context.Context, orderID uuid.UUID) (ordermodel.Order, error) {
	var zero ordermodel.Order

	order, err := b.Storage.Querier().GetOrder(ctx, orderID)
	if err != nil {
		return zero, fmt.Errorf("get order: %w", err)
	}

	orders, err := b.HydrateOrders(ctx, []ordermodel.Order{order})
	if err != nil {
		return zero, fmt.Errorf("hydrate order: %w", err)
	}
	if len(orders) == 0 {
		return zero, ordermodel.ErrOrderNotFound
	}

	return orders[0], nil
}

// InferCurrency fetches the profile for accountID and resolves its ISO 4217 currency code.
func (b *Base) InferCurrency(ctx context.Context, accountID uuid.UUID) (string, error) {
	prof, err := b.account.GetProfile(ctx, accountbiz.GetProfileParams{AccountID: accountID})
	if err != nil {
		return "", fmt.Errorf("get profile for currency: %w", err)
	}
	cur, err := sharedcurrency.Infer(prof.Country)
	if err != nil {
		return "", fmt.Errorf("infer currency: %w", err)
	}
	return cur, nil
}
