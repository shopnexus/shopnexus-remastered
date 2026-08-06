package bgem3_test

import (
	"context"
	"encoding/json/v2"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"shopnexus/internal/provider/embedding"
	"shopnexus/internal/provider/embedding/bgem3"
)

func newClient(t *testing.T, cfg bgem3.Config, h http.HandlerFunc) *bgem3.Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	cfg.BaseURL = srv.URL
	if cfg.Dimensions == 0 {
		cfg.Dimensions = 3
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 5 * time.Second
	}
	c, err := bgem3.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestEmbedReadsBothHalves(t *testing.T) {
	var got struct {
		Texts        []string `json:"texts"`
		ReturnDense  bool     `json:"return_dense"`
		ReturnSparse bool     `json:"return_sparse"`
	}
	var auth string
	c := newClient(t, bgem3.Config{APIKey: "secret"}, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embed" || r.Method != http.MethodPost {
			t.Errorf("called %s %s, want POST /embed", r.Method, r.URL.Path)
		}
		auth = r.Header.Get("Authorization")
		_ = json.UnmarshalRead(r.Body, &got)
		// Column-major, one array per half — the service's own shape. Sparse weights are keyed
		// by the vocabulary index, because that is what a JSON object key has to be.
		_, _ = w.Write([]byte(`{"model":"BAAI/bge-m3",
			"dense":[[0.1,0.2,0.3]],
			"sparse":[{"7":0.5,"250001":0.25}],
			"colbert":null,
			"usage":{"texts":1}}`))
	})

	out, err := c.Embed(context.Background(), []string{"running shoes"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if auth != "Bearer secret" {
		t.Errorf("Authorization = %q, want the bearer token", auth)
	}
	if len(got.Texts) != 1 || got.Texts[0] != "running shoes" {
		t.Errorf("sent %v, want the text verbatim", got.Texts)
	}
	// Asked for, never left to the service's defaults: which halves come back decides whether
	// a row can be written at all.
	if !got.ReturnDense || !got.ReturnSparse {
		t.Errorf("asked for dense=%v sparse=%v, want both", got.ReturnDense, got.ReturnSparse)
	}
	if len(out) != 1 {
		t.Fatalf("got %d vectors, want 1", len(out))
	}
	if len(out[0].Dense) != 3 {
		t.Errorf("dense = %v, want 3 values", out[0].Dense)
	}
	// Zero-based, exactly as the model reported them: the one-based shift is pgvector's
	// convention and belongs to the adapter that writes the literal.
	if out[0].Sparse[7] != 0.5 || out[0].Sparse[250001] != 0.25 {
		t.Errorf("sparse = %v, want the model's own indices", out[0].Sparse)
	}
}

// A service run open (ALLOW_NO_AUTH) is a real deployment, so an unset key is no header at all
// rather than an empty bearer — which reads as a wrong key and answers 401.
func TestEmbedSendsNoAuthHeaderWithoutAKey(t *testing.T) {
	seen := true
	c := newClient(t, bgem3.Config{}, func(w http.ResponseWriter, r *http.Request) {
		_, seen = r.Header["Authorization"]
		_, _ = w.Write([]byte(`{"dense":[[0.1,0.2,0.3]],"sparse":[{}]}`))
	})
	if _, err := c.Embed(context.Background(), []string{"x"}); err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if seen {
		t.Error("sent an Authorization header with no key configured")
	}
}

// A model of the wrong width does not degrade: every row fails for ever, because the column is
// declared at one size. Naming it is the difference between a five-minute fix and an afternoon.
func TestEmbedRefusesTheWrongDimensions(t *testing.T) {
	c := newClient(t, bgem3.Config{Dimensions: 1024}, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"dense":[[0.1,0.2,0.3]],"sparse":[{}]}`))
	})
	_, err := c.Embed(context.Background(), []string{"anything"})
	if !errors.Is(err, embedding.ErrUnexpectedDimensions) {
		t.Fatalf("err = %v, want ErrUnexpectedDimensions", err)
	}
}

// Results are paired with rows positionally, so a short answer would attach one row's vector to
// another. It has to fail rather than be trimmed to fit.
func TestEmbedRefusesAShortAnswer(t *testing.T) {
	c := newClient(t, bgem3.Config{Dimensions: 1}, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"dense":[[0.1]],"sparse":[{}]}`))
	})
	if _, err := c.Embed(context.Background(), []string{"one", "two"}); err == nil {
		t.Error("Embed accepted one vector for two texts")
	}
}

// Sparse was asked for, so an answer without it is not a partial success: the row would go in
// dense-only and the lexical half of search could never match it.
func TestEmbedRefusesAMissingHalf(t *testing.T) {
	c := newClient(t, bgem3.Config{Dimensions: 1}, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"dense":[[0.1]],"sparse":null}`))
	})
	if _, err := c.Embed(context.Background(), []string{"one"}); err == nil {
		t.Error("Embed accepted an answer with no sparse half")
	}
}

// FastAPI raises a string for a request it refused...
func TestEmbedSurfacesTheServiceError(t *testing.T) {
	c := newClient(t, bgem3.Config{Dimensions: 1}, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"detail":"server is overloaded, retry shortly"}`))
	})
	_, err := c.Embed(context.Background(), []string{"x"})
	if err == nil {
		t.Fatal("Embed succeeded on a 503")
	}
	if !strings.Contains(err.Error(), "server is overloaded") {
		t.Errorf("err = %v, want the service's own message in it", err)
	}
}

// ...and a list for a body that did not parse. Printing the value whole is what says which of
// the two happened.
func TestEmbedSurfacesAValidationDetail(t *testing.T) {
	c := newClient(t, bgem3.Config{Dimensions: 1}, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"detail":[{"type":"too_short","loc":["body","texts"]}]}`))
	})
	_, err := c.Embed(context.Background(), []string{"x"})
	if err == nil {
		t.Fatal("Embed succeeded on a 422")
	}
	if !strings.Contains(err.Error(), "too_short") {
		t.Errorf("err = %v, want the field problem in it", err)
	}
}

// A 401 must not be reported as a decode failure: the body is an error envelope, so reading it
// as an answer would name a missing field instead of the reason.
func TestEmbedSurfacesUnauthorized(t *testing.T) {
	c := newClient(t, bgem3.Config{APIKey: "wrong", Dimensions: 1}, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"detail":"invalid or missing API key"}`))
	})
	_, err := c.Embed(context.Background(), []string{"x"})
	if err == nil || !strings.Contains(err.Error(), "invalid or missing API key") {
		t.Fatalf("err = %v, want the service's 401 message", err)
	}
}

// An empty batch is not a request: the caller drains a queue, and a pass that found nothing
// must not wake the model.
func TestEmbedSkipsTheCallForNoTexts(t *testing.T) {
	c := newClient(t, bgem3.Config{Dimensions: 1}, func(w http.ResponseWriter, r *http.Request) {
		t.Error("the model was called for an empty batch")
	})
	out, err := c.Embed(context.Background(), nil)
	if err != nil || out != nil {
		t.Errorf("Embed(nil) = %v, %v; want nil, nil", out, err)
	}
}

func TestNewRejectsUnusableConfig(t *testing.T) {
	for _, cfg := range []bgem3.Config{
		{Dimensions: 1024, Timeout: time.Second},
		{BaseURL: "http://x", Timeout: time.Second},
		{BaseURL: "http://x", Dimensions: 1024},
	} {
		if _, err := bgem3.New(cfg); err == nil {
			t.Errorf("New(%+v) succeeded, want it refused", cfg)
		}
	}
}
