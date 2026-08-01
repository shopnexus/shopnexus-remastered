package errx_test

import (
	"net/http"
	"testing"

	"shopnexus/internal/shared/errx"
)

func TestNewError_Decompose(t *testing.T) {
	err := errx.NewError(http.StatusNotFound, "listing_not_found", "listing not found")
	status, code, message, ok := errx.Decompose(err)
	if !ok {
		t.Fatal("expected Decompose to recognize a coded error")
	}
	if status != http.StatusNotFound {
		t.Errorf("status = %d, want 404", status)
	}
	if code != "listing_not_found" {
		t.Errorf("code = %q, want listing_not_found", code)
	}
	if message != "listing not found" {
		t.Errorf("message = %q, want %q", message, "listing not found")
	}
}

func TestErrorf_FmtFillsMessage(t *testing.T) {
	tmpl := errx.NewErrorf(http.StatusNotFound, "entity_not_found", "%s not found")
	err := tmpl.Fmt("account")
	_, code, message, ok := errx.Decompose(err)
	if !ok {
		t.Fatal("expected Decompose to recognize a coded error")
	}
	if code != "entity_not_found" {
		t.Errorf("code = %q, want entity_not_found", code)
	}
	if message != "account not found" {
		t.Errorf("message = %q, want %q", message, "account not found")
	}
}

func TestDecompose_PlainErrorNotRecognized(t *testing.T) {
	if _, _, _, ok := errx.Decompose(errPlain{}); ok {
		t.Fatal("plain error must not be recognized as coded")
	}
}

func TestNewError_PanicsOnBadStatus(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for non-4xx/5xx status")
		}
	}()
	_ = errx.NewError(200, "ok", "should panic")
}

type errPlain struct{}

func (errPlain) Error() string { return "plain" }
