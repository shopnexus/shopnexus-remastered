package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"shopnexus/internal/module/catalog/domain"
	"shopnexus/internal/module/catalog/port"
	"shopnexus/internal/module/common/dbx"
)

// searchCandidates is how deep each leg goes before fusion. A rank and not a score, because a
// cosine score is not comparable between queries: a fixed cutoff floods a broad search and
// empties a narrow one.
const searchCandidates = 200

// search runs the fused statement with iterative index scans switched on.
//
// The filters live inside each ANN leg, so `LIMIT @candidates` is post-filter: without iterative
// scan HNSW hands back ef_search candidates, the filter cuts them, and a narrow browse gets far
// fewer rows than it asked for. `relaxed_order` is the cheaper of the two modes and costs only
// exactness of the ordering *within* a leg, which fusion by rank is insensitive to.
//
// SET LOCAL means a transaction, which is two extra round trips on the search path. If that ever
// shows up in the latency, the fix is an AfterConnect hook on the pool rather than a change here.
func (r *Repo) search(ctx context.Context, q string, args pgx.NamedArgs) ([]port.ListingSummary, int64, error) {
	var (
		out   []port.ListingSummary
		total int64
	)
	err := dbx.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SET LOCAL hnsw.iterative_scan = relaxed_order`); err != nil {
			return fmt.Errorf("db set iterative scan: %w", err)
		}
		rows, err := tx.Query(ctx, q, args)
		if err != nil {
			return fmt.Errorf("db query search: %w", err)
		}
		defer rows.Close()
		out, total, err = scanListingCards(rows)
		return err
	})
	if err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// searchStatement builds the fused retrieval: one leg pair per probe, one rank-1 contribution per
// satisfied predicate, reciprocal rank fusion over all of them. False when the terms can rank
// nothing at all — a probe with both halves empty — and the caller browses instead.
//
// Fusion is over ranks and not scores because a cosine similarity and a sparse inner product are
// not on one scale. Adding them is what scoreExpr did, and it is why the old relevance floor had
// to be a fraction of the best hit rather than a number: the sum meant nothing absolute. It is
// also why no index could serve the ORDER BY — a sum of two expressions is not an operator class,
// so every search sequentially scanned listing_embedding.
//
// The text is assembled from fixed fragments with generated *parameter names* (`@probe_0_dense`).
// No caller value reaches the SQL text, which is the rule orderBy already followed.
func (r *Repo) searchStatement(f port.ListingFilter) (string, pgx.NamedArgs, bool) {
	args := feedArgs(f)
	args["rrf_k"] = domain.RRFConstant
	args["leg_floor"] = domain.LegRelevanceFloor
	args["candidates"] = searchCandidates

	var legs, sources []string
	for i, term := range f.Terms {
		switch {
		case term.Probe != nil:
			if len(term.Probe.Dense) > 0 {
				name := fmt.Sprintf("probe_%d_dense", i)
				leg := fmt.Sprintf("leg_%d_dense", i)
				args[name] = vectorLiteral(term.Probe.Dense)
				args[name+"_w"] = term.Weight * domain.DenseShare
				legs = append(legs, annLeg(leg, `1 - (e.dense <=> @`+name+`::vector)`,
					`e.dense <=> @`+name+`::vector`, `e.dense IS NOT NULL`))
				sources = append(sources, legSource(leg, name+"_w"))
			}
			// A half with nothing in it is left out rather than scanned: an empty sparsevec is at
			// distance 0 from every row, so the leg would rank @candidates rows of noise at score
			// 0 and its own floor — a share of that same 0 — would keep every one of them.
			if literal, ok := sparseLiteralOf(term.Probe.Sparse); ok {
				name := fmt.Sprintf("probe_%d_sparse", i)
				leg := fmt.Sprintf("leg_%d_sparse", i)
				args[name] = literal
				args[name+"_w"] = term.Weight * domain.SparseShare
				legs = append(legs, annLeg(leg, `-(e.sparse <#> @`+name+`::sparsevec)`,
					`e.sparse <#> @`+name+`::sparsevec`, `e.sparse IS NOT NULL`))
				sources = append(sources, legSource(leg, name+"_w"))
			}

		case term.Predicate != nil:
			sql, ok := predicateSQL[term.Predicate.Kind]
			if !ok {
				continue // a kind nothing here knows cannot reach the statement
			}
			name := fmt.Sprintf("pred_%d", i)
			args[name] = term.Predicate.Value
			args[name+"_w"] = term.Weight
			sources = append(sources, predicateSource(sql, name))
		}
	}
	if len(sources) == 0 {
		return "", nil, false
	}

	ctes := append(legs, `fused AS (
	             SELECT id, SUM(w / (@rrf_k::double precision + rank)) AS score
	             FROM (`+strings.Join(sources, `
	                   UNION ALL`)+`) t
	             GROUP BY id
	           )`)
	q := `WITH ` + strings.Join(ctes, `,
	           `) + `
	           ` + feedSelect + `f.score` + feedScore + fusedTotal(f) + feedFrom + `
	           JOIN fused f ON f.id = l.id` + fusedOrder(f) + feedPage
	return q, args, true
}

// annLeg is one nearest-neighbour scan and its relevance floor.
//
// The floor is applied here, per leg, and not to the fused score: RRF decays hyperbolically by
// rank, so a fraction of the best fused score is the same fraction for every query and cuts at no
// cliff. Within one leg the scores came out of one operator and are still comparable, which is
// where the measured 0.6 means what it was measured to mean.
//
// The caller's filters are inside the scan rather than applied to its result: a filter outside the
// LIMIT would let a narrow browse answer nothing while matching rows sat just past K.
//
// The rank is taken from an explicit ORDER BY, not from the input order of the subquery — an empty
// window follows that order today and is not promised to, and a wrong rank silently corrupts every
// fused score. The id tiebreak is what keeps two equally scored rows in the same order on page two
// as on page one.
func annLeg(name, score, order, notNull string) string {
	return name + ` AS (
	             SELECT id, rank FROM (
	               SELECT id, score, row_number() OVER (ORDER BY score DESC, id DESC) AS rank,
	                      max(score) OVER () AS best
	               FROM (
	                 SELECT l.id AS id, ` + score + ` AS score
	                 FROM listing l
	                 JOIN listing_embedding e ON e.listing_id = l.id` + feedWhere + `
	                   AND ` + notNull + `
	                 ORDER BY ` + order + `
	                 LIMIT @candidates
	               ) hit
	             ) ranked
	             WHERE score >= @leg_floor::double precision * best
	           )`
}

func legSource(leg, weight string) string {
	return `
	                   SELECT id, rank, @` + weight + `::double precision AS w FROM ` + leg
}

// predicateSource is a satisfied predicate entering the fusion at rank 1 — the same units as a
// probe's best hit, so one formula covers both kinds of signal and nothing is added across scales.
// A row that does not satisfy it contributes no row at all.
func predicateSource(sql, name string) string {
	return `
	                   SELECT l.id AS id, 1 AS rank, @` + name + `_w::double precision AS w
	                   FROM listing l` + feedWhere + ` AND ` + fmt.Sprintf(sql, name)
}

// predicateSQL is the whitelist, and each fragment keeps its %s so the caller binds the parameter
// name this file generated into it. A kind with no entry cannot reach the statement, which is what
// keeps a model's vocabulary out of the SQL text.
var predicateSQL = map[string]string{
	port.PredicateCategory:  `l.category_id = @%s::bigint`,
	port.PredicateTag:       `EXISTS (SELECT 1 FROM listing_tag pt WHERE pt.listing_id = l.id AND pt.tag = @%s::text)`,
	port.PredicateMinPrice:  `EXISTS (SELECT 1 FROM variant pv WHERE pv.listing_id = l.id AND pv.deleted_at IS NULL AND pv.price >= @%s::bigint)`,
	port.PredicateMaxPrice:  `EXISTS (SELECT 1 FROM variant pv WHERE pv.listing_id = l.id AND pv.deleted_at IS NULL AND pv.price <= @%s::bigint)`,
	port.PredicateCondition: `l.condition::text = @%s::text`,
	port.PredicateProvince:  `l.province_code = @%s::text`,
	port.PredicateWard:      `l.ward_code = @%s::text`,
}

// fusedOrder is the relevance order, or the order the caller asked for over the fused pool —
// "newest, but still about what I searched for".
func fusedOrder(f port.ListingFilter) string {
	if f.Sort == port.SortRelevance || f.Sort == "" {
		return `
	           ORDER BY f.score DESC, l.id DESC`
	}
	return rerankOrderBy(f)
}

// fusedTotal counts the fused pool when the caller is paging it in some other order, and answers
// nothing for a relevance sort — a top-K is not a seekable total, which is the rule the service
// already applies when it leaves TotalCount unset.
func fusedTotal(f port.ListingFilter) string {
	if f.Sort == port.SortRelevance || f.Sort == "" {
		return `, 0::bigint AS total_count`
	}
	return feedTotal
}

// sparseLiteralOf renders a probe's sparse half the way the column holds it, and answers false
// when there is nothing to match against — no terms, or a term outside the column's width, which
// is a probe from a model this schema cannot express.
//
// The formatter is shared with the writer: a probe is read against those rows, so it has to be
// spelled the same way, and a second implementation is how the two drift.
func sparseLiteralOf(weights map[uint32]float32) (string, bool) {
	if len(weights) == 0 {
		return "", false
	}
	literal, err := sparseLiteral(weights, SparseDimensions)
	if err != nil {
		return "", false
	}
	return literal, true
}
