package repolist

import (
	"fmt"
	"strings"

	"github.com/guregu/null/v6"
)

// Cond is one optional WHERE predicate. The zero value (empty expr) is skipped,
// so a filter that isn't set contributes nothing. expr references @arg, which is
// bound to val unless arg is empty (literal predicates like Raw).
type Cond struct {
	expr string
	arg  string
	val  any
}

func (c Cond) skip() bool { return c.expr == "" }

// argName derives the named-arg key from a quoted column ref: `"date_created"` -> "date_created".
func argName(col string) string { return strings.Trim(col, `"`) }

// In emits `col = ANY(@col)`. A nil slice skips the filter; a non-nil empty
// slice matches nothing (an explicit empty set returns no rows, not all rows).
func In[E any](col string, vals []E) Cond {
	if vals == nil {
		return Cond{}
	}
	a := argName(col)
	return Cond{expr: fmt.Sprintf(`%s = ANY(@%s)`, col, a), arg: a, val: vals}
}

// Gte emits `col >= @col_from` when the bound is a Valid null value; else skips.
func Gte(col string, bound any) Cond {
	v, ok := boundVal(bound)
	if !ok {
		return Cond{}
	}
	a := argName(col) + "_from"
	return Cond{expr: fmt.Sprintf(`%s >= @%s`, col, a), arg: a, val: v}
}

// Lte emits `col <= @col_to` when the bound is a Valid null value; else skips.
func Lte(col string, bound any) Cond {
	v, ok := boundVal(bound)
	if !ok {
		return Cond{}
	}
	a := argName(col) + "_to"
	return Cond{expr: fmt.Sprintf(`%s <= @%s`, col, a), arg: a, val: v}
}

// Raw is a literal predicate with no bound arg, e.g. `"date_deleted" IS NULL`.
func Raw(expr string) Cond { return Cond{expr: expr} }

// Expr is a literal predicate that binds one named arg, e.g. an ILIKE search:
//
//	Expr(`("name" ILIKE @q OR "slug" ILIKE @q)`, "q", "%"+term+"%")
func Expr(expr, arg string, val any) Cond { return Cond{expr: expr, arg: arg, val: val} }

// boundVal unwraps a guregu null range bound to its value (type switch, no
// reflection). Returns ok=false when the bound is unset.
func boundVal(bound any) (any, bool) {
	switch v := bound.(type) {
	case null.Int:
		if v.Valid {
			return v.Int64, true
		}
	case null.Float:
		if v.Valid {
			return v.Float64, true
		}
	case null.Time:
		if v.Valid {
			return v.Time, true
		}
	}
	return nil, false
}
