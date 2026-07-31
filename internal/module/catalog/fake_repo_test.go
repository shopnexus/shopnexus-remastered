package catalog_test

import (
	"context"
	"slices"
	"strings"

	"shopnexus/internal/module/catalog/domain"
	"shopnexus/internal/module/catalog/port"
)

// fakeRepo is an in-memory port.Repository. It enforces the constraints the schema does —
// the unique name, the cycle guard, RESTRICT on a category in use — because those are the
// ones the service's behaviour is built on top of.
type fakeRepo struct {
	nextID     int64
	categories map[int64]domain.Category
	// inUse marks a category a listing references, which the fake cannot derive because
	// listings are not in this slice.
	inUse map[int64]bool
	tags  map[string]domain.Tag
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		categories: map[int64]domain.Category{},
		inUse:      map[int64]bool{},
		tags:       map[string]domain.Tag{},
	}
}

func (f *fakeRepo) id() int64 {
	f.nextID++
	return f.nextID
}

var _ port.Repository = (*fakeRepo)(nil)

func (f *fakeRepo) ListCategories(context.Context) ([]domain.Category, error) {
	out := make([]domain.Category, 0, len(f.categories))
	for _, c := range f.categories {
		out = append(out, c)
	}
	slices.SortFunc(out, func(a, b domain.Category) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

func (f *fakeRepo) CategoryExists(_ context.Context, id int64) (bool, error) {
	_, ok := f.categories[id]
	return ok, nil
}

func (f *fakeRepo) CreateCategory(_ context.Context, c *domain.Category) error {
	if f.nameTaken(c.Name, 0) {
		return domain.ErrCategoryNameTaken
	}
	if c.ParentID != nil {
		if _, ok := f.categories[*c.ParentID]; !ok {
			return domain.ErrCategoryNotFound
		}
	}
	c.ID = f.id()
	f.categories[c.ID] = *c
	return nil
}

func (f *fakeRepo) UpdateCategory(_ context.Context, c domain.Category) error {
	if _, ok := f.categories[c.ID]; !ok {
		return domain.ErrCategoryNotFound
	}
	if f.nameTaken(c.Name, c.ID) {
		return domain.ErrCategoryNameTaken
	}
	if c.ParentID != nil {
		if _, ok := f.categories[*c.ParentID]; !ok {
			return domain.ErrCategoryNotFound
		}
		if f.isDescendant(*c.ParentID, c.ID) {
			return domain.ErrCategoryCycle
		}
	}
	f.categories[c.ID] = c
	return nil
}

func (f *fakeRepo) DeleteCategory(_ context.Context, id int64) error {
	if _, ok := f.categories[id]; !ok {
		return domain.ErrCategoryNotFound
	}
	if f.inUse[id] {
		return domain.ErrCategoryInUse
	}
	// ON DELETE SET NULL: children are promoted to roots.
	for childID, child := range f.categories {
		if child.ParentID != nil && *child.ParentID == id {
			child.ParentID = nil
			f.categories[childID] = child
		}
	}
	delete(f.categories, id)
	return nil
}

func (f *fakeRepo) nameTaken(name string, self int64) bool {
	for id, c := range f.categories {
		if id != self && c.Name == name {
			return true
		}
	}
	return false
}

// isDescendant walks up from candidate: reaching root means it is not under id.
func (f *fakeRepo) isDescendant(candidate, id int64) bool {
	for at := candidate; ; {
		if at == id {
			return true
		}
		c, ok := f.categories[at]
		if !ok || c.ParentID == nil {
			return false
		}
		at = *c.ParentID
	}
}

// --- tags ---

func (f *fakeRepo) ListTags(_ context.Context, filter port.TagFilter) ([]domain.Tag, int64, error) {
	var matched []domain.Tag
	for _, t := range f.tags {
		if filter.Prefix != "" && !strings.HasPrefix(t.Slug, filter.Prefix) {
			continue
		}
		matched = append(matched, t)
	}
	slices.SortFunc(matched, func(a, b domain.Tag) int { return strings.Compare(a.Slug, b.Slug) })
	total := int64(len(matched))
	if filter.Offset >= len(matched) {
		return nil, total, nil
	}
	return matched[filter.Offset:min(filter.Offset+filter.Limit, len(matched))], total, nil
}

// PutTag is an upsert, as the ON CONFLICT in the adapter is.
func (f *fakeRepo) PutTag(_ context.Context, t domain.Tag) error {
	f.tags[t.Slug] = t
	return nil
}

func (f *fakeRepo) DeleteTag(_ context.Context, slug string) error {
	if _, ok := f.tags[slug]; !ok {
		return domain.ErrTagNotFound
	}
	delete(f.tags, slug)
	return nil
}
