package repolist

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

type testRow struct {
	ID          string    `json:"id"`
	DateCreated time.Time `json:"date_created"`
	A           int64     `json:"a"`
}

func testQuery() Query[testRow] {
	return Query[testRow]{
		Table: `"x"."y"`,
		PK:    "id",
		Sort:  []string{"id", "date_created"},
		Fields: func(m *testRow) map[string]any {
			return map[string]any{"id": &m.ID, "date_created": &m.DateCreated, "a": &m.A}
		},
		Where: []Cond{In(`"a"`, []int64{1, 2})},
	}
}

// conds mirrors what List derives from Where, for builder tests.
func conds(q Query[testRow]) []string {
	var out []string
	for _, c := range q.Where {
		if !c.skip() {
			out = append(out, c.expr)
		}
	}
	return out
}

func TestOffsetSQL(t *testing.T) {
	q := testQuery()
	want := `SELECT "a", "date_created", "id" FROM "x"."y" WHERE "a" = ANY(@a) ORDER BY "id" LIMIT @limit OFFSET @offset`
	if got := q.offsetSQL(conds(q)); got != want {
		t.Fatalf("offset sql\n got: %s\nwant: %s", got, want)
	}
}

func TestCountSQL(t *testing.T) {
	q := testQuery()
	want := `SELECT COUNT(*) FROM "x"."y" WHERE "a" = ANY(@a)`
	if got := q.countSQL(conds(q)); got != want {
		t.Fatalf("count sql\n got: %s\nwant: %s", got, want)
	}
}

func TestAllSQL(t *testing.T) {
	q := testQuery()
	want := `SELECT "a", "date_created", "id" FROM "x"."y" WHERE "a" = ANY(@a) ORDER BY "id"`
	if got := q.allSQL(conds(q)); got != want {
		t.Fatalf("all sql\n got: %s\nwant: %s", got, want)
	}
}

func TestKeysetFirstPage(t *testing.T) {
	q := testQuery()
	sort, err := q.sortTuple("-date_created")
	if err != nil {
		t.Fatal(err)
	}
	// pk tiebreaker appended ASC; no keyset predicate on the first page.
	want := `SELECT "a", "date_created", "id" FROM "x"."y" WHERE "a" = ANY(@a) ORDER BY "date_created" DESC, "id" ASC LIMIT @limit`
	if got := q.keysetSQL(conds(q), sort); got != want {
		t.Fatalf("keyset first-page sql\n got: %s\nwant: %s", got, want)
	}
}

func TestKeysetWhereNoCast(t *testing.T) {
	q := testQuery()
	sort, err := q.sortTuple("-date_created")
	if err != nil {
		t.Fatal(err)
	}
	// Typed-JSON cursor: a quoted RFC3339 time and a quoted string id.
	ts, _ := time.Parse(time.RFC3339, "2024-01-02T03:04:05Z")
	keys, err := q.encodeCursorKeys(sort, testRow{ID: "abc", DateCreated: ts})
	if err != nil {
		t.Fatal(err)
	}

	args := map[string]any{}
	where, err := q.keysetWhere(sort, keys, args)
	if err != nil {
		t.Fatal(err)
	}
	// OR-chain, comparators only — no ::cast anywhere.
	for _, frag := range []string{
		`"date_created" < @k0`,
		`"date_created" = @k0 AND "id" > @k1`,
	} {
		if !strings.Contains(where, frag) {
			t.Fatalf("keyset where missing %q\nfull: %s", frag, where)
		}
	}
	if strings.Contains(where, "::") {
		t.Fatalf("keyset where should carry no cast: %s", where)
	}
}

func TestCursorTypeFidelity(t *testing.T) {
	q := testQuery()
	sort, err := q.sortTuple("-date_created")
	if err != nil {
		t.Fatal(err)
	}
	ts := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	keys, err := q.encodeCursorKeys(sort, testRow{ID: "abc", DateCreated: ts})
	if err != nil {
		t.Fatal(err)
	}

	// Decode back into a typed probe and assert exact round-trip.
	probe := new(testRow)
	fm := q.Fields(probe)
	for i, s := range sort {
		if err := json.Unmarshal(keys[i], fm[s.Field]); err != nil {
			t.Fatal(err)
		}
	}
	if !probe.DateCreated.Equal(ts) {
		t.Fatalf("time round-trip: got %v want %v", probe.DateCreated, ts)
	}
	if probe.ID != "abc" {
		t.Fatalf("id round-trip: got %q want %q", probe.ID, "abc")
	}
}

func TestSortRejectsUnknown(t *testing.T) {
	q := testQuery()
	if _, err := q.sortTuple("evil"); err == nil {
		t.Fatal("expected error for non-whitelisted sort field")
	}
}

// TestComposedCTEOrder mirrors the hybrid-search shape: a CTE prefix, a ranked
// subquery as the FROM source, and a relevance Order override.
func TestComposedCTEOrder(t *testing.T) {
	q := Query[testRow]{
		With:  `WITH ranked AS (SELECT 1)`,
		Table: `(SELECT t.*, 1 AS score FROM "x"."y" t) t`,
		PK:    "id",
		Order: "score DESC",
		Fields: func(m *testRow) map[string]any {
			return map[string]any{"id": &m.ID, "a": &m.A}
		},
	}
	wantPage := `WITH ranked AS (SELECT 1) SELECT "a", "id" FROM (SELECT t.*, 1 AS score FROM "x"."y" t) t ORDER BY score DESC LIMIT @limit OFFSET @offset`
	if got := q.offsetSQL(nil); got != wantPage {
		t.Fatalf("composed page sql\n got: %s\nwant: %s", got, wantPage)
	}
	wantCount := `WITH ranked AS (SELECT 1) SELECT COUNT(*) FROM (SELECT t.*, 1 AS score FROM "x"."y" t) t`
	if got := q.countSQL(nil); got != wantCount {
		t.Fatalf("composed count sql\n got: %s\nwant: %s", got, wantCount)
	}
}
