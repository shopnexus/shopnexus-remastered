package llm

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
)

// Compile-time interface check.
var _ Client = (*PythonClient)(nil)

// PythonConfig holds configuration for the Python embedding service HTTP client.
type PythonConfig struct {
	URL     string        `yaml:"url"`
	Timeout time.Duration `yaml:"timeout"`
}

// PythonClient is an HTTP client for the Python embedding service.
// It implements only the Embed method of the Client interface.
type PythonClient struct {
	baseURL    string
	httpClient *http.Client
}

// NewPythonClient creates a new PythonClient.
func NewPythonClient(cfg PythonConfig) *PythonClient {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	return &PythonClient{
		baseURL: strings.TrimRight(cfg.URL, "/"),
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

// embedRequest is the JSON request body sent to the /embed endpoint.
type embedRequest struct {
	Texts []string `json:"texts"`
}

// embedResponseEntry is a single embedding entry from the service response.
// The sparse field uses string keys because JSON object keys are always strings.
type embedResponseEntry struct {
	Dense  []float32          `json:"dense"`
	Sparse map[string]float32 `json:"sparse"`
}

// embedResponse is the JSON response body from the /embed endpoint.
type embedResponse struct {
	Embeddings []embedResponseEntry `json:"embeddings"`
}

// Embed sends texts to the Python embedding service and returns dense+sparse vectors.
func (c *PythonClient) Embed(ctx context.Context, texts []string) ([]EmbedResult, error) {
	body, err := json.Marshal(embedRequest{Texts: texts})
	if err != nil {
		return nil, fmt.Errorf("llm/python: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/embed", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("llm/python: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("llm/python: request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("llm/python: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("llm/python: service returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var result embedResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("llm/python: unmarshal response: %w", err)
	}

	out := make([]EmbedResult, len(result.Embeddings))
	for i, entry := range result.Embeddings {
		sparse := make(map[uint32]float32, len(entry.Sparse))
		for k, v := range entry.Sparse {
			idx, err := strconv.ParseUint(k, 10, 32)
			if err != nil {
				return nil, fmt.Errorf("llm/python: invalid sparse index %q in result %d: %w", k, i, err)
			}
			sparse[uint32(idx)] = v
		}
		out[i] = EmbedResult{
			Dense:  entry.Dense,
			Sparse: sparse,
		}
	}

	return out, nil
}

// GenerateText is not supported by the Python embedding service.
func (c *PythonClient) GenerateText(ctx context.Context, params GenerateTextParams) (string, error) {
	return "", ErrNotSupported
}

// Chat is not supported by the Python embedding service.
func (c *PythonClient) Chat(ctx context.Context, params ChatParams) (ChatMessage, error) {
	return ChatMessage{}, ErrNotSupported
}

// GenerateStructured is not supported by the Python embedding service.
func (c *PythonClient) GenerateStructured(
	ctx context.Context,
	params GenerateStructuredParams,
) (json.RawMessage, error) {
	return nil, ErrNotSupported
}
