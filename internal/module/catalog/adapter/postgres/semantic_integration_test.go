//go:build integration

package postgres_test

import (
	"context"
	"testing"

	"shopnexus/internal/module/catalog/port"
)

// Before the embedding cron has run, every vector is NULL — so a seed resolves to nothing and
// a ranking is empty. Both are the specified behaviour, and this is what keeps the route
// honest until the cron exists.
func TestRepo_SeedVectorsMissingUntilEmbedded(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	c := createCategory(t, repo, unique("near-"), nil)
	tag := createTag(t, repo, slugOf("near-"), nil)

	got, err := repo.SeedVectors(ctx, []port.Seed{{CategoryID: c.ID}, {TagSlug: tag.Slug}})
	if err != nil {
		t.Fatalf("SeedVectors: %v", err)
	}
	// One entry per seed, both nil: the service names the seed it rejects, so the positions
	// have to survive the read.
	if len(got) != 2 || got[0] != nil || got[1] != nil {
		t.Fatalf("vectors = %+v, want two nils before the embedding pass", got)
	}

	// A ranking with no probe vectors is empty rather than an error.
	cats, err := repo.NearestCategories(ctx, nil, 10)
	if err != nil {
		t.Fatalf("NearestCategories: %v", err)
	}
	if len(cats) != 0 {
		t.Fatalf("categories = %+v, want none", cats)
	}
	tags, err := repo.NearestTags(ctx, nil, nil, 0, 10)
	if err != nil {
		t.Fatalf("NearestTags: %v", err)
	}
	if len(tags) != 0 {
		t.Fatalf("tags = %+v, want none", tags)
	}
}

// Once a vector is written, the seed resolves and the ranking comes back scored and ordered.
// The vectors are inserted with SQL because writing them is the embedding cron's job, which
// does not exist yet — this proves the read side against the real pgvector operators.
func TestRepo_NearestRanksOnceEmbedded(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	pool := poolOf(t)

	near := createCategory(t, repo, unique("near-hit-"), nil)
	far := createCategory(t, repo, unique("near-miss-"), nil)
	seedTag := createTag(t, repo, slugOf("seed-"), nil)
	other := createTag(t, repo, slugOf("other-"), nil)

	// The column is vector(1024), so a literal has to carry all 1024 elements.
	axis := func(first float32) string {
		out := make([]byte, 0, 4096)
		out = append(out, '[')
		for i := 0; i < 1024; i++ {
			if i > 0 {
				out = append(out, ',')
			}
			switch i {
			case 0:
				out = append(out, []byte(formatFloat(first))...)
			case 1:
				out = append(out, []byte(formatFloat(1-first))...)
			default:
				out = append(out, '0')
			}
		}
		return string(append(out, ']'))
	}
	for _, row := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO category_embedding (category_id, dense) VALUES ($1, $2::vector)`, []any{near.ID, axis(1)}},
		{`INSERT INTO category_embedding (category_id, dense) VALUES ($1, $2::vector)`, []any{far.ID, axis(0)}},
		{`INSERT INTO tag_embedding (tag_id, dense) VALUES ($1, $2::vector)`, []any{seedTag.Slug, axis(1)}},
		{`INSERT INTO tag_embedding (tag_id, dense) VALUES ($1, $2::vector)`, []any{other.Slug, axis(1)}},
	} {
		if _, err := pool.Exec(ctx, row.sql, row.args...); err != nil {
			t.Fatalf("insert embedding: %v", err)
		}
	}

	// A repeated seed is still one read per position, and the caller reads it positionally.
	vectors, err := repo.SeedVectors(ctx, []port.Seed{{TagSlug: seedTag.Slug}, {TagSlug: seedTag.Slug}})
	if err != nil {
		t.Fatalf("SeedVectors: %v", err)
	}
	if len(vectors) != 2 || len(vectors[0]) != 1024 || len(vectors[1]) != 1024 {
		t.Fatalf("vectors = %+v, want two vectors of 1024", len(vectors))
	}
	vectors = vectors[:1]

	// The limit is generous and the assertion relative: the schema is shared, so this test
	// cannot be the only thing embedded, and an equally-aligned leftover ties for first.
	cats, err := repo.NearestCategories(ctx, vectors, 100)
	if err != nil {
		t.Fatalf("NearestCategories: %v", err)
	}
	atNear, atFar := indexOfCategory(cats, near.ID), indexOfCategory(cats, far.ID)
	if atNear < 0 || atFar < 0 {
		t.Fatalf("ranking = %+v, want both categories in it", cats)
	}
	if atNear >= atFar {
		t.Errorf("aligned category ranked %d, unaligned %d — want the aligned one first", atNear, atFar)
	}
	if cats[atNear].Score <= cats[atFar].Score {
		t.Errorf("scores do not separate: %v then %v", cats[atNear].Score, cats[atFar].Score)
	}

	// A tag ranking excludes its own seeds: they are already on the listing.
	tags, err := repo.NearestTags(ctx, vectors, []string{seedTag.Slug}, 0, 100)
	if err != nil {
		t.Fatalf("NearestTags: %v", err)
	}
	for _, row := range tags {
		if row.Tag.Slug == seedTag.Slug {
			t.Fatalf("the seed came back in its own ranking: %+v", tags)
		}
	}
	var found bool
	for _, row := range tags {
		if row.Tag.Slug == other.Slug {
			found = true
		}
	}
	if !found {
		t.Fatalf("ranking = %+v, want the other embedded tag", tags)
	}
}

func indexOfCategory(rows []port.ScoredCategory, id int64) int {
	for i, row := range rows {
		if row.Category.ID == id {
			return i
		}
	}
	return -1
}

func formatFloat(f float32) string {
	if f == 1 {
		return "1"
	}
	return "0"
}
