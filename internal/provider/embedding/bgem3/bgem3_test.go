package bgem3_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"shopnexus/internal/provider/embedding"
	"shopnexus/internal/provider/embedding/bgem3"
)

func newClient(t *testing.T, dims int, h http.HandlerFunc) *bgem3.Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := bgem3.New(bgem3.Config{BaseURL: srv.URL, Dimensions: dims, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestEmbedReadsBothHalves(t *testing.T) {
	var got struct {
		Texts []string `json:"texts"`
	}
	c := newClient(t, 3, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embed" || r.Method != http.MethodPost {
			t.Errorf("called %s %s, want POST /embed", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		// The service keys sparse weights by the vocabulary index as a string, because that is
		// what a JSON object key has to be.
		_, _ = w.Write([]byte(`{"embeddings":[
			{"dense":[0.1,0.2,0.3],"sparse":{"7":0.5,"250001":0.25}}
		]}`))
	})

	out, err := c.Embed(context.Background(), []string{"running shoes"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(got.Texts) != 1 || got.Texts[0] != "running shoes" {
		t.Errorf("sent %v, want the text verbatim", got.Texts)
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

// A model of the wrong width does not degrade: every row fails for ever, because the column is
// declared at one size. Naming it is the difference between a five-minute fix and an afternoon.
func TestEmbedRefusesTheWrongDimensions(t *testing.T) {
	c := newClient(t, 1024, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"embeddings":[{"dense":[0.1,0.2,0.3],"sparse":{}}]}`))
	})
	_, err := c.Embed(context.Background(), []string{"anything"})
	if !errors.Is(err, embedding.ErrUnexpectedDimensions) {
		t.Fatalf("err = %v, want ErrUnexpectedDimensions", err)
	}
}

// Results are paired with rows positionally, so a short answer would attach one row's vector to
// another. It has to fail rather than be trimmed to fit.
func TestEmbedRefusesAShortAnswer(t *testing.T) {
	c := newClient(t, 1, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"embeddings":[{"dense":[0.1],"sparse":{}}]}`))
	})
	if _, err := c.Embed(context.Background(), []string{"one", "two"}); err == nil {
		t.Error("Embed accepted one vector for two texts")
	}
}

func TestEmbedSurfacesTheServiceError(t *testing.T) {
	c := newClient(t, 1, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"model not loaded"}`))
	})
	_, err := c.Embed(context.Background(), []string{"x"})
	if err == nil {
		t.Fatal("Embed succeeded on a 500")
	}
	if !strings.Contains(err.Error(), "model not loaded") {
		t.Errorf("err = %v, want the service's own message in it", err)
	}
}

// An empty batch is not a request: the caller drains a queue, and a pass that found nothing
// must not wake the model.
func TestEmbedSkipsTheCallForNoTexts(t *testing.T) {
	c := newClient(t, 1, func(w http.ResponseWriter, r *http.Request) {
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
