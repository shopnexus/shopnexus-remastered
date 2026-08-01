package catalog_test

import (
	"context"
	"testing"

	catalogapi "shopnexus/internal/module/catalog/api"
	"shopnexus/internal/module/catalog/domain"
	"shopnexus/internal/shared/id"
)

// The service is a thin pass-through here on purpose: the rule lives in the statement's
// WHERE clause, so there is nothing for it to re-check and no listing to load.
func TestStockMovements(t *testing.T) {
	h := newHarness("user")
	ctx := context.Background()
	h.repo.stock[5] = domain.Stock{Quantity: 4}
	variantID := id.Of[id.Variant](5)

	if err := h.svc.ReserveStock(ctx, catalogapi.StockMovementRequest{VariantID: variantID, Units: 3}); err != nil {
		t.Fatalf("ReserveStock: %v", err)
	}
	if got := status(t, h.svc.ReserveStock(ctx, catalogapi.StockMovementRequest{
		VariantID: variantID, Units: 2,
	})); got != 409 {
		t.Fatalf("status = %d, want 409 for an oversell", got)
	}
	commit := catalogapi.StockCommitRequest{
		VariantID: variantID, Units: 1, IdempotencyKey: "order:1:item:1:commit",
	}
	if err := h.svc.CommitStock(ctx, commit); err != nil {
		t.Fatalf("CommitStock: %v", err)
	}
	// The same key again is the effect the caller asked for, already applied: `sold` never
	// comes back down on its own, so a retry that added to it would sell the units twice.
	if err := h.svc.CommitStock(ctx, commit); err != nil {
		t.Fatalf("second CommitStock: %v", err)
	}
	if err := h.svc.ReleaseStock(ctx, catalogapi.StockMovementRequest{VariantID: variantID, Units: 2}); err != nil {
		t.Fatalf("ReleaseStock: %v", err)
	}
	got, err := h.repo.FindStock(ctx, 5)
	if err != nil {
		t.Fatalf("FindStock: %v", err)
	}
	if got.Reserved != 0 || got.Sold != 1 || got.Available() != 3 {
		t.Fatalf("stock = %+v", got)
	}
}

// A sale is reversed by UncommitStock, and only by that: the units are in `sold` by then, so
// releasing would decrement a counter that holds none of them. Keyed apart from the commit, so
// the reversal is not swallowed as one that already happened.
func TestUncommitStock_ReversesTheSaleOnce(t *testing.T) {
	h := newHarness("user")
	ctx := context.Background()
	h.repo.stock[5] = domain.Stock{Quantity: 4}
	variantID := id.Of[id.Variant](5)

	if err := h.svc.ReserveStock(ctx, catalogapi.StockMovementRequest{VariantID: variantID, Units: 2}); err != nil {
		t.Fatalf("ReserveStock: %v", err)
	}
	if err := h.svc.CommitStock(ctx, catalogapi.StockCommitRequest{
		VariantID: variantID, Units: 2, IdempotencyKey: "order:1:item:1:commit",
	}); err != nil {
		t.Fatalf("CommitStock: %v", err)
	}
	reverse := catalogapi.StockCommitRequest{
		VariantID: variantID, Units: 2, IdempotencyKey: "order:1:item:1:uncommit",
	}
	if err := h.svc.UncommitStock(ctx, reverse); err != nil {
		t.Fatalf("UncommitStock: %v", err)
	}
	if err := h.svc.UncommitStock(ctx, reverse); err != nil {
		t.Fatalf("second UncommitStock: %v", err)
	}
	got, err := h.repo.FindStock(ctx, 5)
	if err != nil {
		t.Fatalf("FindStock: %v", err)
	}
	if got.Reserved != 0 || got.Sold != 0 || got.Available() != 4 {
		t.Fatalf("stock = %+v, want every unit back on the shelf exactly once", got)
	}
	// Nothing is left sold, so there is nothing left to reverse either.
	if got := status(t, h.svc.UncommitStock(ctx, catalogapi.StockCommitRequest{
		VariantID: variantID, Units: 1, IdempotencyKey: "order:1:item:1:uncommit-again",
	})); got != 409 {
		t.Fatalf("status = %d, want 409 reversing a sale that is not there", got)
	}
}
