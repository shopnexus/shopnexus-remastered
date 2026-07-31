package catalog

import (
	"context"
	"fmt"
	"strings"

	catalogapi "shopnexus/internal/module/catalog/api"
	"shopnexus/internal/module/catalog/domain"
	"shopnexus/internal/module/catalog/port"
	"shopnexus/internal/shared/errx"
	"shopnexus/internal/shared/id"
)

// ListCategories answers the whole tree flat, unpaginated: a curated tree stays small,
// and a client assembles the shape from the parent reference on each row.
func (s *Service) ListCategories(ctx context.Context, req catalogapi.ListCategoriesRequest) ([]catalogapi.Category, error) {
	// `near` answers a different question — which categories does this thing belong in — so
	// it returns a scored shortlist instead of the tree.
	if len(req.Near) > 0 {
		vectors, _, err := s.probes(ctx, req.Near)
		if err != nil {
			return nil, err
		}
		ranked, err := s.repo.NearestCategories(ctx, vectors, req.Limit)
		if err != nil {
			return nil, fmt.Errorf("rank categories: %w", err)
		}
		out := make([]catalogapi.Category, 0, len(ranked))
		for _, row := range ranked {
			c := toAPICategory(row.Category)
			score := row.Score
			c.Score = &score
			out = append(out, c)
		}
		return out, nil
	}
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

// ListTags pages the dictionary. Query and Near answer different questions, so asking both
// is a bad request rather than a silent precedence rule.
func (s *Service) ListTags(ctx context.Context, req catalogapi.ListTagsRequest) (catalogapi.TagPage, error) {
	if req.Query != "" && len(req.Near) > 0 {
		return catalogapi.TagPage{}, errx.NewValidationError("invalid field: near", errx.Field{
			Field:   "near",
			Rule:    "excluded_with",
			Message: "cannot be combined with q: one filters the dictionary, the other ranks it",
		})
	}
	if len(req.Near) > 0 {
		vectors, slugs, err := s.probes(ctx, req.Near)
		if err != nil {
			return catalogapi.TagPage{}, err
		}
		ranked, err := s.repo.NearestTags(ctx, vectors, slugs, offsetOf(req.Page, req.Limit), req.Limit)
		if err != nil {
			return catalogapi.TagPage{}, fmt.Errorf("rank tags: %w", err)
		}
		out := make([]catalogapi.Tag, 0, len(ranked))
		for _, row := range ranked {
			score := row.Score
			out = append(out, catalogapi.Tag{Slug: row.Tag.Slug, Description: row.Tag.Description, Score: &score})
		}
		// TotalCount stays nil: the top-K is all the ranking ever visited.
		return catalogapi.TagPage{
			Data: out,
			Meta: catalogapi.PageInfo{Page: req.Page, Limit: req.Limit},
		}, nil
	}
	rows, total, err := s.repo.ListTags(ctx, port.TagFilter{
		Prefix: req.Query,
		Offset: offsetOf(req.Page, req.Limit),
		Limit:  req.Limit,
	})
	if err != nil {
		return catalogapi.TagPage{}, fmt.Errorf("list tags: %w", err)
	}
	out := make([]catalogapi.Tag, 0, len(rows))
	for _, t := range rows {
		out = append(out, catalogapi.Tag{Slug: t.Slug, Description: t.Description})
	}
	return catalogapi.TagPage{
		Data: out,
		Meta: catalogapi.PageInfo{Page: req.Page, Limit: req.Limit, TotalCount: &total},
	}, nil
}

func (s *Service) AdminPutTag(ctx context.Context, req catalogapi.PutTagRequest) (catalogapi.Tag, error) {
	if err := s.requireAdmin(ctx, req.ActorID); err != nil {
		return catalogapi.Tag{}, err
	}
	t, err := domain.NewTag(req.Slug, req.Description)
	if err != nil {
		return catalogapi.Tag{}, err
	}
	if err := s.repo.PutTag(ctx, *t); err != nil {
		return catalogapi.Tag{}, fmt.Errorf("put tag: %w", err)
	}
	return catalogapi.Tag{Slug: t.Slug, Description: t.Description}, nil
}

func (s *Service) AdminDeleteTag(ctx context.Context, req catalogapi.DeleteTagRequest) error {
	if err := s.requireAdmin(ctx, req.ActorID); err != nil {
		return err
	}
	// The slug comes off the path unparsed, so its shape is checked here rather than by a
	// failed lookup.
	if err := domain.ValidateTagSlug(req.Slug); err != nil {
		return err
	}
	if err := s.repo.DeleteTag(ctx, req.Slug); err != nil {
		return fmt.Errorf("delete tag: %w", err)
	}
	return nil
}

// offsetOf turns a 1-based page into an offset. Page and limit are validated at the DTO, so
// this needs no bounds of its own.
func offsetOf(page, limit int) int { return (page - 1) * limit }

// probes turns the wire seeds into probe vectors. It also answers the tag slugs among them,
// because a tag ranking excludes its own seeds.
//
// A seed is a tag slug or a category id, told apart the same way a polymorphic ref is: an
// opaque id always carries an underscore after its prefix and a slug never does. A repeated
// seed is one seed — a picker that does not dedupe its own chips must not get a 422.
func (s *Service) probes(ctx context.Context, raw []string) ([]port.Vector, []string, error) {
	var (
		seeds  []port.Seed
		labels []string
		slugs  []string
		seen   = make(map[string]bool, len(raw))
	)
	for _, value := range raw {
		if seen[value] {
			continue
		}
		seen[value] = true
		if !strings.Contains(value, "_") {
			if err := domain.ValidateTagSlug(value); err != nil {
				return nil, nil, err
			}
			seeds = append(seeds, port.Seed{TagSlug: value})
			slugs = append(slugs, value)
			labels = append(labels, value)
			continue
		}
		categoryID, err := id.Parse[id.Category](value)
		if err != nil {
			return nil, nil, err
		}
		seeds = append(seeds, port.Seed{CategoryID: categoryID.Int64()})
		labels = append(labels, value)
	}
	vectors, err := s.repo.SeedVectors(ctx, seeds)
	if err != nil {
		return nil, nil, fmt.Errorf("read seed vectors: %w", err)
	}
	// One vector per seed, so a missing one is named rather than dropped: ranking against the
	// rest would answer a different question than the caller asked.
	for i, v := range vectors {
		if v == nil {
			return nil, nil, domain.ErrSeedNotEmbedded.Fmt(labels[i])
		}
	}
	return vectors, slugs, nil
}
