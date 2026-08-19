package postgres

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopnexus/internal/module/catalog/port"
	"shopnexus/internal/module/common/dbx"
)

// Embeddings is the indexer's side of catalog: the three stale queues and the three vector
// tables behind them. Its own type rather than methods on Repo, because the indexer is a
// separate process and has no use for the aggregate loader.
type Embeddings struct {
	pool *pgxpool.Pool
}

// SparseDimensions is the width declared by `listing_embedding.sparse` and its two siblings.
// Changing it is a migration, so it lives beside the SQL rather than in config.
const SparseDimensions = 250048

func NewEmbeddings(pool *pgxpool.Pool) *Embeddings {
	return &Embeddings{pool: pool}
}

var _ port.Embeddings = (*Embeddings)(nil)

// maxSparseNonZero is what the HNSW sparsevec index accepts in one value. Enforced at the
// INSERT by pgvector, so a long document does not degrade — the write fails. The adapter
// keeps the heaviest terms instead, which is what the tail of a weight distribution is worth.
const maxSparseNonZero = 1000

func (e *Embeddings) ListStale(ctx context.Context, kind port.Kind, limit int) ([]port.Stale, error) {
	var q string
	switch kind {
	case port.KindListing:
		// The text a listing is found by: its name, the category it sits in, its tags, its
		// specification values and finally its description. Ordered by how much each is worth
		// to a search — the tail is what gets cut when the text is clipped, and a description
		// is the most expendable of the five.
		q = `SELECT l.id, '', l.embedding_stale_at,
		            concat_ws(' ', l.name, c.name,
		              (SELECT string_agg(lt.tag, ' ' ORDER BY lt.tag)
		                 FROM listing_tag lt WHERE lt.listing_id = l.id),
		              (SELECT string_agg(value, ' ')
		                 FROM jsonb_each_text(l.specifications)),
		              l.description)
		     FROM listing l
		     JOIN category c ON c.id = l.category_id
		     WHERE l.embedding_stale_at IS NOT NULL AND l.deleted_at IS NULL
		     ORDER BY l.embedding_stale_at, l.id
		     LIMIT @limit`
	case port.KindCategory:
		q = `SELECT id, '', embedding_stale_at, concat_ws(' ', name, description)
		     FROM category
		     WHERE embedding_stale_at IS NOT NULL
		     ORDER BY embedding_stale_at, id
		     LIMIT @limit`
	case port.KindTag:
		// A slug is the tag's only text, and it is kebab-case: split it, or the model sees one
		// unknown token where there were two ordinary words.
		q = `SELECT 0, id, embedding_stale_at, concat_ws(' ', replace(id, '-', ' '), description)
		     FROM tag
		     WHERE embedding_stale_at IS NOT NULL
		     ORDER BY embedding_stale_at, id
		     LIMIT @limit`
	default:
		return nil, fmt.Errorf("db list stale: unknown kind %q", kind)
	}

	rows, err := e.pool.Query(ctx, q, pgx.NamedArgs{"limit": limit})
	if err != nil {
		return nil, fmt.Errorf("db query stale %s: %w", kind, err)
	}
	defer rows.Close()

	var out []port.Stale
	for rows.Next() {
		s := port.Stale{Kind: kind}
		if err := rows.Scan(&s.ID, &s.Slug, &s.StaleAt, &s.Text); err != nil {
			return nil, fmt.Errorf("db scan stale %s: %w", kind, err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db iterate stale %s: %w", kind, err)
	}
	return out, nil
}

// vectorTable is where each kind's vectors live and what keys them. The three tables differ
// only in those two names, so the statements are built from this rather than written out
// three times with one word changed.
var vectorTable = map[port.Kind]struct{ table, key string }{
	port.KindListing:  {"listing_embedding", "listing_id"},
	port.KindCategory: {"category_embedding", "category_id"},
	port.KindTag:      {"tag_embedding", "tag_id"},
}

// staleTable is the row carrying the mark to clear, per kind.
var staleTable = map[port.Kind]struct{ table, key string }{
	port.KindListing:  {"listing", "id"},
	port.KindCategory: {"category", "id"},
	port.KindTag:      {"tag", "id"},
}

func (e *Embeddings) Save(ctx context.Context, done []port.Embedded) error {
	if len(done) == 0 {
		return nil
	}
	now := time.Now()
	return dbx.InTx(ctx, e.pool, func(tx pgx.Tx) error {
		for _, d := range done {
			vec, ok := vectorTable[d.Kind]
			if !ok {
				return fmt.Errorf("db save embedding: unknown kind %q", d.Kind)
			}
			sparse, err := sparseLiteral(d.Sparse, SparseDimensions)
			if err != nil {
				return err
			}
			key := any(d.ID)
			if d.Kind == port.KindTag {
				key = d.Slug
			}
			upsert := `INSERT INTO ` + vec.table + ` (` + vec.key + `, dense, sparse, updated_at)
			           VALUES (@key, @dense::vector, @sparse::sparsevec, @now)
			           ON CONFLICT (` + vec.key + `) DO UPDATE
			             SET dense = EXCLUDED.dense,
			                 sparse = EXCLUDED.sparse,
			                 updated_at = EXCLUDED.updated_at`
			args := pgx.NamedArgs{
				"key":    key,
				"dense":  denseLiteral(d.Dense),
				"sparse": sparse,
				"now":    now,
			}
			if _, err := tx.Exec(ctx, upsert, args); err != nil {
				return fmt.Errorf("db upsert %s embedding: %w", d.Kind, err)
			}

			// Clear the exact mark this vector was computed from. A row edited while the model
			// was working carries a newer one, matches nothing here, and is picked up next
			// pass — which is the whole point of carrying the timestamp through.
			src := staleTable[d.Kind]
			clear := `UPDATE ` + src.table + ` SET embedding_stale_at = NULL
			          WHERE ` + src.key + ` = @key AND embedding_stale_at = @stale_at`
			if _, err := tx.Exec(ctx, clear, pgx.NamedArgs{"key": key, "stale_at": d.StaleAt}); err != nil {
				return fmt.Errorf("db clear %s stale mark: %w", d.Kind, err)
			}
		}
		return nil
	})
}

// denseLiteral is pgvector's `[1,2,3]`. Text rather than a binary codec because this module
// adds no pgvector dependency for one — the same reason the reads cast to text.
func denseLiteral(v []float32) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, x := range v {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(x), 'g', -1, 32))
	}
	b.WriteByte(']')
	return b.String()
}

// sparseLiteral is pgvector's `{index:weight,...}/dimensions`.
//
// Two conversions happen here and neither belongs to the model: pgvector counts indices from
// one, and the value may carry at most maxSparseNonZero of them. Both are properties of the
// column, which is why the provider hands over the tokenizer's own zero-based map and this
// function is where storage's rules are applied.
//
// A function of (weights, dim) rather than a method on the writer, because a search probe is
// spelled by the same rules as the rows it is compared against and reaches this from search.go.
func sparseLiteral(weights map[uint32]float32, dim uint32) (string, error) {
	type term struct {
		index  uint32
		weight float32
	}
	terms := make([]term, 0, len(weights))
	for index, weight := range weights {
		if weight == 0 {
			continue // a stored zero is a non-zero that says nothing, and it counts against the cap
		}
		if index >= dim {
			return "", fmt.Errorf("sparse index %d is outside the %d-wide column", index, dim)
		}
		terms = append(terms, term{index, weight})
	}
	// Heaviest first, keep the cap, then back into index order — which is the order pgvector
	// parses a sparsevec in.
	if len(terms) > maxSparseNonZero {
		sort.Slice(terms, func(i, j int) bool { return terms[i].weight > terms[j].weight })
		terms = terms[:maxSparseNonZero]
	}
	sort.Slice(terms, func(i, j int) bool { return terms[i].index < terms[j].index })

	var b strings.Builder
	b.WriteByte('{')
	for i, t := range terms {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatUint(uint64(t.index)+1, 10))
		b.WriteByte(':')
		b.WriteString(strconv.FormatFloat(float64(t.weight), 'g', -1, 32))
	}
	b.WriteByte('}')
	b.WriteByte('/')
	b.WriteString(strconv.FormatUint(uint64(dim), 10))
	return b.String(), nil
}
