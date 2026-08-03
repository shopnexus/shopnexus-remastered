// Package bgem3 talks to the BGE-M3 embedding service — the small Python process that holds
// the model, because the model is a GPU-shaped dependency and this platform is not.
//
// One call returns both halves of the embedding, which is the reason for the round trip: a
// dense vector for meaning and a sparse one for the words, produced from a single pass over
// the text rather than by two models that would disagree about how they tokenised it.
package bgem3

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"shopnexus/internal/provider/embedding"
)

// Name is the EMBEDDING_PROVIDER value that selects this client.
const Name = "bge-m3"

type Config struct {
	// BaseURL is where the model service answers, e.g. http://embedding:5007.
	BaseURL string
	// Dimensions is the dense width this deployment's schema stores. Checked against every
	// answer rather than trusted: the two are set in different repositories.
	Dimensions int
	// Timeout bounds one batch. Generous by the standards of the other providers — this is a
	// transformer over a batch of documents on a CPU, not an API call.
	Timeout    time.Duration
	HTTPClient *http.Client
}

type Client struct {
	cfg  Config
	http *http.Client
}

func New(cfg Config) (*Client, error) {
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("bgem3: no base url")
	}
	if cfg.Dimensions <= 0 {
		return nil, fmt.Errorf("bgem3: dimensions must be positive, got %d", cfg.Dimensions)
	}
	if cfg.Timeout <= 0 {
		return nil, fmt.Errorf("bgem3: no timeout")
	}
	c := &Client{cfg: cfg, http: cfg.HTTPClient}
	if c.http == nil {
		// No http.Client.Timeout on purpose: it covers reading the body, and the per-call
		// budget belongs on the context (see the outbound-deadlines rule).
		c.http = &http.Client{}
	}
	return c, nil
}

var _ embedding.Client = (*Client)(nil)

func (c *Client) Name() string { return Name }

type embedRequest struct {
	Texts []string `json:"texts"`
}

// The service reports sparse weights as a JSON object keyed by the vocabulary index written
// out as a string, which is what a JSON object key has to be.
type embedResponse struct {
	Embeddings []struct {
		Dense  []float32          `json:"dense"`
		Sparse map[string]float32 `json:"sparse"`
	} `json:"embeddings"`
	Error string `json:"error"`
}

func (c *Client) Embed(ctx context.Context, texts []string) ([]embedding.Vector, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()

	body, err := json.Marshal(embedRequest{Texts: texts})
	if err != nil {
		return nil, fmt.Errorf("encode embed request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+"/embed", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build embed request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	res, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call embedding service: %w", err)
	}
	defer func() { _ = res.Body.Close() }()

	var decoded embedResponse
	if err := json.NewDecoder(res.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode embed response (status %d): %w", res.StatusCode, err)
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embedding service answered %d: %s", res.StatusCode, decoded.Error)
	}
	// Positional pairing is the caller's contract, so a short answer has to fail rather than
	// leave one row wearing another's vector.
	if len(decoded.Embeddings) != len(texts) {
		return nil, fmt.Errorf("embedding service returned %d vectors for %d texts",
			len(decoded.Embeddings), len(texts))
	}

	out := make([]embedding.Vector, len(decoded.Embeddings))
	for i, e := range decoded.Embeddings {
		if len(e.Dense) != c.cfg.Dimensions {
			return nil, fmt.Errorf("%w: model gave %d, this deployment stores %d",
				embedding.ErrUnexpectedDimensions, len(e.Dense), c.cfg.Dimensions)
		}
		sparse := make(map[uint32]float32, len(e.Sparse))
		for key, weight := range e.Sparse {
			idx, err := strconv.ParseUint(key, 10, 32)
			if err != nil {
				return nil, fmt.Errorf("parse sparse index %q: %w", key, err)
			}
			sparse[uint32(idx)] = weight
		}
		out[i] = embedding.Vector{Dense: e.Dense, Sparse: sparse}
	}
	return out, nil
}
