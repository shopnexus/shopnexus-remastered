package restateclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	restate "github.com/restatedev/sdk-go"
)

// Client is a simple HTTP client for calling Restate services via the ingress endpoint.
type Client struct {
	BaseURL    string
	HTTPClient *http.Client
}

// newRestateTransport tunes the connection pool for the Restate ingress
// (default MaxIdleConnsPerHost of 2 serializes concurrent ingress calls).
func newRestateTransport() *http.Transport {
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   5 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          200,
		MaxIdleConnsPerHost:   100,
		MaxConnsPerHost:       200,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}

func NewClient(baseURL string) *Client {
	return &Client{
		BaseURL: baseURL,
		HTTPClient: &http.Client{
			Timeout:   30 * time.Second,
			Transport: newRestateTransport(),
		},
	}
}

// Call invokes a Restate service method.
//
//   - restate.Context caller → journaled into the parent handler's invocation.
//   - plain context.Context → HTTP POST to the Restate ingress.
func Call[O any](ctx context.Context, c *Client, service, method string, input any) (O, error) {
	if rctx, ok := ctx.(restate.Context); ok {
		return restate.Service[O](rctx, service, method).Request(input)
	}

	var zero O
	body, err := json.Marshal(input)
	if err != nil {
		return zero, fmt.Errorf("restate: marshal input: %w", err)
	}

	resp, err := c.post(ctx, service, method, body)
	if err != nil {
		return zero, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return zero, fmt.Errorf("restate: %s/%s returned %d: %s", service, method, resp.StatusCode, respBody)
	}

	var out O
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return zero, fmt.Errorf("restate: decode response from %s/%s: %w", service, method, err)
	}
	return out, nil
}

// Send invokes a Restate service method as fire-and-forget.
//
//   - restate.Context caller → journaled ServiceSend.
//   - plain context.Context → HTTP POST to the Restate ingress.
func Send(ctx context.Context, c *Client, service, method string, input any) error {
	if rctx, ok := ctx.(restate.Context); ok {
		restate.ServiceSend(rctx, service, method).Send(input)
		return nil
	}

	body, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("restate: marshal input: %w", err)
	}

	resp, err := c.post(ctx, service, method, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("restate: %s/%s returned %d: %s", service, method, resp.StatusCode, respBody)
	}
	return nil
}

func (c *Client) post(ctx context.Context, service, method string, body []byte) (*http.Response, error) {
	url := fmt.Sprintf("%s/%s/%s", c.BaseURL, service, method)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("restate: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("restate: call %s/%s: %w", service, method, err)
	}
	return resp, nil
}
