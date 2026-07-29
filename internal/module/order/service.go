// Package order implements orderapi.Service.
package order

import (
	"context"
	"fmt"
	"log/slog"

	"shopnexus/internal/infra/eventbus"
	orderapi "shopnexus/internal/module/order/api"
	"shopnexus/internal/module/order/domain"
	"shopnexus/internal/module/order/port"
	"shopnexus/internal/shared/id"
)

type Service struct {
	repo   port.Repository
	events eventbus.Client
	log    *slog.Logger
}

func NewService(repo port.Repository, events eventbus.Client, log *slog.Logger) *Service {
	return &Service{repo: repo, events: events, log: log}
}

var _ orderapi.Service = (*Service)(nil)

func (s *Service) PlaceOrder(ctx context.Context, req orderapi.PlaceOrderRequest) (orderapi.Order, error) {
	o, err := domain.NewOrder(req.BuyerID.Int64(), req.Total)
	if err != nil {
		return orderapi.Order{}, err
	}
	if err := s.repo.Save(ctx, &o); err != nil {
		return orderapi.Order{}, fmt.Errorf("save order: %w", err)
	}
	// Publish the event; a bus failure must not fail an already-persisted order.
	if err := eventbus.Publish(ctx, s.events, OrderPlacedTopic, OrderPlaced{
		OrderID: o.ID,
		BuyerID: o.BuyerID,
		Total:   o.Total,
	}); err != nil {
		s.log.Warn("publish order.placed failed", "order_id", o.ID, "err", err)
	}
	return s.toAPIOrder(o), nil
}

func (s *Service) GetOrder(ctx context.Context, req orderapi.GetOrderRequest) (orderapi.Order, error) {
	o, err := s.repo.FindByID(ctx, req.ID.Int64())
	if err != nil {
		return orderapi.Order{}, fmt.Errorf("find order: %w", err)
	}
	return s.toAPIOrder(o), nil
}

// toAPIOrder maps domain -> api.
func (s *Service) toAPIOrder(o domain.Order) orderapi.Order {
	return orderapi.Order{
		ID:      id.Of[id.Order](o.ID),
		BuyerID: id.Of[id.Account](o.BuyerID),
		Total:   o.Total,
		Status:  o.Status,
	}
}
