package besteffort

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// CallClient makes non-durable HTTP/2+JSON request-response calls, bypassing Restate.
type CallClient struct {
	baseURL string
	http    *http.Client
}

func NewCallClient(baseURL string) *CallClient {
	return &CallClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    newHTTPClient(),
	}
}

// Call posts input as JSON to /{service}/{method} and decodes the result.
// Non-2xx responses carry an Envelope and are returned as the original domain error.
func Call[O any](ctx context.Context, c *CallClient, service, method string, input any) (O, error) {
	var out O

	body, err := json.Marshal(input)
	if err != nil {
		return out, fmt.Errorf("besteffort: marshal input: %w", err)
	}

	url := c.baseURL + "/" + service + "/" + method
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return out, fmt.Errorf("besteffort: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return out, fmt.Errorf("besteffort: %s/%s: %w", service, method, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return out, fmt.Errorf("besteffort: read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var env Envelope
		if jerr := json.Unmarshal(raw, &env); jerr != nil || env.Code == "" {
			return out, fmt.Errorf("besteffort: %s/%s: status %d: %s", service, method, resp.StatusCode, raw)
		}
		return out, DecodeError(env)
	}

	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &out); err != nil {
			return out, fmt.Errorf("besteffort: decode response: %w", err)
		}
	}
	return out, nil
}

// CallVoid invokes a method and discards the result.
func CallVoid(ctx context.Context, c *CallClient, service, method string, input any) error {
	_, err := Call[struct{}](ctx, c, service, method, input)
	return err
}
