//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"

	"shopnexus/internal/module/catalog/domain"
)

// The whole lifecycle of a unit: reserved by a checkout, then either committed to a sale or
// released back. cached_sold moves only on the commit.
func TestRepo_StockLifecycle(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	category := createCategory(t, repo, unique("cat-"), nil)
	createTag(t, repo, "handmade", nil)
	l := newListingFor(t, repo, category.ID, unique("Listing "))
	variantID := l.Variants[0].ID // quantity 5

	if err := repo.ReserveStock(ctx, variantID, 2); err != nil {
		t.Fatalf("ReserveStock: %v", err)
	}
	got, err := repo.FindStock(ctx, variantID)
	if err != nil {
		t.Fatalf("FindStock: %v", err)
	}
	if got.Reserved != 2 || got.Sold != 0 || got.Available() != 3 {
		t.Fatalf("stock = %+v", got)
	}

	if err := repo.CommitStock(ctx, variantID, 1); err != nil {
		t.Fatalf("CommitStock: %v", err)
	}
	if err := repo.ReleaseStock(ctx, variantID, 1); err != nil {
		t.Fatalf("ReleaseStock: %v", err)
	}
	got, err = repo.FindStock(ctx, variantID)
	if err != nil {
		t.Fatalf("FindStock: %v", err)
	}
	if got.Reserved != 0 || got.Sold != 1 || got.Available() != 4 {
		t.Fatalf("stock = %+v, want one sold and nothing held", got)
	}

	// The sale is on the listing's counter too, in the same transaction as the commit.
	var cachedSold int64
	if err := poolOf(t).QueryRow(ctx, `SELECT cached_sold FROM listing WHERE id = $1`, l.ID).
		Scan(&cachedSold); err != nil {
		t.Fatalf("read cached_sold: %v", err)
	}
	if cachedSold != 1 {
		t.Fatalf("cached_sold = %d, want 1", cachedSold)
	}
}

// The guard: a reservation that would oversell is refused, and the row is untouched.
func TestRepo_ReserveStockRefusesAnOversell(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	category := createCategory(t, repo, unique("cat-"), nil)
	createTag(t, repo, "handmade", nil)
	l := newListingFor(t, repo, category.ID, unique("Listing "))
	variantID := l.Variants[0].ID // quantity 5

	if err := repo.ReserveStock(ctx, variantID, 6); !errors.Is(err, domain.ErrInsufficientStock) {
		t.Fatalf("ReserveStock = %v, want ErrInsufficientStock", err)
	}
	got, _ := repo.FindStock(ctx, variantID)
	if got.Reserved != 0 {
		t.Fatalf("stock = %+v, want it untouched", got)
	}

	// Committing more than is held is the same class of refusal.
	if err := repo.ReserveStock(ctx, variantID, 2); err != nil {
		t.Fatalf("ReserveStock: %v", err)
	}
	if err := repo.CommitStock(ctx, variantID, 3); !errors.Is(err, domain.ErrInsufficientStock) {
		t.Fatalf("CommitStock = %v, want ErrInsufficientStock", err)
	}
	if err := repo.ReleaseStock(ctx, variantID, 3); !errors.Is(err, domain.ErrInsufficientStock) {
		t.Fatalf("ReleaseStock = %v, want ErrInsufficientStock", err)
	}
}

// A missing variant and a variant with no room both affect zero rows, and a caller does the
// same thing about either — stop. Only FindStock, which has to answer something, tells them
// apart.
func TestRepo_StockUnknownVariant(t *testing.T) {
	repo := newRepo(t)
	if _, err := repo.FindStock(context.Background(), 0); !errors.Is(err, domain.ErrVariantNotFound) {
		t.Fatalf("FindStock = %v, want ErrVariantNotFound", err)
	}
	if err := repo.ReserveStock(context.Background(), 0, 1); !errors.Is(err, domain.ErrInsufficientStock) {
		t.Fatalf("ReserveStock = %v, want ErrInsufficientStock", err)
	}
}
