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

// ListStale reads the next batch and claims it in the same statement, by moving each row's mark
// to now().
//
// Claiming has to survive the read, and a lock does not: the model call that follows takes
// seconds, while `FOR UPDATE` here would be released the moment this statement's implicit
// transaction commits — so two workers would read the same rows and buy the same vectors twice.
// The mark is the claim instead. A claimed row is still stale, so nothing is lost if the worker
// dies; it has simply moved to the back of `ORDER BY embedding_stale_at`, where the next read
// steps past it to rows nobody has taken.
//
// SKIP LOCKED alone does not make that safe, and the guard on the mark is what does. Under READ
// COMMITTED a second worker that blocks on a row the first is claiming does not give up when the
// first commits: it re-reads the new version and re-applies the qual, and "still stale" is still
// true of a row somebody just claimed — so it would claim it a second time. Comparing the mark
// against the one the subquery read fails that re-check, because the claim is precisely what
// changed it. The row is then left for the next pass. Same idiom as Save's clear, and the reason
// a batch may come back shorter than the limit.
//
// Save is unaffected: it clears by comparing against the mark it was handed, which is the
// bumped one, and a seller editing the row mid-flight bumps it again and keeps it queued —
// exactly as before. A row that fails to embed also moves to the back rather than sitting at the
// head, so one poison listing no longer blocks the queue behind it.
func (e *Embeddings) ListStale(ctx context.Context, kind port.Kind, limit int) ([]port.Stale, error) {
	var q string
	switch kind {
	case port.KindListing:
		// The text a listing is found by: its name, the category it sits in, its tags, its
		// specification values and finally its description. Ordered by how much each is worth
		// to a search — the tail is what gets cut when the text is clipped, and a description
		// is the most expendable of the five. The category is a subquery and not a join
		// because RETURNING sees only the updated table; "category_id" is NOT NULL behind a
		// RESTRICT foreign key, so it finds a row for the same reason the join did.
		q = `UPDATE listing SET embedding_stale_at = now()
		     FROM (
		       SELECT l.id, l.embedding_stale_at FROM listing l
		       WHERE l.embedding_stale_at IS NOT NULL AND l.deleted_at IS NULL
		       ORDER BY l.embedding_stale_at, l.id
		       LIMIT @limit
		       FOR UPDATE SKIP LOCKED
		     ) AS claim
		     WHERE listing.id = claim.id
		       AND listing.embedding_stale_at = claim.embedding_stale_at
		     RETURNING listing.id, '', listing.embedding_stale_at,
		       concat_ws(' ', name,
		         (SELECT c.name FROM category c WHERE c.id = listing.category_id),
		         (SELECT string_agg(lt.tag, ' ' ORDER BY lt.tag)
		            FROM listing_tag lt WHERE lt.listing_id = listing.id),
		         (SELECT string_agg(value, ' ')
		            FROM jsonb_each_text(listing.specifications)),
		         description)`
	case port.KindCategory:
		q = `UPDATE category SET embedding_stale_at = now()
		     FROM (
		       SELECT c.id, c.embedding_stale_at FROM category c
		       WHERE c.embedding_stale_at IS NOT NULL
		       ORDER BY c.embedding_stale_at, c.id
		       LIMIT @limit
		       FOR UPDATE SKIP LOCKED
		     ) AS claim
		     WHERE category.id = claim.id
		       AND category.embedding_stale_at = claim.embedding_stale_at
		     RETURNING category.id, '', category.embedding_stale_at,
		       concat_ws(' ', category.name, category.description)`
	case port.KindTag:
		// A slug is the tag's only text, and it is kebab-case: split it, or the model sees one
		// unknown token where there were two ordinary words.
		q = `UPDATE tag SET embedding_stale_at = now()
		     FROM (
		       SELECT t.id, t.embedding_stale_at FROM tag t
		       WHERE t.embedding_stale_at IS NOT NULL
		       ORDER BY t.embedding_stale_at, t.id
		       LIMIT @limit
		       FOR UPDATE SKIP LOCKED
		     ) AS claim
		     WHERE tag.id = claim.id
		       AND tag.embedding_stale_at = claim.embedding_stale_at
		     RETURNING 0, tag.id, tag.embedding_stale_at,
		       concat_ws(' ', replace(tag.id, '-', ' '), tag.description)`
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
		// Pipelined rather than sent one at a time: a batch is two statements per row, and at a
		// few hundred rows the round trips cost more than the writes do. They still run inside
		// the one transaction, in the order queued, so what a partial failure leaves behind is
		// what it left behind before — nothing.
		batch := &pgx.Batch{}
		// failures[i] names what the i-th queued statement was doing, for the error that reads
		// its result. Kept beside the batch because a batch result says only which position
		// failed.
		failures := make([]string, 0, 2*len(done))

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
			batch.Queue(upsert, pgx.NamedArgs{
				"key":    key,
				"dense":  denseLiteral(d.Dense),
				"sparse": sparse,
				"now":    now,
			})
			failures = append(failures, fmt.Sprintf("db upsert %s embedding", d.Kind))

			// Clear the exact mark this vector was computed from. A row edited while the model
			// was working carries a newer one, matches nothing here, and is picked up next
			// pass — which is the whole point of carrying the timestamp through.
			src := staleTable[d.Kind]
			clear := `UPDATE ` + src.table + ` SET embedding_stale_at = NULL
			          WHERE ` + src.key + ` = @key AND embedding_stale_at = @stale_at`
			batch.Queue(clear, pgx.NamedArgs{"key": key, "stale_at": d.StaleAt})
			failures = append(failures, fmt.Sprintf("db clear %s stale mark", d.Kind))
		}

		// Every result has to be read, and the reader closed, before the transaction commits.
		results := tx.SendBatch(ctx, batch)
		for _, what := range failures {
			if _, err := results.Exec(); err != nil {
				_ = results.Close()
				return fmt.Errorf("%s: %w", what, err)
			}
		}
		if err := results.Close(); err != nil {
			return fmt.Errorf("db save %d embeddings: %w", len(done), err)
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
