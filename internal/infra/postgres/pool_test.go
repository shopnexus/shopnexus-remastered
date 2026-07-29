package postgres_test

import (
	"context"
	"testing"

	"shopnexus/internal/infra/postgres"
)

func TestNewPool_EmptyDSNFails(t *testing.T) {
	if _, err := postgres.NewPool(context.Background(), "", "account"); err == nil {
		t.Fatal("expected error for empty DSN")
	}
}
