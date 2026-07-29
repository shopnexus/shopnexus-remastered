//go:build integration

package postgres_test

import (
	"context"
	"os"
	"testing"

	"shopnexus/internal/infra/postgres"
	pgadapter "shopnexus/internal/module/account/adapter/postgres"
	"shopnexus/internal/module/account/domain"
)

func TestRepo_CreateAndFind(t *testing.T) {
	dsn := os.Getenv("ACCOUNT_DB_DSN")
	if dsn == "" {
		t.Skip("ACCOUNT_DB_DSN not set")
	}
	pool, err := postgres.NewPool(context.Background(), dsn, "account")
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer pool.Close()

	repo := pgadapter.New(pool)
	a := &domain.Account{Email: "int@test.com", PasswordHash: "h", Name: "Int"}
	if err := repo.Create(context.Background(), a); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := repo.FindByEmail(context.Background(), "int@test.com")
	if err != nil || got.ID == "" {
		t.Fatalf("FindByEmail: got=%v err=%v", got, err)
	}
}
