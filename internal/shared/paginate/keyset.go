package paginate

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/guregu/null/v6"
)

type SortDir string

const (
	Asc  SortDir = "asc"
	Desc SortDir = "desc"
)

type SortField struct {
	Field string
	Dir   SortDir
}

// SortCol describes one whitelisted sortable column: its quoted physical name
// and the Postgres type the (string-encoded) cursor value is cast back to.
type SortCol struct {
	Col  string // quoted physical column, e.g. `"date_created"`
	Cast string // Postgres type for the cursor cast, e.g. "timestamptz", "uuid", "float8"
}

// Whitelist maps logical field name -> column. Compile-time only; user input
// never reaches a column name, so there is no injection surface.
type Whitelist map[string]SortCol

// keysetCursor is the opaque position: the sort-tuple values of the last row,
// string-encoded and cast back to native types in SQL. Last element is the pk.
type keysetCursor struct {
	Keys []string `json:"k"`
}

// Keyset is the parsed cursor request for one list call.
type Keyset struct {
	Limit  int32
	Cursor null.String
	Sort   []SortField
}

func (k Keyset) Constrain() Keyset {
	if k.Limit <= 0 {
		k.Limit = 10
	}
	if k.Limit > 100 {
		k.Limit = 100
	}
	return k
}

// NormalizedSort is the sort tuple with the pk tiebreaker guaranteed last.
// Build and the caller (cursor key extraction) must agree on this exact order.
func (k Keyset) NormalizedSort(pk string) []SortField {
	sort := append([]SortField{}, k.Sort...)
	if len(sort) == 0 || sort[len(sort)-1].Field != pk {
		sort = append(sort, SortField{Field: pk, Dir: Asc})
	}
	return sort
}

// Build returns the keyset WHERE fragment (empty on first page), the ORDER BY
// clause, and named args for the cursor values. pk = logical name of the
// tiebreaker column (always appended ASC) for a total ordering.
func (k Keyset) Build(wl Whitelist, pk string) (where, order string, args map[string]any, err error) {
	sort := k.NormalizedSort(pk)

	cols := make([]SortCol, len(sort))
	for i, s := range sort {
		c, ok := wl[s.Field]
		if !ok {
			return "", "", nil, fmt.Errorf("sort field not allowed: %q", s.Field)
		}
		cols[i] = c
	}
	order = orderClause(sort, cols)
	args = map[string]any{}

	if !k.Cursor.Valid {
		return "", order, args, nil // first page: no keyset predicate
	}

	var c keysetCursor
	if err = decodeKeyset(k.Cursor.String, &c); err != nil {
		return "", "", nil, fmt.Errorf("decode cursor: %w", err)
	}
	if len(c.Keys) != len(sort) {
		return "", "", nil, fmt.Errorf("cursor arity %d != sort arity %d", len(c.Keys), len(sort))
	}

	// Lexicographic OR-chain. Row i: equality on prefix cols[0..i-1], comparator on cols[i].
	ors := make([]string, len(sort))
	for i := range sort {
		ands := make([]string, 0, i+1)
		for j := range i {
			key := fmt.Sprintf("k%d", j)
			args[key] = c.Keys[j]
			ands = append(ands, fmt.Sprintf("%s = @%s::%s", cols[j].Col, key, cols[j].Cast))
		}
		cmp := ">"
		if sort[i].Dir == Desc {
			cmp = "<"
		}
		key := fmt.Sprintf("k%d", i)
		args[key] = c.Keys[i]
		ands = append(ands, fmt.Sprintf("%s %s @%s::%s", cols[i].Col, cmp, key, cols[i].Cast))
		ors[i] = "(" + strings.Join(ands, " AND ") + ")"
	}
	return "(" + strings.Join(ors, " OR ") + ")", order, args, nil
}

func orderClause(sort []SortField, cols []SortCol) string {
	parts := make([]string, len(sort))
	for i, s := range sort {
		dir := "ASC"
		if s.Dir == Desc {
			dir = "DESC"
		}
		parts[i] = cols[i].Col + " " + dir
	}
	return strings.Join(parts, ", ")
}

// ParseSort reads a `?sort=-date_created,score` style param. `-` prefix = desc.
func ParseSort(raw string) []SortField {
	if raw == "" {
		return nil
	}
	var out []SortField
	for tok := range strings.SplitSeq(raw, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		dir := Asc
		switch tok[0] {
		case '-':
			dir, tok = Desc, tok[1:]
		case '+':
			tok = tok[1:]
		}
		out = append(out, SortField{Field: tok, Dir: dir})
	}
	return out
}

// EncodeKeyset builds the next cursor from the string-encoded sort-tuple values
// of the last row, in NormalizedSort order. nil => no next page.
func EncodeKeyset(keys []string) null.String {
	if keys == nil {
		return null.String{}
	}
	b, err := json.Marshal(keysetCursor{Keys: keys})
	if err != nil {
		return null.String{}
	}
	return null.StringFrom(base64.StdEncoding.EncodeToString(b))
}

func decodeKeyset(s string, dst any) error {
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, dst)
}
