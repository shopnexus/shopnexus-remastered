package besteffort

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"shopnexus-server/internal/shared/errors"
)

type echoReq struct {
	Msg string `json:"msg"`
}

type echoResp struct {
	Msg string `json:"msg"`
}

func newTestServer() *Server {
	srv := NewServer()
	srv.Handle("Test", "Echo", func(_ context.Context, body []byte) (any, error) {
		var in echoReq
		if err := json.Unmarshal(body, &in); err != nil {
			return nil, err
		}
		if in.Msg == "fail" {
			return nil, errors.NewError(409, "boom", "boom")
		}
		return echoResp{Msg: in.Msg}, nil
	})
	return srv
}

func TestCallRoundTrip(t *testing.T) {
	ts := httptest.NewServer(newTestServer().Handler())
	defer ts.Close()

	c := NewCallClient(ts.URL)

	out, err := Call[echoResp](context.Background(), c, "Test", "Echo", echoReq{Msg: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Msg != "hello" {
		t.Errorf("round-trip msg = %q, want hello", out.Msg)
	}
}

func TestCallDomainError(t *testing.T) {
	ts := httptest.NewServer(newTestServer().Handler())
	defer ts.Close()

	c := NewCallClient(ts.URL)

	_, err := Call[echoResp](context.Background(), c, "Test", "Echo", echoReq{Msg: "fail"})
	if err == nil {
		t.Fatal("expected error")
	}
	status, code, _, ok := errors.Decompose(err)
	if !ok {
		t.Fatalf("error is not a coded domain error: %v", err)
	}
	if code != "boom" {
		t.Errorf("code = %q, want boom", code)
	}
	if status != 409 {
		t.Errorf("status = %d, want 409", status)
	}
}
