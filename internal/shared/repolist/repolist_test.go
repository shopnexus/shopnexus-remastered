package repolist

import (
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"shopnexus-server/internal/shared/paginate"
)

func testSpec() Spec[struct{}] {
	return Spec[struct{}]{
		Table: `"x"."y"`,
		PK:    "id",
		Whitelist: paginate.Whitelist{
			"id":           {Col: `"id"`, Cast: "uuid"},
			"date_created": {Col: `"date_created"`, Cast: "timestamptz"},
		},
		BindConds: func(conds *[]string, args pgx.NamedArgs) {
			*conds = append(*conds, `"a" = ANY(@a)`)
			args["a"] = []int64{1, 2}
		},
	}
}

func bind(spec Spec[struct{}]) ([]string, pgx.NamedArgs) {
	conds := []string{}
	args := pgx.NamedArgs{}
	spec.BindConds(&conds, args)
	return conds, args
}

func TestBuildOffset(t *testing.T) {
	spec := testSpec()
	conds, args := bind(spec)

	sql, listArgs := buildOffset(spec, conds, args, 10, 20)

	want := `SELECT * FROM "x"."y" WHERE "a" = ANY(@a) ORDER BY "id" LIMIT @limit OFFSET @offset`
	if sql != want {
		t.Fatalf("offset sql\n got: %s\nwant: %s", sql, want)
	}
	if listArgs["limit"] != int32(10) || listArgs["offset"] != int32(20) {
		t.Fatalf("offset args limit/offset = %v/%v", listArgs["limit"], listArgs["offset"])
	}
	if _, ok := listArgs["a"]; !ok {
		t.Fatalf("offset args missing filter arg a: %v", listArgs)
	}
}

func TestBuildCount(t *testing.T) {
	spec := testSpec()
	conds, _ := bind(spec)

	want := `SELECT COUNT(*) FROM "x"."y" WHERE "a" = ANY(@a)`
	if got := countSQL(spec.Table, conds); got != want {
		t.Fatalf("count sql\n got: %s\nwant: %s", got, want)
	}
}

func TestBuildKeysetFirstPage(t *testing.T) {
	spec := testSpec()
	conds, args := bind(spec)
	ks := paginate.Keyset{Limit: 10, Sort: []paginate.SortField{{Field: "date_created", Dir: paginate.Desc}}}

	sql, err := buildKeyset(spec, ks, conds, args, 10)
	if err != nil {
		t.Fatal(err)
	}

	// First page: no keyset predicate, just the filter; pk appended to ORDER BY.
	want := `SELECT * FROM "x"."y" WHERE "a" = ANY(@a) ORDER BY "date_created" DESC, "id" ASC LIMIT @limit`
	if sql != want {
		t.Fatalf("keyset first-page sql\n got: %s\nwant: %s", sql, want)
	}
	if args["limit"] != int32(11) { // peek
		t.Fatalf("keyset peek limit = %v, want 11", args["limit"])
	}
}

func TestBuildKeysetWithCursor(t *testing.T) {
	spec := testSpec()
	conds, args := bind(spec)

	// Cursor encodes the tuple (date_created, id) of the last row.
	cursor := paginate.EncodeKeyset([]string{"2024-01-02T03:04:05Z", "abc"})
	ks := paginate.Keyset{
		Limit:  10,
		Cursor: cursor,
		Sort:   []paginate.SortField{{Field: "date_created", Dir: paginate.Desc}},
	}

	sql, err := buildKeyset(spec, ks, conds, args, 10)
	if err != nil {
		t.Fatal(err)
	}

	// OR-chain: comparator on date_created (DESC => "<"), then equality + pk tiebreaker (ASC => ">").
	for _, frag := range []string{
		`"a" = ANY(@a)`,
		`"date_created" < @k0::timestamptz`,
		`"date_created" = @k0::timestamptz AND "id" > @k1::uuid`,
		`ORDER BY "date_created" DESC, "id" ASC`,
	} {
		if !strings.Contains(sql, frag) {
			t.Fatalf("keyset sql missing %q\nfull: %s", frag, sql)
		}
	}
	if args["k0"] != "2024-01-02T03:04:05Z" || args["k1"] != "abc" {
		t.Fatalf("keyset cursor args k0/k1 = %v/%v", args["k0"], args["k1"])
	}
}

func TestBuildKeysetRejectsUnknownSort(t *testing.T) {
	spec := testSpec()
	conds, args := bind(spec)
	ks := paginate.Keyset{Limit: 10, Sort: []paginate.SortField{{Field: "evil", Dir: paginate.Asc}}}

	if _, err := buildKeyset(spec, ks, conds, args, 10); err == nil {
		t.Fatal("expected error for non-whitelisted sort field")
	}
}

func TestBuildAll(t *testing.T) {
	spec := testSpec()
	conds, _ := bind(spec)

	want := `SELECT * FROM "x"."y" WHERE "a" = ANY(@a) ORDER BY "id"`
	if got := buildAll(spec, conds); got != want {
		t.Fatalf("all sql\n got: %s\nwant: %s", got, want)
	}
}
