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
	if err := h.svc.CommitStock(ctx, catalogapi.StockMovementRequest{VariantID: variantID, Units: 1}); err != nil {
		t.Fatalf("CommitStock: %v", err)
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
