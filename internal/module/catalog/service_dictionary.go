package catalog

import (
	"context"
	"fmt"

	catalogapi "shopnexus/internal/module/catalog/api"
	"shopnexus/internal/module/catalog/domain"
	"shopnexus/internal/shared/id"
)

// ListCategories answers the whole tree flat, unpaginated: a curated tree stays small,
// and a client assembles the shape from the parent reference on each row.
func (s *Service) ListCategories(ctx context.Context, req catalogapi.ListCategoriesRequest) ([]catalogapi.Category, error) {
	rows, err := s.repo.ListCategories(ctx)
	if err != nil {
		return nil, fmt.Errorf("list categories: %w", err)
	}
	out := make([]catalogapi.Category, 0, len(rows))
	for _, c := range rows {
		out = append(out, toAPICategory(c))
	}
	return out, nil
}

func (s *Service) AdminCreateCategory(ctx context.Context, req catalogapi.CreateCategoryRequest) (catalogapi.Category, error) {
	if err := s.requireAdmin(ctx, req.ActorID); err != nil {
		return catalogapi.Category{}, err
	}
	var parentID *int64
	if req.ParentID != nil {
		n := req.ParentID.Int64()
		parentID = &n
	}
	c, err := domain.NewCategory(req.Name, req.Description, parentID)
	if err != nil {
		return catalogapi.Category{}, err
	}
	if err := s.repo.CreateCategory(ctx, c); err != nil {
		return catalogapi.Category{}, fmt.Errorf("create category: %w", err)
	}
	return toAPICategory(*c), nil
}

// AdminUpdateCategory applies the patch onto the row and validates the result, so a rule
// is checked against what the category becomes rather than against the field.
func (s *Service) AdminUpdateCategory(ctx context.Context, req catalogapi.UpdateCategoryRequest) (catalogapi.Category, error) {
	if err := s.requireAdmin(ctx, req.ActorID); err != nil {
		return catalogapi.Category{}, err
	}
	current, err := s.category(ctx, req.ID.Int64())
	if err != nil {
		return catalogapi.Category{}, err
	}
	if req.Name != nil {
		current.Name = *req.Name
	}
	if req.Description != nil {
		current.Description = *req.Description
	}
	// Omitted leaves the parent, a value moves the node, the flag promotes it to a root.
	if req.ClearParentID {
		current.ParentID = nil
	} else if req.ParentID != nil {
		n := req.ParentID.Int64()
		current.ParentID = &n
	}
	if err := current.Validate(); err != nil {
		return catalogapi.Category{}, err
	}
	if err := s.repo.UpdateCategory(ctx, current); err != nil {
		return catalogapi.Category{}, fmt.Errorf("update category: %w", err)
	}
	return toAPICategory(current), nil
}

func (s *Service) AdminDeleteCategory(ctx context.Context, req catalogapi.DeleteCategoryRequest) error {
	if err := s.requireAdmin(ctx, req.ActorID); err != nil {
		return err
	}
	if err := s.repo.DeleteCategory(ctx, req.ID.Int64()); err != nil {
		return fmt.Errorf("delete category: %w", err)
	}
	return nil
}

// category reads one node out of the tree. The whole tree is one small read, so there is
// no second query shape to keep in step with it.
func (s *Service) category(ctx context.Context, categoryID int64) (domain.Category, error) {
	rows, err := s.repo.ListCategories(ctx)
	if err != nil {
		return domain.Category{}, fmt.Errorf("list categories: %w", err)
	}
	for _, c := range rows {
		if c.ID == categoryID {
			return c, nil
		}
	}
	return domain.Category{}, domain.ErrCategoryNotFound
}

func toAPICategory(c domain.Category) catalogapi.Category {
	out := catalogapi.Category{
		ID:          id.Of[id.Category](c.ID),
		Name:        c.Name,
		Description: c.Description,
	}
	if c.ParentID != nil {
		parent := id.Of[id.Category](*c.ParentID)
		out.ParentID = &parent
	}
	return out
}
