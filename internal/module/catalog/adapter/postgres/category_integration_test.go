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
	            price_mode, condition, currency)
	           VALUES (@slug, @account_id, @category_id, @name, @description, @specifications::jsonb,
	                   @price_mode, @condition, @currency)`
	args := pgx.NamedArgs{
		"slug":           unique("listing-"),
		"account_id":     int64(1),
		"category_id":    categoryID,
		"name":           "test listing",
		"description":    "",
		"specifications": `{}`,
		"price_mode":     "fixed",
		"condition":      "new",
		"currency":       "USD",
	}
	var listingID int64
	if err := pool.QueryRow(context.Background(), q+` RETURNING id`, args).Scan(&listingID); err != nil {
		t.Fatalf("insert listing: %v", err)
	}
	// Registered after the category, so it is deleted before it, and RESTRICT stops blocking.
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM listing WHERE id = @id`,
			pgx.NamedArgs{"id": listingID}); err != nil {
			t.Logf("cleanup listing %d: %v", listingID, err)
		}
	})
}

// unique keeps repeated runs against the same database apart.
func unique(prefix string) string {
	return prefix + time.Now().Format("150405.000000000")
}

// createCategory registers its own cleanup. The schema is shared across runs, so a test that
// leaves rows behind changes what the next run reads — a ranking with a leftover perfect match
// ties, and the tie order is arbitrary.
func createCategory(t *testing.T, repo *pgadapter.Repo, name string, parent *int64) *domain.Category {
	t.Helper()
	c, err := domain.NewCategory(name, "", parent)
	if err != nil {
		t.Fatalf("NewCategory: %v", err)
	}
	if err := repo.CreateCategory(context.Background(), c); err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	// Cleanups run last-registered first, so a child goes before its parent and a listing
	// before the category RESTRICT would otherwise hold.
	t.Cleanup(func() {
		if err := repo.DeleteCategory(context.Background(), c.ID); err != nil {
			t.Logf("cleanup category %d: %v", c.ID, err)
		}
	})
	return c
}

// createTag is the same bargain for the dictionary: the row goes away with the test, and its
// embedding cascades with it.
func createTag(t *testing.T, repo *pgadapter.Repo, slug string, description *string) domain.Tag {
	t.Helper()
	tag, err := domain.NewTag(slug, description)
	if err != nil {
		t.Fatalf("NewTag: %v", err)
	}
	if err := repo.PutTag(context.Background(), *tag); err != nil {
		t.Fatalf("PutTag: %v", err)
	}
	t.Cleanup(func() {
		if err := repo.DeleteTag(context.Background(), tag.Slug); err != nil && !errors.Is(err, domain.ErrTagNotFound) {
			t.Logf("cleanup tag %q: %v", tag.Slug, err)
		}
	})
	return *tag
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

// Two concurrent re-parents of each other's node is the write skew a read-then-write cycle
// guard cannot see on its own: under READ COMMITTED each statement's subquery reads a tree in
// which its own move is legal, so both land and the tree comes out looped. The advisory lock is
// what makes one of them lose.
//
// One attempt is not enough to observe it — the window is the statement itself, and the first
// pair of goroutines rarely overlaps. Unguarded, this loop closes a loop on nearly every
// iteration; guarded, on none.
func TestRepo_ConcurrentMovesCannotCreateACycle(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	pool := poolOf(t)

	const attempts = 50
	for i := 0; i < attempts; i++ {
		a := createCategory(t, repo, unique("race-a-"), nil)
		b := createCategory(t, repo, unique("race-b-"), nil)

		// a under b and b under a, at the same time.
		done := make(chan error, 2)
		start := make(chan struct{})
		for _, move := range []struct{ child, parent *domain.Category }{{a, b}, {b, a}} {
			go func() {
				<-start
				c := *move.child
				c.ParentID = &move.parent.ID
				done <- repo.UpdateCategory(ctx, c)
			}()
		}
		close(start)
		for range 2 {
			if err := <-done; err != nil && !errors.Is(err, domain.ErrCategoryCycle) {
				t.Fatalf("UpdateCategory: %v", err)
			}
		}

		parentOf := func(id int64) *int64 {
			var parent *int64
			if err := pool.QueryRow(ctx, `SELECT parent_id FROM category WHERE id = @id`,
				pgx.NamedArgs{"id": id}).Scan(&parent); err != nil {
				t.Fatalf("read parent: %v", err)
			}
			return parent
		}
		pa, pb := parentOf(a.ID), parentOf(b.ID)
		if pa != nil && pb != nil && *pa == b.ID && *pb == a.ID {
			// Break it, or the cleanup cannot delete either row.
			if _, err := pool.Exec(ctx, `UPDATE category SET parent_id = NULL WHERE id = @id`,
				pgx.NamedArgs{"id": a.ID}); err != nil {
				t.Fatalf("unloop: %v", err)
			}
			t.Fatalf("attempt %d: both moves landed, so %d and %d are each other's parent", i, a.ID, b.ID)
		}
		// The tree stays walkable, which a cycle plus UNION ALL would not: the guard's
		// recursive walk would never terminate.
		root := *a
		root.ParentID = nil
		if err := repo.UpdateCategory(ctx, root); err != nil {
			t.Fatalf("attempt %d: moving back to a root failed: %v", i, err)
		}
	}
}
