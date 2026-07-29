package httpx_test

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"shopnexus/internal/shared/errx"
	"shopnexus/internal/shared/httpx"
)

func TestWriteError_MapsCodedErrorToStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	httpx.WriteError(rec, slog.Default(), errx.NewError(http.StatusNotFound, "not_found", "missing"))
	if rec.Code != 404 {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	var body struct {
		Error struct{ Code, Message string } `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Error.Code != "not_found" {
		t.Fatalf("code = %q, want not_found", body.Error.Code)
	}
	if body.Error.Message != "missing" {
		t.Fatalf("message = %q, want missing", body.Error.Message)
	}
}

func TestWriteError_UnknownIs500(t *testing.T) {
	rec := httptest.NewRecorder()
	httpx.WriteError(rec, slog.Default(), errorsNew("boom"))
	if rec.Code != 500 {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func errorsNew(s string) error { return &simpleErr{s} }

type simpleErr struct{ s string }

func (e *simpleErr) Error() string { return e.s }
