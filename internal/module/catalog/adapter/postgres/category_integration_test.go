//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"shopnexus/internal/infra/postgres"
	pgadapter "shopnexus/internal/module/catalog/adapter/postgres"
	"shopnexus/internal/module/catalog/domain"
)

// These exercise the SQL a fake cannot: the recursive cycle guard, the name UNIQUE and
// ON DELETE RESTRICT. They skip when no DSN is set, so `go test ./...` stays
// database-free.
func newRepo(t *testing.T) *pgadapter.Repo {
	t.Helper()
	dsn := os.Getenv("CATALOG_DB_DSN")
	if dsn == "" {
		t.Skip("CATALOG_DB_DSN not set")
	}
	pool, err := postgres.NewPool(context.Background(), dsn, "catalog")
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pgadapter.New(pool)
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
