package postgres

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"shopnexus/internal/module/catalog/domain"
	"shopnexus/internal/module/catalog/port"
)

// querier is what a pool and a transaction have in common, so a read is written once.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// SeedVectors reads one dense vector per seed. Two statements rather than a UNION, because
// the two seed kinds are keyed differently; a seed the embedding pass has not reached yet is
// simply absent, and the service counts what came back against what it asked for.
func (r *Repo) SeedVectors(ctx context.Context, seeds []port.Seed) ([]port.Vector, error) {
	var (
		slugs       []string
		categoryIDs []int64
	)
	for _, s := range seeds {
		if s.TagSlug != "" {
			slugs = append(slugs, s.TagSlug)
			continue
		}
		categoryIDs = append(categoryIDs, s.CategoryID)
	}
	out := make([]port.Vector, 0, len(seeds))
	if len(slugs) > 0 {
		const q = `SELECT dense::text FROM tag_embedding
		           WHERE tag_id = ANY(@slugs) AND dense IS NOT NULL`
		vectors, err := collectVectors(ctx, r.pool, q, pgx.NamedArgs{"slugs": slugs})
		if err != nil {
			return nil, err
		}
		out = append(out, vectors...)
	}
	if len(categoryIDs) > 0 {
		const q = `SELECT dense::text FROM category_embedding
		           WHERE category_id = ANY(@ids) AND dense IS NOT NULL`
		vectors, err := collectVectors(ctx, r.pool, q, pgx.NamedArgs{"ids": categoryIDs})
		if err != nil {
			return nil, err
		}
		out = append(out, vectors...)
	}
	return out, nil
}

func collectVectors(ctx context.Context, q querier, sql string, args pgx.NamedArgs) ([]port.Vector, error) {
	rows, err := q.Query(ctx, sql, args)
	if err != nil {
		return nil, fmt.Errorf("db query seed vectors: %w", err)
	}
	defer rows.Close()

	var out []port.Vector
	for rows.Next() {
		// pgx has no codec for the vector OID and this module adds no dependency for one, so
		// the column is cast to text and parsed here.
		var literal string
		if err := rows.Scan(&literal); err != nil {
			return nil, fmt.Errorf("db scan seed vector: %w", err)
		}
		v, err := parseVector(literal)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db iterate seed vectors: %w", err)
	}
	return out, nil
}

// NearestCategories ranks by cosine distance to the centroid of the probes. One probe is the
// common case; averaging several is what "near all of these" means, and it keeps the query a
// single index scan rather than one per seed.
func (r *Repo) NearestCategories(ctx context.Context, vectors []port.Vector, limit int) ([]port.ScoredCategory, error) {
	if len(vectors) == 0 {
		return nil, nil
	}
	// The probe is passed as text and cast, for the same reason the read is: no codec.
	const q = `SELECT c.id, c.parent_id, c.name, c.description, 1 - (e.dense <=> @probe::vector) AS score
	           FROM category_embedding e
	           JOIN category c ON c.id = e.category_id
	           WHERE e.dense IS NOT NULL
	           ORDER BY e.dense <=> @probe::vector
	           LIMIT @limit`
	args := pgx.NamedArgs{"probe": vectorLiteral(centroid(vectors)), "limit": limit}
	rows, err := r.pool.Query(ctx, q, args)
	if err != nil {
		return nil, fmt.Errorf("db query nearest categories: %w", err)
	}
	defer rows.Close()

	var out []port.ScoredCategory
	for rows.Next() {
		var (
			c     domain.Category
			score float64
		)
		if err := rows.Scan(&c.ID, &c.ParentID, &c.Name, &c.Description, &score); err != nil {
			return nil, fmt.Errorf("db scan nearest category: %w", err)
		}
		out = append(out, port.ScoredCategory{Category: c, Score: score})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db iterate nearest categories: %w", err)
	}
	return out, nil
}

// NearestTags is the same ranking over the tag dictionary. The seeds are excluded: they are
// already on the listing, so offering them back says nothing.
func (r *Repo) NearestTags(ctx context.Context, vectors []port.Vector, exclude []string, offset, limit int) ([]port.ScoredTag, error) {
	if len(vectors) == 0 {
		return nil, nil
	}
	const q = `SELECT t.id, t.description, 1 - (e.dense <=> @probe::vector) AS score
	           FROM tag_embedding e
	           JOIN tag t ON t.id = e.tag_id
	           WHERE e.dense IS NOT NULL AND t.id <> ALL(@exclude)
	           ORDER BY e.dense <=> @probe::vector
	           LIMIT @limit OFFSET @offset`
	args := pgx.NamedArgs{
		"probe":   vectorLiteral(centroid(vectors)),
		"exclude": exclude,
		"limit":   limit,
		"offset":  offset,
	}
	rows, err := r.pool.Query(ctx, q, args)
	if err != nil {
		return nil, fmt.Errorf("db query nearest tags: %w", err)
	}
	defer rows.Close()

	var out []port.ScoredTag
	for rows.Next() {
		var (
			t     domain.Tag
			score float64
		)
		if err := rows.Scan(&t.Slug, &t.Description, &score); err != nil {
			return nil, fmt.Errorf("db scan nearest tag: %w", err)
		}
		out = append(out, port.ScoredTag{Tag: t, Score: score})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db iterate nearest tags: %w", err)
	}
	return out, nil
}

// centroid averages the probes into one vector, which is what "near all of these" means in a
// cosine space.
func centroid(vectors []port.Vector) port.Vector {
	if len(vectors) == 1 {
		return vectors[0]
	}
	sum := make(port.Vector, len(vectors[0]))
	for _, v := range vectors {
		for i := range sum {
			sum[i] += v[i]
		}
	}
	for i := range sum {
		sum[i] /= float32(len(vectors))
	}
	return sum
}

// vectorLiteral renders a vector in pgvector's text format, which is what the ::vector cast
// reads.
func vectorLiteral(v port.Vector) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, f := range v {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(f), 'f', -1, 32))
	}
	b.WriteByte(']')
	return b.String()
}

// parseVector reads that same format back.
func parseVector(literal string) (port.Vector, error) {
	trimmed := strings.TrimSuffix(strings.TrimPrefix(literal, "["), "]")
	if trimmed == "" {
		return nil, nil
	}
	parts := strings.Split(trimmed, ",")
	out := make(port.Vector, 0, len(parts))
	for _, part := range parts {
		f, err := strconv.ParseFloat(strings.TrimSpace(part), 32)
		if err != nil {
			return nil, fmt.Errorf("parse vector element %q: %w", part, err)
		}
		out = append(out, float32(f))
	}
	return out, nil
}
