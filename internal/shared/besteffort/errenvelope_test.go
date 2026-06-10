package besteffort

import (
	"encoding/json"
	"testing"

	"shopnexus-server/internal/shared/errors"

	restate "github.com/restatedev/sdk-go"
)

func TestErrorEnvelopeRoundTrip(t *testing.T) {
	orig := errors.NewError(404, "x_not_found", "x not found")

	env := EncodeError(orig)
	if env.HTTPStatus != 404 || env.Code != "x_not_found" || env.Message != "x not found" {
		t.Fatalf("encode mismatch: %+v", env)
	}

	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	var got Envelope
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}

	decoded := DecodeError(got)
	status, code, msg, ok := errors.Decompose(decoded)
	if !ok {
		t.Fatal("decoded error is not a coded domain error")
	}
	if code != "x_not_found" {
		t.Errorf("code = %q, want x_not_found", code)
	}
	if status != 404 {
		t.Errorf("status = %d, want 404", status)
	}
	if msg != "x not found" {
		t.Errorf("message = %q, want %q", msg, "x not found")
	}
	if !restate.IsTerminalError(decoded) {
		t.Error("decoded error is not terminal")
	}
}

func TestEncodeNonCoded(t *testing.T) {
	env := EncodeError(stdError("boom"))
	if env.HTTPStatus != 500 || env.Code != "internal" || env.Message != "boom" {
		t.Fatalf("non-coded encode mismatch: %+v", env)
	}
}

type stdError string

func (e stdError) Error() string { return string(e) }
