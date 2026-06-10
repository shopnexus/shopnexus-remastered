// Package repolist is the generic runtime behind generated per-table List
// functions. One List[T] call serves both offset (?page) and cursor
// (?cursor/?sort) pagination, picked from the request. The per-table specifics
// — table, sortable-column whitelist, the column<->field map — arrive as a
// Query the generator fills in. The runtime writes no reflection: rows scan by
// result-column name, the cursor round-trips typed values through JSON.
package repolist

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/guregu/null/v6"
	"github.com/jackc/pgx/v5"

	"shopnexus-server/internal/shared/paginate"
)

const defaultLimit = 10 // keyset peek fallback when a cursor request omits limit

// Conn is the minimal query surface List needs. Both sqlc's *Queries (via its
// DBTX) and pgsqlc.TxBeginner satisfy it, so a generated method can pass q.db.
type Conn interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Query is everything List needs for one listing. Generated code fills the
// static parts (Table, PK, Sort, Fields); callers layer Where/From/With on top
// to compose custom sources (e.g. a pgvector candidate pool) while reusing the
// same scan + pagination machinery.
type Query[T any] struct {
	Table  string                  // FROM source: `"catalog"."product_spu"` or a subquery/CTE alias
	With   string                  // optional CTE prefix, e.g. `WITH dense AS (...)`
	PK     string                  // logical pk column, the keyset tiebreaker (always sorted last)
	Order  string                  // optional raw ORDER BY clause (no keyword) for offset/fetch-all; empty => PK. Whitelist-built upstream, never raw user input. Ignored in cursor mode.
	Sort   []string                // sortable-column whitelist; user ?sort must be one of these
	Fields func(*T) map[string]any // db column -> &field; drives scan, cursor encode/decode, pgx cursor args
	Where  []Cond
	Args   pgx.NamedArgs // extra named args for a custom From/With (e.g. @query_dense, @pool)
}

// Filter returns a copy with the given conditions appended — copy-on-write, so
// a shared base Query is safe to compose from concurrently.
func (q Query[T]) Filter(c ...Cond) Query[T] {
	q.Where = append(slices.Clone(q.Where), c...)
	return q
}

// From swaps the FROM source (a subquery/CTE alias) and its args. The source
// must expose the same column names as Fields keys.
func (q Query[T]) From(src string, args pgx.NamedArgs) Query[T] {
	q.Table, q.Args = src, args
	return q
}

// WithCTE sets the CTE prefix emitted before SELECT.
func (q Query[T]) WithCTE(cte string) Query[T] {
	q.With = cte
	return q
}

// OrderBy sets the offset/fetch-all ORDER BY clause (no keyword), overriding the
// default PK order — e.g. relevance ranking over a computed column.
func (q Query[T]) OrderBy(clause string) Query[T] {
	q.Order = clause
	return q
}

// cursorMode reports keyset mode: a cursor or an explicit sort is present;
// otherwise offset/page mode.
func cursorMode(p paginate.Params) bool { return p.Cursor.Valid || p.Sort != "" }

// List runs one listing in whichever mode the HTTP-bound params imply. Bounds
// policy (default/max limit) belongs to the caller (paginate.Params.Constrain);
// List honors p.Limit verbatim, where Limit <= 0 in offset mode means "fetch all".
func List[T any](
	ctx context.Context,
	conn Conn,
	p paginate.Params,
	q Query[T],
) (paginate.PaginateResult[T], error) {
	args := pgx.NamedArgs{}
	maps.Copy(args, q.Args)

	conds := []string{}
	for _, c := range q.Where {
		if c.skip() {
			continue
		}
		conds = append(conds, c.expr)
		if c.arg != "" {
			args[c.arg] = c.val
		}
	}

	if cursorMode(p) {
		return q.listKeyset(ctx, conn, p, conds, args)
	}
	return q.listOffset(ctx, conn, p, conds, args)
}

func (q Query[T]) listKeyset(
	ctx context.Context, conn Conn, p paginate.Params, conds []string, args pgx.NamedArgs,
) (paginate.PaginateResult[T], error) {
	var zero paginate.PaginateResult[T]

	limit := p.Limit.Int32
	if limit <= 0 {
		limit = defaultLimit
	}

	sort, err := q.sortTuple(p.Sort)
	if err != nil {
		return zero, err
	}

	allConds := conds
	if p.Cursor.Valid {
		keys, err := paginate.DecodeKeyset(p.Cursor.String)
		if err != nil {
			return zero, fmt.Errorf("decode cursor: %w", err)
		}
		if len(keys) != len(sort) {
			return zero, fmt.Errorf("cursor arity %d != sort arity %d", len(keys), len(sort))
		}
		where, err := q.keysetWhere(sort, keys, args)
		if err != nil {
			return zero, err
		}
		allConds = append(append([]string{}, conds...), where)
	}
	args["limit"] = limit + 1 // peek one extra to detect the next page

	rows, err := q.collect(ctx, conn, q.keysetSQL(allConds, sort), args)
	if err != nil {
		return zero, err
	}

	hasMore := int32(len(rows)) > limit
	if hasMore {
		rows = rows[:limit]
	}

	var next null.String
	if hasMore && len(rows) > 0 {
		next, err = q.encodeCursor(sort, rows[len(rows)-1])
		if err != nil {
			return zero, err
		}
	}

	return paginate.PaginateResult[T]{
		PageParams: p,
		Data:       rows,
		NextCursor: next, // Total left invalid: cursor mode never counts.
	}, nil
}

func (q Query[T]) listOffset(
	ctx context.Context, conn Conn, p paginate.Params, conds []string, args pgx.NamedArgs,
) (paginate.PaginateResult[T], error) {
	var zero paginate.PaginateResult[T]

	// Fetch-all (accessor) mode: no limit/offset, no count. One query.
	limit := p.Limit.Int32
	if limit <= 0 {
		data, err := q.collect(ctx, conn, q.allSQL(conds), args)
		if err != nil {
			return zero, err
		}
		return paginate.PaginateResult[T]{Data: data}, nil
	}

	page := p.Page.Int32
	if page <= 0 {
		page = 1
	}
	offset := (page - 1) * limit

	// Query 1: the page. Offset mode orders by pk for a deterministic page.
	listArgs := pgx.NamedArgs{"limit": limit, "offset": offset}
	maps.Copy(listArgs, args)
	data, err := q.collect(ctx, conn, q.offsetSQL(conds), listArgs)
	if err != nil {
		return zero, err
	}

	// Query 2: the count. Independent query, NOT a window function.
	var total int64
	if err := conn.QueryRow(ctx, q.countSQL(conds), args).Scan(&total); err != nil {
		return zero, fmt.Errorf("count %s: %w", q.Table, err)
	}

	return paginate.PaginateResult[T]{
		PageParams: p,
		Data:       data,
		Total:      null.IntFrom(total),
	}, nil
}

// --- SQL builders (pure: depend only on the Query shape + conds) ---

func (q Query[T]) allSQL(conds []string) string {
	return fmt.Sprintf(`%sSELECT %s FROM %s%s ORDER BY %s`,
		q.withPrefix(), q.selectList(), q.Table, whereClause(conds), q.offsetOrder())
}

func (q Query[T]) offsetSQL(conds []string) string {
	return fmt.Sprintf(`%sSELECT %s FROM %s%s ORDER BY %s LIMIT @limit OFFSET @offset`,
		q.withPrefix(), q.selectList(), q.Table, whereClause(conds), q.offsetOrder())
}

func (q Query[T]) keysetSQL(conds []string, sort []paginate.SortField) string {
	return fmt.Sprintf(`%sSELECT %s FROM %s%s ORDER BY %s LIMIT @limit`,
		q.withPrefix(), q.selectList(), q.Table, whereClause(conds), orderClause(sort))
}

// countSQL builds the offset-mode count query: same WHERE, no order/limit/offset.
func (q Query[T]) countSQL(conds []string) string {
	return fmt.Sprintf(`%sSELECT COUNT(*) FROM %s%s`, q.withPrefix(), q.Table, whereClause(conds))
}

func (q Query[T]) withPrefix() string {
	if q.With == "" {
		return ""
	}
	return q.With + " "
}

func (q Query[T]) pkOrder() string { return `"` + q.PK + `"` }

// offsetOrder is the offset/fetch-all ORDER BY: the caller's Order override
// (e.g. relevance) when set, else the pk for a deterministic page.
func (q Query[T]) offsetOrder() string {
	if q.Order != "" {
		return q.Order
	}
	return q.pkOrder()
}

// cols returns the Fields column names, sorted for a stable SELECT string.
func (q Query[T]) cols() []string {
	var probe T
	fm := q.Fields(&probe)
	cols := make([]string, 0, len(fm))
	for k := range fm {
		cols = append(cols, k)
	}
	slices.Sort(cols)
	return cols
}

func (q Query[T]) selectList() string {
	cols := q.cols()
	for i, c := range cols {
		cols[i] = `"` + c + `"`
	}
	return strings.Join(cols, ", ")
}

// --- cursor / sort ---

// sortTuple validates the request sort against the whitelist and appends the pk
// tiebreaker (ASC) so the ordering is total — keyset paging needs a stable order.
func (q Query[T]) sortTuple(raw string) ([]paginate.SortField, error) {
	allowed := make(map[string]bool, len(q.Sort))
	for _, c := range q.Sort {
		allowed[c] = true
	}

	sort := paginate.ParseSort(raw)
	for _, s := range sort {
		if !allowed[s.Field] {
			return nil, fmt.Errorf("sort field not allowed: %q", s.Field)
		}
	}
	if len(sort) == 0 || sort[len(sort)-1].Field != q.PK {
		sort = append(sort, paginate.SortField{Field: q.PK, Dir: paginate.Asc})
	}
	return sort, nil
}

func orderClause(sort []paginate.SortField) string {
	parts := make([]string, len(sort))
	for i, s := range sort {
		dir := "ASC"
		if s.Dir == paginate.Desc {
			dir = "DESC"
		}
		parts[i] = `"` + s.Field + `" ` + dir
	}
	return strings.Join(parts, ", ")
}

// keysetWhere builds the lexicographic OR-chain WHERE and binds typed cursor
// values to args. Each cursor key is JSON-decoded into its real field type (via
// Fields), so the comparison needs no SQL cast.
func (q Query[T]) keysetWhere(sort []paginate.SortField, keys []json.RawMessage, args pgx.NamedArgs) (string, error) {
	probe := new(T)
	fm := q.Fields(probe)
	for i, s := range sort {
		ptr, ok := fm[s.Field]
		if !ok {
			return "", fmt.Errorf("sort field %q not in Fields", s.Field)
		}
		if err := json.Unmarshal(keys[i], ptr); err != nil {
			return "", fmt.Errorf("decode cursor key %q: %w", s.Field, err)
		}
	}

	// Row i: equality on prefix cols[0..i-1], comparator on cols[i].
	ors := make([]string, len(sort))
	for i := range sort {
		ands := make([]string, 0, i+1)
		for j := 0; j <= i; j++ {
			key := fmt.Sprintf("k%d", j)
			args[key] = fm[sort[j].Field] // typed pointer; pgx encodes with the right OID
			col := `"` + sort[j].Field + `"`
			if j < i {
				ands = append(ands, fmt.Sprintf("%s = @%s", col, key))
			} else {
				cmp := ">"
				if sort[i].Dir == paginate.Desc {
					cmp = "<"
				}
				ands = append(ands, fmt.Sprintf("%s %s @%s", col, cmp, key))
			}
		}
		ors[i] = "(" + strings.Join(ands, " AND ") + ")"
	}
	return "(" + strings.Join(ors, " OR ") + ")", nil
}

// encodeCursor marshals the last row's sort-tuple values (typed, via Fields) so
// the next request decodes them back into the same types.
func (q Query[T]) encodeCursor(sort []paginate.SortField, row T) (null.String, error) {
	keys, err := q.encodeCursorKeys(sort, row)
	if err != nil {
		return null.String{}, err
	}
	return paginate.EncodeKeyset(keys), nil
}

func (q Query[T]) encodeCursorKeys(sort []paginate.SortField, row T) ([]json.RawMessage, error) {
	fm := q.Fields(&row)
	keys := make([]json.RawMessage, len(sort))
	for i, s := range sort {
		ptr, ok := fm[s.Field]
		if !ok {
			return nil, fmt.Errorf("sort field %q not in Fields", s.Field)
		}
		b, err := json.Marshal(ptr)
		if err != nil {
			return nil, fmt.Errorf("encode cursor key %q: %w", s.Field, err)
		}
		keys[i] = b
	}
	return keys, nil
}

// --- scan ---

// collect runs the query and scans each row by matching result column names to
// Fields pointers — name-based, so SELECT order is irrelevant and there is no
// reflection in this package.
func (q Query[T]) collect(ctx context.Context, conn Conn, sql string, args pgx.NamedArgs) ([]T, error) {
	rows, err := conn.Query(ctx, sql, args)
	if err != nil {
		return nil, fmt.Errorf("query list: %w", err)
	}
	defer rows.Close()

	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (T, error) {
		var m T
		fm := q.Fields(&m)
		fds := row.FieldDescriptions()
		dest := make([]any, len(fds))
		for i, fd := range fds {
			ptr, ok := fm[fd.Name]
			if !ok {
				return m, fmt.Errorf("scan %s: no field for column %q", q.Table, fd.Name)
			}
			dest[i] = ptr
		}
		if err := row.Scan(dest...); err != nil {
			return m, err
		}
		return m, nil
	})
}

func whereClause(conds []string) string {
	if len(conds) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(conds, " AND ")
}
