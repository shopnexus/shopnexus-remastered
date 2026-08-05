// Package bgem3 talks to the BGE-M3 embedding service — the small Python process that holds
// the model, because the model is a GPU-shaped dependency and this platform is not.
//
// One call returns both halves of the embedding, which is the reason for the round trip: a
// dense vector for meaning and a sparse one for the words, produced from a single pass over
// the text rather than by two models that would disagree about how they tokenised it.
//
// The service is FastAPI, so its two envelopes are its own: an answer is column-major
// (`{"dense": [[...]], "sparse": [{...}]}` — one array per half, not one object per text) and
// a failure is `{"detail": ...}`, a message for a refused request and a list of field problems
// for one that did not parse.
package bgem3

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"shopnexus/internal/provider/embedding"
)

// Name is the EMBEDDING_PROVIDER value that selects this client.
const Name = "bge-m3"

// errBodyLimit caps what is read off a failed response. The message is a sentence and the
// validation list a few lines; anything past that is a proxy's HTML error page, which is
// worth naming but not worth logging whole.
const errBodyLimit = 8 << 10

type Config struct {
	// BaseURL is where the model service answers, e.g. http://embedding:5007.
	BaseURL string
	// APIKey is the service's bearer token. Empty is allowed because the service can be run
	// open (ALLOW_NO_AUTH) on a private network; whether *this* deployment must have one is
	// config's call, not the client's.
	APIKey string
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

// Both halves are asked for explicitly rather than left to the service's defaults: which
// vectors come back decides whether a row can be written at all, so it is not a setting that
// should be changeable from the other repository. ColBERT is never asked for — it is a matrix
// per token, and nothing here stores one.
type embedRequest struct {
	Texts        []string `json:"texts"`
	ReturnDense  bool     `json:"return_dense"`
	ReturnSparse bool     `json:"return_sparse"`
}

// Sparse weights are keyed by the vocabulary index written out as a string, which is what a
// JSON object key has to be.
type embedResponse struct {
	Model  string               `json:"model"`
	Dense  [][]float32          `json:"dense"`
	Sparse []map[string]float32 `json:"sparse"`
}

func (c *Client) Embed(ctx context.Context, texts []string) ([]embedding.Vector, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()

	body, err := json.Marshal(embedRequest{Texts: texts, ReturnDense: true, ReturnSparse: true})
	if err != nil {
		return nil, fmt.Errorf("encode embed request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimSuffix(c.cfg.BaseURL, "/")+"/embed", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build embed request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}

	res, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call embedding service: %w", err)
	}
	defer func() { _ = res.Body.Close() }()

	// Status first: a refusal carries a different envelope, so decoding it as an answer would
	// report a missing field instead of the reason.
	if res.StatusCode != http.StatusOK {
		return nil, serviceError(res)
	}

	var decoded embedResponse
	if err := json.NewDecoder(res.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode embed response: %w", err)
	}
	// Positional pairing is the caller's contract, so a short answer has to fail rather than
	// leave one row wearing another's vector. Both halves are checked: a service asked for
	// sparse and answering without it would otherwise write a dense-only row that the lexical
	// side of search can never match.
	if len(decoded.Dense) != len(texts) || len(decoded.Sparse) != len(texts) {
		return nil, fmt.Errorf("embedding service returned %d dense and %d sparse vectors for %d texts",
			len(decoded.Dense), len(decoded.Sparse), len(texts))
	}

	out := make([]embedding.Vector, len(texts))
	for i := range texts {
		dense := decoded.Dense[i]
		if len(dense) != c.cfg.Dimensions {
			return nil, fmt.Errorf("%w: model gave %d, this deployment stores %d",
				embedding.ErrUnexpectedDimensions, len(dense), c.cfg.Dimensions)
		}
		sparse := make(map[uint32]float32, len(decoded.Sparse[i]))
		for key, weight := range decoded.Sparse[i] {
			idx, err := strconv.ParseUint(key, 10, 32)
			if err != nil {
				return nil, fmt.Errorf("parse sparse index %q: %w", key, err)
			}
			sparse[uint32(idx)] = weight
		}
		out[i] = embedding.Vector{Dense: dense, Sparse: sparse}
	}
	return out, nil
}

// serviceError renders FastAPI's `{"detail": ...}`, which is a string for a refused request
// and a list of field problems for a body that did not parse. Either is printed as it came:
// the whole value is what says which of the two happened.
func serviceError(res *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(res.Body, errBodyLimit))
	msg := strings.TrimSpace(string(body))
	var env struct {
		Detail json.RawMessage `json:"detail"`
	}
	if err := json.Unmarshal(body, &env); err == nil && len(env.Detail) > 0 {
		var text string
		if err := json.Unmarshal(env.Detail, &text); err == nil {
			msg = text
		} else {
			msg = string(env.Detail)
		}
	}
	if msg == "" {
		msg = res.Status
	}
	return fmt.Errorf("embedding service answered %d: %s", res.StatusCode, msg)
}
