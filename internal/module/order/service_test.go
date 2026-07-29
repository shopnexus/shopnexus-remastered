package order_test

import (
	"context"
	"log/slog"
	"testing"

	"shopnexus/internal/infra/eventbus"
	"shopnexus/internal/module/order"
	orderapi "shopnexus/internal/module/order/api"
	"shopnexus/internal/module/order/domain"
	"shopnexus/internal/shared/errx"
	"shopnexus/internal/shared/id"
	"shopnexus/internal/shared/id/idtest"
)

const (
	ordID   = 1
	buyerID = 7
)

func TestMain(m *testing.M) { idtest.Install(); m.Run() }

type fakeRepo struct {
	saved *domain.Order
	byID  map[int64]domain.Order
}

func (f *fakeRepo) Save(_ context.Context, o *domain.Order) error {
	o.ID = ordID
	f.saved = o
	return nil
}

func (f *fakeRepo) FindByID(_ context.Context, oid int64) (domain.Order, error) {
	o, ok := f.byID[oid]
	if !ok {
		return domain.Order{}, domain.ErrOrderNotFound
	}
	return o, nil
}

func TestPlaceOrder(t *testing.T) {
	svc := order.NewService(&fakeRepo{byID: map[int64]domain.Order{}}, eventbus.NewMemory(nil), slog.Default())
	got, err := svc.PlaceOrder(context.Background(), orderapi.PlaceOrderRequest{BuyerID: id.Of[id.Account](buyerID), Total: 500})
	if err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}
	if got.ID != id.Of[id.Order](ordID) || got.Status != domain.StatusPending {
		t.Fatalf("unexpected order: %+v", got)
	}
}

func TestPlaceOrder_PublishesEvent(t *testing.T) {
	mem := eventbus.NewMemory(nil)
	var got []order.OrderPlaced
	eventbus.Subscribe(mem, order.OrderPlacedTopic, "test", func(_ context.Context, e order.OrderPlaced) error {
		got = append(got, e)
		return nil
	})

	svc := order.NewService(&fakeRepo{byID: map[int64]domain.Order{}}, mem, slog.Default())
	if _, err := svc.PlaceOrder(context.Background(), orderapi.PlaceOrderRequest{BuyerID: id.Of[id.Account](buyerID), Total: 500}); err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}
	mem.Wait()

	if len(got) != 1 || got[0].OrderID != ordID || got[0].BuyerID != buyerID || got[0].Total != 500 {
		t.Fatalf("unexpected published events: %+v", got)
	}
}

func TestGetOrder_NotFound(t *testing.T) {
	svc := order.NewService(&fakeRepo{byID: map[int64]domain.Order{}}, eventbus.NewMemory(nil), slog.Default())
	_, err := svc.GetOrder(context.Background(), orderapi.GetOrderRequest{ID: id.Of[id.Order](404)})
	if status, _, _, ok := errx.Decompose(err); !ok || status != 404 {
		t.Fatalf("expected NotFound, got %v", err)
	}
}
