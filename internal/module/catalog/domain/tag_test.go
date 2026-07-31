package domain_test

import (
	"strings"
	"testing"

	"shopnexus/internal/module/catalog/domain"
)

// A tag's id is its slug, so the shape is a contract rather than decoration: it appears in
// a URL, in a listing body and as a semantic seed.
func TestValidateTagSlug(t *testing.T) {
	for _, ok := range []string{"handmade", "eco-friendly", "size-42", "a1"} {
		if err := domain.ValidateTagSlug(ok); err != nil {
			t.Errorf("ValidateTagSlug(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"", "Handmade", "eco friendly", "eco_friendly", "-lead", "trail-", "double--dash"} {
		if err := domain.ValidateTagSlug(bad); err == nil {
			t.Errorf("ValidateTagSlug(%q) = nil, want an error", bad)
		}
	}
	// The column is VARCHAR(100), so the length is part of the shape.
	if err := domain.ValidateTagSlug(strings.Repeat("a", 101)); err == nil {
		t.Error("a 101-character slug was accepted")
	}
}

func TestNewTag(t *testing.T) {
	desc := "  Made by hand  "
	tag, err := domain.NewTag("Handmade", &desc)
	if err != nil {
		t.Fatalf("NewTag: %v", err)
	}
	if tag.Slug != "handmade" {
		t.Errorf("slug = %q, want it lowercased", tag.Slug)
	}
	if tag.Description == nil || *tag.Description != "Made by hand" {
		t.Errorf("description = %v, want it trimmed", tag.Description)
	}
}

// An empty description is absent, not an empty string: one representation of "not set".
func TestNewTag_BlankDescriptionIsAbsent(t *testing.T) {
	for _, blank := range []*string{nil, new(""), new("   ")} {
		tag, err := domain.NewTag("handmade", blank)
		if err != nil {
			t.Fatalf("NewTag: %v", err)
		}
		if tag.Description != nil {
			t.Errorf("description = %q, want nil", *tag.Description)
		}
	}
}

func TestNewTag_Rejects(t *testing.T) {
	if _, err := domain.NewTag("Not A Slug", nil); err == nil {
		t.Error("a malformed slug was accepted")
	}
	long := strings.Repeat("d", 256)
	if _, err := domain.NewTag("handmade", &long); err == nil {
		t.Error("a 256-character description was accepted")
	}
}
