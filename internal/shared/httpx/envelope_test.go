package httpx_test

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"shopnexus/internal/shared/errx"
	"shopnexus/internal/shared/httpx"
	"shopnexus/internal/shared/validation"
)

// decode into a map so the test sees the actual root keys, not what a typed struct is
// willing to ignore. The point of the envelope is which keys exist at the root.
func rootKeys(t *testing.T, body []byte) map[string]jsontext.Value {
	t.Helper()
	var m map[string]jsontext.Value
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("unmarshal: %v\nbody: %s", err, body)
	}
	return m
}

func TestWriteData_NestsPayloadUnderData(t *testing.T) {
	rec := httptest.NewRecorder()
	// A payload with a field named "error" — a Transaction has one. Bare at the root it
	// would be indistinguishable from a failure to any client that checks for "error".
	httpx.WriteData(rec, http.StatusCreated, map[string]any{"id": "txn_1", "error": "gateway declined"})

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rec.Code)
	}
	root := rootKeys(t, rec.Body.Bytes())
	if _, ok := root["error"]; ok {
		t.Error("payload's own \"error\" field reached the root; the envelope did not nest it")
	}
	if len(root) != 1 {
		t.Errorf("root keys = %v, want only data", keysOf(root))
	}
	// Tagged, not inferred: v2 matches member names case-sensitively, so an untagged `Error`
	// field looks for "Error" and reads the wire's "error" as absent.
	var inner struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(root["data"], &inner); err != nil || inner.Error != "gateway declined" {
		t.Errorf("payload not preserved under data: %v %+v", err, inner)
	}
}

func TestWritePage_PutsPaginationInMeta(t *testing.T) {
	rec := httptest.NewRecorder()
	total := int64(42)
	httpx.WritePage(rec, http.StatusOK, []string{"a", "b"}, httpx.PageMeta{Page: 1, Limit: 20, TotalCount: &total})

	root := rootKeys(t, rec.Body.Bytes())
	if _, ok := root["total_count"]; ok {
		t.Error("total_count is at the root; pagination must live in meta")
	}
	var meta httpx.PageMeta
	if err := json.Unmarshal(root["meta"], &meta); err != nil {
		t.Fatalf("meta: %v", err)
	}
	if meta.TotalCount == nil || *meta.TotalCount != 42 || meta.Page != 1 {
		t.Errorf("meta = %+v", meta)
	}
}

// A ranked query has no total. It must be an explicit null rather than a missing key, so
// a client can tell "no total exists" from "the server forgot".
func TestWritePage_NullTotalIsExplicit(t *testing.T) {
	rec := httptest.NewRecorder()
	httpx.WritePage(rec, http.StatusOK, []string{}, httpx.PageMeta{Page: 1, Limit: 20, TotalCount: nil})

	var body struct {
		Meta map[string]jsontext.Value `json:"meta"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	raw, ok := body.Meta["total_count"]
	if !ok {
		t.Fatal("total_count key is missing; it must be present and null")
	}
	if string(raw) != "null" {
		t.Errorf("total_count = %s, want null", raw)
	}
}

func TestWriteCursor_NullNextCursorIsExplicit(t *testing.T) {
	rec := httptest.NewRecorder()
	httpx.WriteCursor(rec, http.StatusOK, []string{"a"}, httpx.CursorMeta{NextCursor: nil})

	var body struct {
		Meta map[string]jsontext.Value `json:"meta"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if string(body.Meta["next_cursor"]) != "null" {
		t.Errorf("next_cursor = %s, want null", body.Meta["next_cursor"])
	}
}

func TestWriteNoContent_HasNoBody(t *testing.T) {
	rec := httptest.NewRecorder()
	httpx.WriteNoContent(rec)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body = %q, want empty", rec.Body.String())
	}
}

// The request id is what a user hands over when they report a failure, so it has to be in
// the body and not only in our logs.
func TestWriteError_CarriesRequestID(t *testing.T) {
	rec := httptest.NewRecorder()
	rec.Header().Set(httpx.RequestIDHeader, "dkav6vyqeqm3")
	httpx.WriteError(rec, slog.Default(), errx.NewError(http.StatusNotFound, "not_found", "missing"))

	var body struct {
		Error struct {
			Code      string       `json:"code"`
			Message   string       `json:"message"`
			RequestID string       `json:"request_id"`
			Fields    []errx.Field `json:"fields"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.RequestID != "dkav6vyqeqm3" {
		t.Errorf("request_id = %q, want the header's value", body.Error.RequestID)
	}
	// Present and empty, not absent: a zero value is normalised rather than omitted, so a
	// client iterates "fields" unconditionally instead of testing for the key first.
	if body.Error.Fields == nil {
		t.Error("fields is absent; an empty list must still be sent as []")
	}
	if len(body.Error.Fields) != 0 {
		t.Errorf("fields = %v, want empty on a non-validation error", body.Error.Fields)
	}
}

// The whole point of fix (1): a form with several problems must learn which inputs, not
// one sentence about all of them.
func TestWriteError_ValidationCarriesEveryField(t *testing.T) {
	type sku struct {
		Price int64 `json:"price" validate:"gt=0"`
	}
	req := struct {
		Email string `json:"email" validate:"required,email"`
		Name  string `json:"name"  validate:"required"`
		Skus  []sku  `json:"skus"  validate:"required,min=1,dive"`
	}{Email: "nope", Skus: []sku{{Price: 0}}}

	err := validation.AsError(validation.Default().Struct(req))

	rec := httptest.NewRecorder()
	httpx.WriteError(rec, slog.Default(), err)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	var body struct {
		Error struct {
			Code   string       `json:"code"`
			Fields []errx.Field `json:"fields"`
		} `json:"error"`
	}
	if e := json.Unmarshal(rec.Body.Bytes(), &body); e != nil {
		t.Fatal(e)
	}
	if body.Error.Code != "validation" {
		t.Errorf("code = %q", body.Error.Code)
	}
	got := map[string]string{}
	for _, f := range body.Error.Fields {
		got[f.Field] = f.Rule
	}
	// The names are the ones the client sent, with the index of the offending row —
	// anything else and a form cannot find the input to mark.
	for field, rule := range map[string]string{"email": "email", "name": "required", "skus[0].price": "gt"} {
		if got[field] != rule {
			t.Errorf("field %q rule = %q, want %q (got all: %v)", field, got[field], rule, got)
		}
	}
}

// A non-validation error passed through the translator must survive untouched, or every
// programming mistake would come out as a 400.
func TestValidationAsError_PassesThroughOtherErrors(t *testing.T) {
	orig := errx.NewError(http.StatusConflict, "conflict", "nope")
	if got := validation.AsError(orig); got != orig {
		t.Errorf("AsError rewrote a non-validation error: %v", got)
	}
	if validation.AsError(nil) != nil {
		t.Error("AsError(nil) must be nil")
	}
}

func keysOf(m map[string]jsontext.Value) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
