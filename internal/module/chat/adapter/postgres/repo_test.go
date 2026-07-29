//go:build integration

package postgres_test

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"shopnexus/internal/module/chat/adapter/postgres"
	"shopnexus/internal/module/chat/domain"
)

func TestRepo_SaveAndListByConversation(t *testing.T) {
	dsn := os.Getenv("CHAT_DB_DSN")
	if dsn == "" {
		t.Skip("CHAT_DB_DSN not set")
	}

	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	defer pool.Close()

	repo := postgres.New(pool)

	// Save a message.
	m, err := domain.NewMessage("conv-1", "sender-1", "Hello world")
	if err != nil {
		t.Fatalf("NewMessage: %v", err)
	}
	if err := repo.Save(context.Background(), &m); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if m.ID == "" {
		t.Fatal("expected ID to be set after Save")
	}

	// List messages.
	msgs, err := repo.ListByConversation(context.Background(), "conv-1", 10, 0)
	if err != nil {
		t.Fatalf("ListByConversation: %v", err)
	}
	if len(msgs) == 0 {
		t.Fatal("expected at least one message")
	}
	if msgs[0].Body != "Hello world" {
		t.Fatalf("body = %q, want Hello world", msgs[0].Body)
	}
}
