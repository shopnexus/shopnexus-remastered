package gwctx_test

import (
	"context"
	"testing"

	"shopnexus/internal/gateway/gwctx"
	"shopnexus/internal/shared/id"
)

func TestUserID_RoundTrip(t *testing.T) {
	want := id.Of[id.Account](42)
	ctx := gwctx.WithUserID(context.Background(), want)
	got, ok := gwctx.UserID(ctx)
	if !ok || got != want {
		t.Fatalf("UserID = %d, %v; want %d, true", got, ok, want)
	}
}

func TestUserID_Missing(t *testing.T) {
	if _, ok := gwctx.UserID(context.Background()); ok {
		t.Fatal("expected ok=false when no user id")
	}
}

func TestRequestID_RoundTrip(t *testing.T) {
	ctx := gwctx.WithRequestID(context.Background(), "req-1")
	if gwctx.RequestID(ctx) != "req-1" {
		t.Fatal("request id not round-tripped")
	}
}
