package catalog

import (
	"testing"

	"shopnexus/internal/module/catalog/domain"
)

// The knowledge base is also the resolver: the set that was shown to the model is the set that
// resolves, so a model cannot be blamed for naming something it was never offered. Matching is
// case- and space-insensitive, which is the only way a copy goes wrong when the list was in front
// of it.
func TestKnowledge_ResolvesOnlyWhatItShowed(t *testing.T) {
	kb := knowledge{
		Categories: []domain.Category{{ID: 42, Name: "Áo nam"}},
		Tags:       []string{"uniqlo"},
	}
	if id, ok := kb.CategoryID("  áo   NAM "); !ok || id != 42 {
		t.Errorf("CategoryID = %d, %v; want the category it showed", id, ok)
	}
	if _, ok := kb.CategoryID("Không có thật"); ok {
		t.Error("a category nobody defined resolved")
	}
	if slug, ok := kb.TagSlug("UNIQLO"); !ok || slug != "uniqlo" {
		t.Errorf("TagSlug = %q, %v; want the tag it showed", slug, ok)
	}
	if _, ok := kb.TagSlug("khong-co"); ok {
		t.Error("a tag nobody defined resolved")
	}
}
