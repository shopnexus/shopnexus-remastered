//go:build integration

package postgres_test

import (
	"context"
	"os"
	"testing"

	"shopnexus/internal/infra/postgres"
	pgadapter "shopnexus/internal/module/order/adapter/postgres"
	"shopnexus/internal/module/order/domain"
)

func TestRepo_SaveAndFind(t *testing.T) {
	dsn := os.Getenv("ORDER_DB_DSN")
	if dsn == "" {
		t.Skip("ORDER_DB_DSN not set")
	}
	pool, err := postgres.NewPool(context.Background(), dsn, "order")
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer pool.Close()

	repo := pgadapter.New(pool)
	o := &domain.Order{BuyerID: "00000000-0000-0000-0000-000000000001", Total: 100, Status: domain.StatusPending}
	if err := repo.Save(context.Background(), o); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := repo.FindByID(context.Background(), o.ID)
	if err != nil || got.ID == "" {
		t.Fatalf("FindByID: got=%v err=%v", got, err)
	}
}
