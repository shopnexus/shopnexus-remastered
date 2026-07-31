package domain

import (
	"regexp"
	"strings"

	"shopnexus/internal/shared/errx"
)

// tagSlugRe is the wire contract for a tag id: lowercase kebab-case, and the column's
// primary key. No leading, trailing or doubled dash, so one label has one spelling.
var tagSlugRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// maxTagDescription mirrors the column's VARCHAR(255).
const maxTagDescription = 255

// Tag is a free-form label on a listing. Its id is the slug — a natural key, so it is
// readable on the wire rather than encoded, and renaming one cascades into the join.
type Tag struct {
	Slug        string
	Description *string
}

func NewTag(slug string, description *string) (*Tag, error) {
	slug = strings.ToLower(strings.TrimSpace(slug))
	if err := ValidateTagSlug(slug); err != nil {
		return nil, err
	}
	t := &Tag{Slug: slug}
	// An empty description is the same fact as no description, so it collapses to nil.
	if description != nil {
		if trimmed := strings.TrimSpace(*description); trimmed != "" {
			t.Description = &trimmed
		}
	}
	if t.Description != nil && len(*t.Description) > maxTagDescription {
		return nil, errx.NewValidationError("invalid field: description", errx.Field{
			Field: "description", Rule: "max", Message: "must be at most 255 characters",
		})
	}
	return t, nil
}

// ValidateTagSlug guards the shape "tag_id_slug_check" enforces and the path parameter carries.
func ValidateTagSlug(slug string) error {
	if tagSlugRe.MatchString(slug) && len(slug) <= 100 {
		return nil
	}
	return errx.NewValidationError("invalid field: slug", errx.Field{
		Field: "slug", Rule: "pattern", Message: "must be lowercase kebab-case, e.g. eco-friendly",
	})
}
