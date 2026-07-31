//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"shopnexus/internal/module/catalog/domain"
	"shopnexus/internal/module/catalog/port"
)

// slugOf turns the unique() stamp into something the slug pattern accepts: it carries a dot,
// which kebab-case does not allow.
func slugOf(prefix string) string {
	return strings.ToLower(strings.ReplaceAll(unique(prefix), ".", "-"))
}

// The prefix search is the reason tag_id_prefix_idx uses text_pattern_ops: a btree under a
// non-C collation will not serve LIKE 'x%'.
func TestRepo_TagPrefixSearchAndUpsert(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	prefix := slugOf("it-")

	createTag(t, repo, prefix+"-alpha", nil)
	// The same slug again is an update, not a conflict: the route is idempotent.
	desc := "second write"
	createTag(t, repo, prefix+"-alpha", &desc)

	rows, total, err := repo.ListTags(ctx, port.TagFilter{Prefix: prefix, Limit: 10})
	if err != nil {
		t.Fatalf("ListTags: %v", err)
	}
	if total != 1 || len(rows) != 1 {
		t.Fatalf("rows = %+v, total = %d, want one", rows, total)
	}
	if rows[0].Description == nil || *rows[0].Description != desc {
		t.Errorf("description = %v, want the second write", rows[0].Description)
	}
}

// A prefix is matched literally: LIKE metacharacters in what the picker typed are escaped, so
// "%" is a per-cent sign and not the whole dictionary — which would also lose the index.
func TestRepo_ListTagsEscapesLikeMetacharacters(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	createTag(t, repo, slugOf("like-"), nil)

	for _, prefix := range []string{"%", "_", `\`} {
		rows, total, err := repo.ListTags(ctx, port.TagFilter{Prefix: prefix, Limit: 10})
		if err != nil {
			t.Fatalf("ListTags(%q): %v", prefix, err)
		}
		// No slug can contain any of these, so a literal match finds nothing.
		if total != 0 || len(rows) != 0 {
			t.Errorf("prefix %q matched %d rows, want none — the pattern is not escaped", prefix, total)
		}
	}
}

// An empty prefix is the whole dictionary, and the page bounds it.
func TestRepo_ListTagsPages(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	prefix := slugOf("page-")
	for _, suffix := range []string{"a", "b", "c"} {
		createTag(t, repo, prefix+"-"+suffix, nil)
	}
	rows, total, err := repo.ListTags(ctx, port.TagFilter{Prefix: prefix, Offset: 1, Limit: 1})
	if err != nil {
		t.Fatalf("ListTags: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3 — the window count is the whole match, not the page", total)
	}
	if len(rows) != 1 || rows[0].Slug != prefix+"-b" {
		t.Fatalf("rows = %+v, want the second slug alone", rows)
	}
}

func TestRepo_DeleteTag(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	tag := createTag(t, repo, slugOf("del-"), nil)
	if err := repo.DeleteTag(ctx, tag.Slug); err != nil {
		t.Fatalf("DeleteTag: %v", err)
	}
	if err := repo.DeleteTag(ctx, tag.Slug); !errors.Is(err, domain.ErrTagNotFound) {
		t.Fatalf("second DeleteTag = %v, want ErrTagNotFound", err)
	}
}

// The slug shape is enforced twice on purpose: the domain refuses it early, and
// "tag_id_slug_check" holds even when a service is wrong. This is the second one, on the real
// table — the only place a bad slug could still arrive from.
func TestMigration_TagSlugCheck(t *testing.T) {
	ctx := context.Background()
	pool := poolOf(t)
	for _, bad := range []string{"Handmade", "eco_friendly", "-lead", "trail-", "double--dash", ""} {
		_, err := pool.Exec(ctx, `INSERT INTO tag (id) VALUES (@id)`, pgx.NamedArgs{"id": bad})
		if err == nil || !strings.Contains(err.Error(), "tag_id_slug_check") {
			// Undo the write the constraint should have refused, so the run stays repeatable.
			if err == nil {
				_, _ = pool.Exec(ctx, `DELETE FROM tag WHERE id = @id`, pgx.NamedArgs{"id": bad})
			}
			t.Errorf("slug %q: err = %v, want the check constraint to refuse it", bad, err)
		}
	}
}
