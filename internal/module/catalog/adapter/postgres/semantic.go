package postgres

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopnexus/internal/module/catalog/domain"
	"shopnexus/internal/module/catalog/port"
	"shopnexus/internal/module/common/dbx"
)

// SeedVectors reads one vector per seed, in the order asked. Two statements rather than a
// UNION, because the two seed kinds are keyed differently; a seed the embedding pass has not
// reached yet comes back nil, which is what lets the service name it.
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
	byTag := map[string]port.Vector{}
	if len(slugs) > 0 {
		const q = `SELECT tag_id, dense::text FROM tag_embedding
		           WHERE tag_id = ANY(@slugs) AND dense IS NOT NULL`
		if err := collectVectors(ctx, r.pool, q, pgx.NamedArgs{"slugs": slugs}, func(key any, v port.Vector) {
			byTag[key.(string)] = v
		}); err != nil {
			return nil, err
		}
	}
	byCategory := map[int64]port.Vector{}
	if len(categoryIDs) > 0 {
		const q = `SELECT category_id, dense::text FROM category_embedding
		           WHERE category_id = ANY(@ids) AND dense IS NOT NULL`
		if err := collectVectors(ctx, r.pool, q, pgx.NamedArgs{"ids": categoryIDs}, func(key any, v port.Vector) {
			byCategory[key.(int64)] = v
		}); err != nil {
			return nil, err
		}
	}
	// One entry per seed, nil included: the caller reads the result positionally.
	out := make([]port.Vector, len(seeds))
	for i, s := range seeds {
		if s.TagSlug != "" {
			out[i] = byTag[s.TagSlug]
			continue
		}
		out[i] = byCategory[s.CategoryID]
	}
	return out, nil
}

// collectVectors scans a (key, vector) read and hands each pair to keep, which files it under
// the map its seed kind is keyed by.
func collectVectors(ctx context.Context, q *pgxpool.Pool, sql string, args pgx.NamedArgs, keep func(key any, v port.Vector)) error {
	rows, err := q.Query(ctx, sql, args)
	if err != nil {
		return fmt.Errorf("db query seed vectors: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		// pgx has no codec for the vector OID and this module adds no dependency for one, so
		// the column is cast to text and parsed here.
		var (
			key     any
			literal string
		)
		if err := rows.Scan(&key, &literal); err != nil {
			return fmt.Errorf("db scan seed vector: %w", err)
		}
		v, err := parseVector(literal)
		if err != nil {
			return err
		}
		keep(key, v)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("db iterate seed vectors: %w", err)
	}
	return nil
}

// ListingProbe reads a listing's own embedding back to be used as a query — the dense half
// only, which is what "more like this one" means.
//
// The sparse half is deliberately left out. It is a lexical vector: matching against it finds
// the listings that reuse this one's *words*, which for a marketplace of one-of-a-kind goods
// ranks a "iPhone 13 case" beside an "iPhone 13" and calls them similar. Dense alone is the
// neighbourhood of the thing rather than of its title.
func (r *Repo) ListingProbe(ctx context.Context, listingID int64) (port.Probe, error) {
	// Cast to text for the same reason every other vector read here is: pgx has no codec for
	// the type and this module adds no dependency for one.
	const q = `SELECT dense::text FROM listing_embedding
	           WHERE listing_id = @listing_id AND dense IS NOT NULL`
	var literal string
	err := r.pool.QueryRow(ctx, q, pgx.NamedArgs{"listing_id": listingID}).Scan(&literal)
	if dbx.IsNoRows(err) {
		return port.Probe{}, domain.ErrListingNotEmbedded
	}
	if err != nil {
		return port.Probe{}, fmt.Errorf("db scan listing probe: %w", err)
	}
	v, err := parseVector(literal)
	if err != nil {
		return port.Probe{}, err
	}
	return port.Probe{Dense: v}, nil
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
	// COALESCE, because `id <> ALL(NULL)` is NULL rather than true: a nil exclude list would
	// otherwise answer nothing at all.
	const q = `SELECT t.id, t.description, 1 - (e.dense <=> @probe::vector) AS score
	           FROM tag_embedding e
	           JOIN tag t ON t.id = e.tag_id
	           WHERE e.dense IS NOT NULL AND t.id <> ALL(COALESCE(@exclude::text[], '{}'))
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
