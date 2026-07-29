//go:build integration

package postgres_test

import (
	"context"
	"os"
	"testing"

	"shopnexus/internal/infra/postgres"
	pgadapter "shopnexus/internal/module/catalog/adapter/postgres"
	"shopnexus/internal/module/catalog/domain"
)

func TestRepo_SaveAndFind(t *testing.T) {
	dsn := os.Getenv("CATALOG_DB_DSN")
	if dsn == "" {
		t.Skip("CATALOG_DB_DSN not set")
	}
	pool, err := postgres.NewPool(context.Background(), dsn, "catalog")
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer pool.Close()

	repo := pgadapter.New(pool)
	l := &domain.Listing{OwnerID: "00000000-0000-0000-0000-000000000001", Title: "Int", Price: 100, Status: domain.StatusActive}
	if err := repo.Save(context.Background(), l); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := repo.FindByID(context.Background(), l.ID)
	if err != nil || got.ID == "" {
		t.Fatalf("FindByID: got=%v err=%v", got, err)
	}
}
