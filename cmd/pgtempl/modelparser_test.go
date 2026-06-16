package main

import (
	"go/parser"
	"go/token"
	"testing"
)

func TestCollectStructsReadsDBTagsAndTypes(t *testing.T) {
	const src = "package repo\n" +
		"import (\n" +
		"\t\"github.com/google/uuid\"\n" +
		"\torderdb \"shopnexus-server/internal/module/order/db/sqlc\"\n" +
		")\n" +
		"type Refund struct {\n" +
		"\tID     uuid.UUID                 `db:\"id\"`\n" +
		"\tStatus orderdb.OrderRefundStatus `db:\"status\"`\n" +
		"\tExtra  string\n" +
		"}\n"

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "x.go", src, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}

	got := collectStructs(fset, f)
	rf, ok := got["Refund"]
	if !ok {
		t.Fatal("Refund struct not found")
	}
	if len(rf.Fields) != 2 {
		t.Fatalf("got %d db-tagged fields, want 2 (Extra must be skipped)", len(rf.Fields))
	}

	id := rf.ByDB["id"]
	if id.GoName != "ID" || id.GoType != "uuid.UUID" {
		t.Fatalf("id field = %+v, want GoName=ID GoType=uuid.UUID", id)
	}
	st := rf.ByDB["status"]
	if st.GoType != "orderdb.OrderRefundStatus" {
		t.Fatalf("status GoType = %q, want orderdb.OrderRefundStatus", st.GoType)
	}

	if rf.Imports["uuid"] != "github.com/google/uuid" {
		t.Fatalf("uuid import not captured: %v", rf.Imports)
	}
	if rf.Imports["orderdb"] != "shopnexus-server/internal/module/order/db/sqlc" {
		t.Fatalf("orderdb import not captured: %v", rf.Imports)
	}
}

func TestCollectStructsReadsTableMarkerAndPkg(t *testing.T) {
	const src = "package ordermodel\n" +
		"import \"github.com/google/uuid\"\n" +
		"//pgtempl:table \"order\".\"refund\"\n" +
		"type Refund struct {\n" +
		"\tID uuid.UUID `db:\"id\"`\n" +
		"}\n" +
		"type NotAnEntity struct{ X int }\n"
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "x.go", src, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	got := collectStructs(fset, f)
	if got["Refund"].Table != `"order"."refund"` {
		t.Fatalf("Refund.Table = %q", got["Refund"].Table)
	}
	if got["NotAnEntity"].Table != "" {
		t.Fatal("NotAnEntity must have empty Table")
	}
}
