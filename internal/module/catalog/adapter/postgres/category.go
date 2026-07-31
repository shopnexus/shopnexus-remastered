package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"shopnexus/internal/module/catalog/domain"
)

const categoryColumns = `id, parent_id, name, description`

func (r *Repo) ListCategories(ctx context.Context) ([]domain.Category, error) {
	q := `SELECT ` + categoryColumns + ` FROM category ORDER BY name`
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

// UpdateCategory writes the row and moves it in one statement. The NOT EXISTS walks the
// subtree of the row being moved: if the new parent is inside it, the move would loop the
// tree, and no row is touched. Existence is checked first so that "no rows" has exactly
// one remaining meaning.
func (r *Repo) UpdateCategory(ctx context.Context, c domain.Category) error {
	exists, err := r.CategoryExists(ctx, c.ID)
	if err != nil {
		return err
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
	                 UNION ALL
	                 SELECT child.id FROM category child
	                   JOIN descendants d ON child.parent_id = d.id
	               )
	               SELECT 1 FROM descendants WHERE id = @parent_id::bigint
	             )`
	args := pgx.NamedArgs{
		"id": c.ID, "parent_id": c.ParentID, "name": c.Name, "description": c.Description,
	}
	tag, err := r.pool.Exec(ctx, q, args)
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
