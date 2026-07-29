// Package litellm implements the llm.Client interface against a LiteLLM proxy.
// The proxy speaks the OpenAI API for chat and embeddings and the Cohere API for
// rerank, so one client covers every model the proxy routes to: the concrete
// model is just a string ("gpt-5", "gemini/gemini-2.5-pro", "openai/mgte").
package litellm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"shopnexus/internal/provider/llm"
)

const (
	chatPath   = "/v1/chat/completions"
	embedPath  = "/v1/embeddings"
	rerankPath = "/v1/rerank"
)

// Config configures the proxy connection and the default model per capability.
// A default may be left empty as long as every request names its own model.
type Config struct {
	// BaseURL is the proxy root without the /v1 suffix, e.g. "http://litellm:4000".
	BaseURL string
	// APIKey is a LiteLLM virtual key, sent as a bearer token.
	APIKey string

	ChatModel   string // default for Complete and Stream
	EmbedModel  string // default for Embed
	RerankModel string // default for Rerank

	// RequestTimeout bounds one non-streaming call (Complete, Embed, Rerank).
	// Required: a call with no deadline pins a goroutine and a connection for as
	// long as the upstream keeps the socket open, which is how a slow model
	// takes down unrelated traffic.
	RequestTimeout time.Duration
	// StreamTimeout bounds a whole Stream call — request through last chunk — so
	// it must cover the longest generation expected, not just connect time.
	// Required for the same reason as RequestTimeout.
	StreamTimeout time.Duration

	// HTTPClient is optional. It must not carry a Timeout: that budget covers
	// reading the body too, so it truncates long streams. The timeouts above
	// are applied to the request context instead.
	HTTPClient *http.Client
}

var _ llm.Client = (*Client)(nil)

type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client

	chatModel   string
	embedModel  string
	rerankModel string

	requestTimeout time.Duration
	streamTimeout  time.Duration
}

func NewClient(cfg Config) (*Client, error) {
	if cfg.BaseURL == "" {
		return nil, errors.New("litellm config: base url is required")
	}
	if cfg.APIKey == "" {
		return nil, errors.New("litellm config: api key is required")
	}
	if cfg.RequestTimeout <= 0 {
		return nil, errors.New("litellm config: request timeout is required")
	}
	if cfg.StreamTimeout <= 0 {
		return nil, errors.New("litellm config: stream timeout is required")
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	return &Client{
		baseURL:        strings.TrimRight(cfg.BaseURL, "/"),
		apiKey:         cfg.APIKey,
		http:           httpClient,
		chatModel:      cfg.ChatModel,
		embedModel:     cfg.EmbedModel,
		rerankModel:    cfg.RerankModel,
		requestTimeout: cfg.RequestTimeout,
		streamTimeout:  cfg.StreamTimeout,
	}, nil
}

// Every call gets its own deadline below. context.WithTimeout keeps the
// caller's deadline when that one is earlier, so a request-scoped budget
// still wins over the configured one.

func (c *Client) Complete(ctx context.Context, params llm.CompleteParams) (llm.CompleteResult, error) {
	ctx, cancel := context.WithTimeout(ctx, c.requestTimeout)
	defer cancel()

	var out chatResponse
	if err := c.postJSON(ctx, chatPath, c.buildChatRequest(params, false), &out); err != nil {
		return llm.CompleteResult{}, fmt.Errorf("chat completion: %w", err)
	}
	if len(out.Choices) == 0 {
		return llm.CompleteResult{}, errors.New("chat completion: response has no choices")
	}
	choice := out.Choices[0]
	return llm.CompleteResult{
		Model:        out.Model,
		Message:      choice.Message.toMessage(),
		FinishReason: toFinishReason(choice.FinishReason),
		Usage:        out.Usage.toUsage(),
	}, nil
}

func (c *Client) Embed(ctx context.Context, params llm.EmbedParams) (llm.EmbedResult, error) {
	ctx, cancel := context.WithTimeout(ctx, c.requestTimeout)
	defer cancel()

	req := embedRequest{
		Model:          c.model(params.Model, c.embedModel),
		Input:          params.Input,
		EncodingFormat: "float",
		Dimensions:     params.Dimensions,
	}
	var out embedResponse
	if err := c.postJSON(ctx, embedPath, req, &out); err != nil {
		return llm.EmbedResult{}, fmt.Errorf("embed: %w", err)
	}
	if len(out.Data) != len(params.Input) {
		return llm.EmbedResult{}, fmt.Errorf("embed: got %d vectors for %d inputs", len(out.Data), len(params.Input))
	}
	// Index the vectors back onto the input order; the API may return them unsorted.
	vectors := make([][]float32, len(out.Data))
	for _, d := range out.Data {
		if d.Index < 0 || d.Index >= len(vectors) {
			return llm.EmbedResult{}, fmt.Errorf("embed: vector index %d out of range", d.Index)
		}
		vectors[d.Index] = d.Embedding
	}
	return llm.EmbedResult{Model: out.Model, Vectors: vectors, Usage: out.Usage.toUsage()}, nil
}

func (c *Client) Rerank(ctx context.Context, params llm.RerankParams) (llm.RerankResult, error) {
	ctx, cancel := context.WithTimeout(ctx, c.requestTimeout)
	defer cancel()

	req := rerankRequest{
		Model:     c.model(params.Model, c.rerankModel),
		Query:     params.Query,
		Documents: params.Documents,
		TopN:      params.TopN,
	}
	var out rerankResponse
	if err := c.postJSON(ctx, rerankPath, req, &out); err != nil {
		return llm.RerankResult{}, fmt.Errorf("rerank: %w", err)
	}
	hits := make([]llm.RerankHit, 0, len(out.Results))
	for _, r := range out.Results {
		hits = append(hits, llm.RerankHit{Index: r.Index, Score: r.RelevanceScore})
	}
	usage := llm.Usage{
		PromptTokens:     out.Meta.Tokens.InputTokens,
		CompletionTokens: out.Meta.Tokens.OutputTokens,
		TotalTokens:      out.Meta.Tokens.InputTokens + out.Meta.Tokens.OutputTokens,
	}
	return llm.RerankResult{Model: req.Model, Hits: hits, Usage: usage}, nil
}

// model picks the per-request model, falling back to the configured default.
func (c *Client) model(requested, fallback string) string {
	if requested != "" {
		return requested
	}
	return fallback
}

// postJSON sends payload and decodes a JSON response into out.
func (c *Client) postJSON(ctx context.Context, path string, payload, out any) error {
	resp, err := c.post(ctx, path, payload, false)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// post sends payload to path and returns a response whose body the caller must
// close. A non-2xx status is converted to *llm.APIError and the body closed here.
func (c *Client) post(ctx context.Context, path string, payload any, stream bool) (*http.Response, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	} else {
		req.Header.Set("Accept", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call litellm %s: %w", path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		return nil, apiError(resp)
	}
	return resp, nil
}

// apiError turns a failed response into *llm.APIError, falling back to the raw
// body when the proxy did not send its usual error envelope.
func apiError(resp *http.Response) error {
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read error response (status %d): %w", resp.StatusCode, err)
	}
	apiErr := &llm.APIError{StatusCode: resp.StatusCode}

	var env errorResponse
	if json.Unmarshal(raw, &env) == nil && env.Error.Message != "" {
		apiErr.Message = env.Error.Message
		apiErr.Type = env.Error.Type
		if env.Error.Code != nil {
			apiErr.Code = fmt.Sprint(env.Error.Code)
		}
		return apiErr
	}
	apiErr.Message = strings.TrimSpace(string(raw))
	if apiErr.Message == "" {
		apiErr.Message = http.StatusText(resp.StatusCode)
	}
	return apiErr
}
