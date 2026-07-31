//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopnexus/internal/infra/postgres"
	pgadapter "shopnexus/internal/module/catalog/adapter/postgres"
	"shopnexus/internal/module/catalog/domain"
)

// These exercise the SQL a fake cannot: the recursive cycle guard, the name UNIQUE and
// ON DELETE RESTRICT. They skip when no DSN is set, so `go test ./...` stays
// database-free.
func testDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("CATALOG_DB_DSN")
	if dsn == "" {
		t.Skip("CATALOG_DB_DSN not set")
	}
	return dsn
}

func newRepo(t *testing.T) *pgadapter.Repo {
	t.Helper()
	pool, err := postgres.NewPool(context.Background(), testDSN(t), "catalog")
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pgadapter.New(pool)
}

// poolOf opens a pool for the assertions that read or write a column no port method exposes —
// a listing row, an embedding vector. Closed with the test.
func poolOf(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := postgres.NewPool(context.Background(), testDSN(t), "catalog")
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// insertListing writes a listing referencing categoryID directly with SQL: the listing
// aggregate has no Go repository yet, and this is the only way to produce the row that
// makes "listing_category_id_fkey" (ON DELETE RESTRICT) fire.
func insertListing(t *testing.T, categoryID int64) {
	t.Helper()
	pool := poolOf(t)
	const q = `INSERT INTO listing
	           (slug, account_id, category_id, name, description, specifications,
	            price_mode, condition, shipping_paid_by, currency)
	           VALUES (@slug, @account_id, @category_id, @name, @description, @specifications::jsonb,
	                   @price_mode, @condition, @shipping_paid_by, @currency)`
	args := pgx.NamedArgs{
		"slug":             unique("listing-"),
		"account_id":       int64(1),
		"category_id":      categoryID,
		"name":             "test listing",
		"description":      "",
		"specifications":   `{}`,
		"price_mode":       "fixed",
		"condition":        "new",
		"shipping_paid_by": "seller",
		"currency":         "USD",
	}
	if _, err := pool.Exec(context.Background(), q, args); err != nil {
		t.Fatalf("insert listing: %v", err)
	}
}

// unique keeps repeated runs against the same database apart.
func unique(prefix string) string {
	return prefix + time.Now().Format("150405.000000000")
}

func createCategory(t *testing.T, repo *pgadapter.Repo, name string, parent *int64) *domain.Category {
	t.Helper()
	c, err := domain.NewCategory(name, "", parent)
	if err != nil {
		t.Fatalf("NewCategory: %v", err)
	}
	if err := repo.CreateCategory(context.Background(), c); err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	return c
}

// The rule the guard exists for: a node cannot be moved under its own descendant, which
// would detach the whole branch from the tree and loop it.
func TestRepo_CategoryMoveRefusesACycle(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	root := createCategory(t, repo, unique("root-"), nil)
	child := createCategory(t, repo, unique("child-"), &root.ID)
	grandchild := createCategory(t, repo, unique("grand-"), &child.ID)

	moved := *root
	moved.ParentID = &grandchild.ID
	if err := repo.UpdateCategory(ctx, moved); !errors.Is(err, domain.ErrCategoryCycle) {
		t.Fatalf("UpdateCategory = %v, want ErrCategoryCycle", err)
	}
	// A legal move still works, so the guard is not simply refusing everything.
	sibling := createCategory(t, repo, unique("sib-"), nil)
	moved = *sibling
	moved.ParentID = &child.ID
	if err := repo.UpdateCategory(ctx, moved); err != nil {
		t.Fatalf("legal move: %v", err)
	}
}

func TestRepo_CategoryNameIsUnique(t *testing.T) {
	repo := newRepo(t)
	name := unique("dup-")
	createCategory(t, repo, name, nil)

	dup, err := domain.NewCategory(name, "", nil)
	if err != nil {
		t.Fatalf("NewCategory: %v", err)
	}
	if err := repo.CreateCategory(context.Background(), dup); !errors.Is(err, domain.ErrCategoryNameTaken) {
		t.Fatalf("CreateCategory = %v, want ErrCategoryNameTaken", err)
	}
}

// The mapping DeleteCategory rests on: ON DELETE RESTRICT refuses to orphan a listing
// still pointing at the category, and the adapter turns that into ErrCategoryInUse rather
// than a bare driver error.
func TestRepo_CategoryInUseRefusesDelete(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	cat := createCategory(t, repo, unique("used-"), nil)
	insertListing(t, cat.ID)

	if err := repo.DeleteCategory(ctx, cat.ID); !errors.Is(err, domain.ErrCategoryInUse) {
		t.Fatalf("DeleteCategory = %v, want ErrCategoryInUse", err)
	}
}

// The mapping UpdateCategory rests on: moving a category under a parent id that does not
// exist trips "category_parent_id_fkey", which the adapter reports as ErrCategoryNotFound.
func TestRepo_CategoryMoveRefusesUnknownParent(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	cat := createCategory(t, repo, unique("orphan-"), nil)

	unknown := int64(-1)
	moved := *cat
	moved.ParentID = &unknown
	if err := repo.UpdateCategory(ctx, moved); !errors.Is(err, domain.ErrCategoryNotFound) {
		t.Fatalf("UpdateCategory = %v, want ErrCategoryNotFound", err)
	}
}

func TestRepo_CategoryNotFound(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	if err := repo.DeleteCategory(ctx, 0); !errors.Is(err, domain.ErrCategoryNotFound) {
		t.Fatalf("DeleteCategory = %v, want ErrCategoryNotFound", err)
	}
	missing := domain.Category{ID: 0, Name: "x"}
	if err := repo.UpdateCategory(ctx, missing); !errors.Is(err, domain.ErrCategoryNotFound) {
		t.Fatalf("UpdateCategory = %v, want ErrCategoryNotFound", err)
	}
}
