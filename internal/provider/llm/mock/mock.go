// Package mock is the dev-only LLM: it answers without a model, an API key or a network call, so a
// local stack can walk the whole "photo in, listing out" flow.
//
// What it answers is deliberately plausible rather than clever. A suggestion route's job is to fill
// a form the seller then corrects, and a stub that returns a filled form exercises every step after
// the model — the JSON parse, the category check, the price bounds, the DTO — which is where the
// bugs a test can catch actually live.
package mock

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"iter"
	"strings"

	"shopnexus/internal/provider/llm"
)

// Name is the LLM_PROVIDER value that selects this stub.
const Name = "mock"

var _ llm.Client = (*Client)(nil)

type Client struct{}

func NewClient() *Client { return &Client{} }

// Complete answers the shape the request asked for: a JSON object when a response format was set —
// the suggestion route's case — and a sentence otherwise. It echoes what it was given, so a caller
// can see its own prompt came through: the photo count and the first line of the note.
func (c *Client) Complete(_ context.Context, params llm.CompleteParams) (llm.CompleteResult, error) {
	note, images := lastUserTurn(params.Messages)
	if params.ResponseFormat == nil {
		return result(fmt.Sprintf("mock llm: %d image(s), note %q", images, note)), nil
	}
	// The field names are the suggestion schema's. A model that answered a different shape is the
	// case the caller has to survive anyway, and its own tests cover that with their own stub.
	payload := map[string]any{
		"name":            firstLine(note, "Sản phẩm đã qua sử dụng"),
		"description":     "Mô tả tự động từ ảnh và ghi chú của người bán. Hãy kiểm tra lại trước khi đăng.",
		"category":        "",
		"condition":       "used",
		"tags":            []string{},
		"specifications":  map[string]any{},
		"package_details": map[string]any{"weight_g": 500},
		"price":           0,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return llm.CompleteResult{}, fmt.Errorf("encode mock suggestion: %w", err)
	}
	return result(string(body)), nil
}

// Transcribe answers a fixed Vietnamese sentence: the point of the stub is that the words reach the
// completion and the seller's screen, not that they match the audio nobody sent.
func (c *Client) Transcribe(_ context.Context, params llm.TranscribeParams) (llm.TranscribeResult, error) {
	if len(params.Audio) == 0 {
		return llm.TranscribeResult{}, fmt.Errorf("transcribe: no audio")
	}
	return llm.TranscribeResult{Text: "mock: bán một sản phẩm đã qua sử dụng, còn dùng tốt"}, nil
}

func (c *Client) Stream(_ context.Context, params llm.CompleteParams) iter.Seq2[llm.Chunk, error] {
	note, images := lastUserTurn(params.Messages)
	text := fmt.Sprintf("mock llm: %d image(s), note %q", images, note)
	return func(yield func(llm.Chunk, error) bool) {
		if !yield(llm.Chunk{ContentDelta: text}, nil) {
			return
		}
		yield(llm.Chunk{FinishReason: llm.FinishReasonStop}, nil)
	}
}

// Embed and Rerank are not this stub's business: catalog's vectors come from EMBEDDING_PROVIDER,
// which has its own seam and its own mock.
func (c *Client) Embed(context.Context, llm.EmbedParams) (llm.EmbedResult, error) {
	return llm.EmbedResult{}, llm.ErrNotSupported
}

func (c *Client) Rerank(context.Context, llm.RerankParams) (llm.RerankResult, error) {
	return llm.RerankResult{}, llm.ErrNotSupported
}

func result(content string) llm.CompleteResult {
	return llm.CompleteResult{
		Model:        Name,
		Message:      llm.Message{Role: llm.RoleAssistant, Content: content},
		FinishReason: llm.FinishReasonStop,
	}
}

func lastUserTurn(msgs []llm.Message) (note string, images int) {
	for _, m := range msgs {
		if m.Role == llm.RoleUser {
			note, images = m.Content, len(m.Images)
		}
	}
	return note, images
}

func firstLine(s, fallback string) string {
	line := strings.TrimSpace(strings.SplitN(s, "\n", 2)[0])
	if line == "" {
		return fallback
	}
	if len(line) > 200 {
		line = line[:200]
	}
	return line
}
