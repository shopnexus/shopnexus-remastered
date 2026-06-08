// Package repolist is the generic runtime behind generated per-table List
// functions. One List[T] call serves both offset (?page) and cursor
// (?cursor/?sort) pagination, picked from the request. The per-table specifics
// — table name, sortable-column whitelist, filter binding, cursor-key
// extraction — arrive as a Spec the generator fills in, so the runtime stays
// reflection-free and type-safe.
package repolist

import (
	"context"
	"fmt"
	"maps"
	"reflect"
	"strings"
	"time"

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

// Spec is everything List needs that varies per table. The generator emits one
// builder returning a filled Spec; all fields are data or small typed closures.
type Spec[T any] struct {
	Table     string             // quoted, e.g. `"catalog"."comment"`
	PK        string             // logical pk field, e.g. "id"
	Whitelist paginate.Whitelist // sortable field -> SortCol{Col, Cast}

	// BindConds appends filter predicates (quoted col refs against @named args)
	// to conds and their values to args. This is the seam for changing what a
	// table filters on — regenerate, or wrap it in biz.
	BindConds func(conds *[]string, args pgx.NamedArgs)

	// CursorKeys extracts the sort-tuple values of a row as strings, in the
	// order paginate.NormalizedSort consumes them. Optional: when nil, List
	// reads them via the model's `db` tags (see cursorKeysByTag).
	CursorKeys func(sort []paginate.SortField, row T) []string
}

// Request is the mode-agnostic input. cursorMode is true when a cursor or an
// explicit sort is present; otherwise it is offset/page mode.
type Request struct {
	Page   int32
	Limit  int32
	Cursor string
	Sort   []paginate.SortField
}

func (r Request) cursorMode() bool { return r.Cursor != "" || len(r.Sort) > 0 }

// FromParams builds a Request from the HTTP-bound paginate.Params. Mode is
// implicit: a cursor or ?sort selects keyset, otherwise ?page offset.
func FromParams(p paginate.Params) Request {
	return Request{
		Page:   p.Page.Int32,
		Limit:  p.Limit.Int32,
		Cursor: p.Cursor.String,
		Sort:   paginate.ParseSort(p.Sort),
	}
}

// List runs one table's list query in whichever mode req implies. Bounds policy
// (default/max limit) belongs to the caller (paginate.Params.Constrain); List
// honors req.Limit verbatim, where Limit <= 0 in offset mode means "fetch all".
func List[T any](
	ctx context.Context,
	conn Conn,
	spec Spec[T],
	req Request,
) (paginate.PaginateResult[T], error) {
	conds := []string{}
	args := pgx.NamedArgs{}
	spec.BindConds(&conds, args)

	if req.cursorMode() {
		return listKeyset(ctx, conn, spec, req, conds, args)
	}
	return listOffset(ctx, conn, spec, req, conds, args)
}

func listKeyset[T any](
	ctx context.Context, conn Conn, spec Spec[T],
	req Request, conds []string, args pgx.NamedArgs,
) (paginate.PaginateResult[T], error) {
	var zero paginate.PaginateResult[T]

	limit := req.Limit
	if limit <= 0 {
		limit = defaultLimit
	}

	ks := paginate.Keyset{Limit: limit, Sort: req.Sort}
	if req.Cursor != "" {
		ks.Cursor = null.StringFrom(req.Cursor)
	}

	query, err := buildKeyset(spec, ks, conds, args, limit)
	if err != nil {
		return zero, err
	}

	rows, err := queryRows[T](ctx, conn, query, args)
	if err != nil {
		return zero, err
	}

	hasMore := int32(len(rows)) > limit
	if hasMore {
		rows = rows[:limit]
	}

	var next null.String
	if hasMore && len(rows) > 0 {
		sort := ks.NormalizedSort(spec.PK)
		next = paginate.EncodeKeyset(cursorKeys(spec, sort, rows[len(rows)-1]))
	}

	return paginate.PaginateResult[T]{
		PageParams: paginate.Params{Limit: null.Int32From(limit), Cursor: ks.Cursor},
		Data:       rows,
		NextCursor: next, // Total left invalid: cursor mode never counts.
	}, nil
}

func listOffset[T any](
	ctx context.Context, conn Conn, spec Spec[T],
	req Request, conds []string, args pgx.NamedArgs,
) (paginate.PaginateResult[T], error) {
	var zero paginate.PaginateResult[T]

	// Fetch-all (accessor) mode: no limit/offset, no count. One query.
	if req.Limit <= 0 {
		query := buildAll(spec, conds)
		data, err := queryRows[T](ctx, conn, query, args)
		if err != nil {
			return zero, err
		}
		return paginate.PaginateResult[T]{Data: data}, nil
	}

	page := req.Page
	if page <= 0 {
		page = 1
	}
	offset := (page - 1) * req.Limit

	// Query 1: the page. Offset mode orders by pk for a deterministic page.
	listQuery, listArgs := buildOffset(spec, conds, args, req.Limit, offset)
	data, err := queryRows[T](ctx, conn, listQuery, listArgs)
	if err != nil {
		return zero, err
	}

	// Query 2: the count. Independent query, NOT a window function.
	var total int64
	if err := conn.QueryRow(ctx, countSQL(spec.Table, conds), args).Scan(&total); err != nil {
		return zero, fmt.Errorf("count %s: %w", spec.Table, err)
	}

	return paginate.PaginateResult[T]{
		PageParams: paginate.Params{Page: null.Int32From(page), Limit: null.Int32From(req.Limit)},
		Data:       data,
		Total:      null.IntFrom(total),
	}, nil
}

// buildKeyset returns the keyset list SQL and mutates args with the cursor
// key values + the peek limit (limit+1). Pure except for the args map it fills.
func buildKeyset[T any](spec Spec[T], ks paginate.Keyset, conds []string, args pgx.NamedArgs, limit int32) (string, error) {
	where, order, keyArgs, err := ks.Build(spec.Whitelist, spec.PK)
	if err != nil {
		return "", fmt.Errorf("keyset build: %w", err)
	}
	maps.Copy(args, keyArgs)
	args["limit"] = limit + 1 // peek one extra to detect the next page

	allConds := conds
	if where != "" {
		allConds = append(append([]string{}, conds...), where)
	}
	return fmt.Sprintf(
		`SELECT * FROM %s%s ORDER BY %s LIMIT @limit`,
		spec.Table, whereClause(allConds), order,
	), nil
}

// buildAll returns the fetch-all SQL: filter only, ordered by pk, no limit.
func buildAll[T any](spec Spec[T], conds []string) string {
	return fmt.Sprintf(`SELECT * FROM %s%s ORDER BY %s`, spec.Table, whereClause(conds), pkOrder(spec))
}

// buildOffset returns the offset list SQL and its args (filter args + limit/offset).
func buildOffset[T any](spec Spec[T], conds []string, args pgx.NamedArgs, limit, offset int32) (string, pgx.NamedArgs) {
	listArgs := pgx.NamedArgs{"limit": limit, "offset": offset}
	maps.Copy(listArgs, args)
	sql := fmt.Sprintf(
		`SELECT * FROM %s%s ORDER BY %s LIMIT @limit OFFSET @offset`,
		spec.Table, whereClause(conds), pkOrder(spec),
	)
	return sql, listArgs
}

// countSQL builds the offset-mode count query: same WHERE, no order/limit/offset.
// This is the one place the count strategy lives — change it here (or override
// via a hand-written repo) to move off exact COUNT(*).
func countSQL(table string, conds []string) string {
	return fmt.Sprintf(`SELECT COUNT(*) FROM %s%s`, table, whereClause(conds))
}

func pkOrder[T any](spec Spec[T]) string {
	if c, ok := spec.Whitelist[spec.PK]; ok {
		return c.Col
	}
	return `"` + spec.PK + `"`
}

func whereClause(conds []string) string {
	if len(conds) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(conds, " AND ")
}

func queryRows[T any](ctx context.Context, conn Conn, sql string, args pgx.NamedArgs) ([]T, error) {
	rows, err := conn.Query(ctx, sql, args)
	if err != nil {
		return nil, fmt.Errorf("query list: %w", err)
	}
	defer rows.Close()
	return pgx.CollectRows(rows, pgx.RowToStructByName[T])
}

// cursorKeys returns the row's sort-tuple as strings, using the Spec override
// when set, else reading the model's `db`-tagged fields.
func cursorKeys[T any](spec Spec[T], sort []paginate.SortField, row T) []string {
	if spec.CursorKeys != nil {
		return spec.CursorKeys(sort, row)
	}
	return cursorKeysByTag(sort, row, spec.Whitelist)
}

// cursorKeysByTag reads each sort field's value off the row by matching the
// whitelist's physical column to the struct's `db` tag (emitted by sqlc via
// emit_db_tags). Sortable columns are NOT NULL, so values are plain scalars.
func cursorKeysByTag[T any](sort []paginate.SortField, row T, wl paginate.Whitelist) []string {
	rv := reflect.ValueOf(row)
	rt := rv.Type()

	byTag := make(map[string]int, rt.NumField())
	for i := range rt.NumField() {
		if tag := rt.Field(i).Tag.Get("db"); tag != "" {
			byTag[tag] = i
		}
	}

	keys := make([]string, len(sort))
	for i, s := range sort {
		col := strings.Trim(wl[s.Field].Col, `"`)
		if idx, ok := byTag[col]; ok {
			keys[i] = stringifyKey(rv.Field(idx).Interface())
		}
	}
	return keys
}

// stringifyKey encodes a sort value as a string the SQL cast can parse back
// (time as RFC3339Nano, uuid/etc via Stringer, numbers via Sprint's shortest form).
func stringifyKey(v any) string {
	switch x := v.(type) {
	case time.Time:
		return x.Format(time.RFC3339Nano)
	case fmt.Stringer:
		return x.String()
	default:
		return fmt.Sprint(x)
	}
}
