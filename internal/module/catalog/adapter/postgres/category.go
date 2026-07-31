package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"shopnexus/internal/module/catalog/domain"
)

const categoryColumns = `id, parent_id, name, description`

func (r *Repo) ListCategories(ctx context.Context) ([]domain.Category, error) {
	const q = `SELECT ` + categoryColumns + ` FROM category ORDER BY name`
	rows, err := r.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("db query categories: %w", err)
	}
	defer rows.Close()

	var out []domain.Category
	for rows.Next() {
		var c domain.Category
		if err := rows.Scan(&c.ID, &c.ParentID, &c.Name, &c.Description); err != nil {
			return nil, fmt.Errorf("db scan category row: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db iterate categories: %w", err)
	}
	return out, nil
}

func (r *Repo) CategoryExists(ctx context.Context, id int64) (bool, error) {
	var ok bool
	err := r.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM category WHERE id = @id)`,
		pgx.NamedArgs{"id": id}).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("db query category exists: %w", err)
	}
	return ok, nil
}

func (r *Repo) CreateCategory(ctx context.Context, c *domain.Category) error {
	const q = `INSERT INTO category (parent_id, name, description)
	           VALUES (@parent_id, @name, @description)
	           RETURNING id`
	args := pgx.NamedArgs{"parent_id": c.ParentID, "name": c.Name, "description": c.Description}
	if err := r.pool.QueryRow(ctx, q, args).Scan(&c.ID); err != nil {
		if isUniqueViolation(err) {
			return domain.ErrCategoryNameTaken
		}
		if isRestrictViolation(err) {
			return domain.ErrCategoryNotFound // the named parent does not exist
		}
		return fmt.Errorf("db insert category: %w", err)
	}
	return nil
}

// categoryTreeLock is the advisory-lock key every re-parent takes. A cycle guard that reads
// the tree and then writes it is write skew waiting to happen: two concurrent moves each read
// a tree in which their own move is legal, and together they close a loop. One transaction-
// scoped lock serialises re-parents against each other and against nothing else — reads of
// the tree are far more frequent and never take it. The constant is arbitrary but permanent.
const categoryTreeLock = 8_070_101

// UpdateCategory writes the row and moves it in one statement, under the tree lock. The NOT
// EXISTS walks the subtree of the row being moved: if the new parent is inside it, the move
// would loop the tree, and no row is touched.
//
// UNION, not UNION ALL: with UNION ALL a recursive walk over a tree that already contains a
// cycle never terminates, and no statement_timeout is set — so one bad row would burn a
// backend on every later attempt to repair it, including through this route.
func (r *Repo) UpdateCategory(ctx context.Context, c domain.Category) error {
	return r.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(@key)`,
			pgx.NamedArgs{"key": int64(categoryTreeLock)}); err != nil {
			return fmt.Errorf("db lock category tree: %w", err)
		}
		// Read after the lock: a not-found answered from before it could be stale.
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM category WHERE id = @id)`,
			pgx.NamedArgs{"id": c.ID}).Scan(&exists); err != nil {
			return fmt.Errorf("db query category exists: %w", err)
		}
		if !exists {
			return domain.ErrCategoryNotFound
		}
		const q = `UPDATE category
		           SET parent_id = @parent_id, name = @name, description = @description
		           WHERE id = @id
		             AND (@parent_id::bigint IS NULL OR @parent_id::bigint <> @id)
		             AND NOT EXISTS (
		               WITH RECURSIVE descendants AS (
		                 SELECT id FROM category WHERE id = @id
		                 UNION
		                 SELECT child.id FROM category child
		                   JOIN descendants d ON child.parent_id = d.id
		               )
		               SELECT 1 FROM descendants WHERE id = @parent_id::bigint
		             )`
		args := pgx.NamedArgs{
			"id": c.ID, "parent_id": c.ParentID, "name": c.Name, "description": c.Description,
		}
		tag, err := tx.Exec(ctx, q, args)
		if err != nil {
			if isUniqueViolation(err) {
				return domain.ErrCategoryNameTaken
			}
			if isRestrictViolation(err) {
				return domain.ErrCategoryNotFound // the named parent does not exist
			}
			return fmt.Errorf("db update category: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrCategoryCycle
		}
		return nil
	})
}

// DeleteCategory leans on the schema for both outcomes: children are promoted to roots by
// ON DELETE SET NULL, and a category still holding listings is refused by RESTRICT.
func (r *Repo) DeleteCategory(ctx context.Context, id int64) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM category WHERE id = @id`, pgx.NamedArgs{"id": id})
	if err != nil {
		if isRestrictViolation(err) {
			return domain.ErrCategoryInUse
		}
		return fmt.Errorf("db delete category: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrCategoryNotFound
	}
	return nil
}
