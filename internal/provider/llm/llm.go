// Package llm defines the LLM provider interface and its shared types.
// It is provider-agnostic: implementations live in subpackages (litellm, ...)
// and callers depend only on Client.
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
)

// ErrNotSupported is returned by a provider that cannot serve an operation
// (e.g. a chat-only endpoint asked to rerank).
var ErrNotSupported = errors.New("operation not supported by this llm provider")

// APIError is a non-2xx response from the provider. Callers can inspect it with
// errors.As to branch on the upstream status (429, 400, ...).
type APIError struct {
	StatusCode int    `json:"status_code"`
	Type       string `json:"type,omitempty"`
	Code       string `json:"code,omitempty"`
	Message    string `json:"message"`
}

func (e *APIError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("llm api error %d (%s): %s", e.StatusCode, e.Code, e.Message)
	}
	return fmt.Sprintf("llm api error %d: %s", e.StatusCode, e.Message)
}

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// FinishReason is why generation stopped.
type FinishReason string

const (
	FinishReasonStop          FinishReason = "stop"
	FinishReasonLength        FinishReason = "length"
	FinishReasonToolCalls     FinishReason = "tool-calls"
	FinishReasonContentFilter FinishReason = "content-filter"
)

// Message is one turn of a conversation. Content carries the text; ToolCalls is
// set on assistant turns that call tools, and ToolCallID on RoleTool turns that
// answer one (both are ignored for the other roles).
type Message struct {
	Role    Role   `json:"role"`
	Content string `json:"content"`
	// Images are what the model looks at, on a user turn. Bytes rather than a URL: an object this
	// platform holds is behind a signed link only its own gateway can serve, which a hosted model
	// cannot follow — so the picture travels with the request.
	Images     []Image    `json:"images,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// Image is one picture in a message, as the bytes and the type they are.
type Image struct {
	Mime string `json:"mime"`
	Data []byte `json:"data"`
}

// TranscribeParams is a voice note to turn into text. Language is a hint, not a filter: an ISO-639-1
// code where the caller knows it ("vi"), empty to let the model detect.
type TranscribeParams struct {
	Model    string
	Audio    []byte
	Mime     string
	Language string
	// Prompt is optional context that biases the decoding — a domain's vocabulary, so a
	// marketplace's brand names come back spelled the way its catalogue spells them.
	Prompt string
}

type TranscribeResult struct {
	Text string `json:"text"`
}

// ToolCall is a model request to invoke a tool with JSON arguments.
type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// Tool is a function the model may call. Parameters is a JSON Schema object.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

// ToolChoice constrains tool use for a request. Empty means the provider default.
type ToolChoice string

const (
	ToolChoiceAuto     ToolChoice = "auto"     // model decides
	ToolChoiceNone     ToolChoice = "none"     // never call a tool
	ToolChoiceRequired ToolChoice = "required" // must call at least one tool
)

// ResponseFormat requests structured output. With Schema set the model must
// emit JSON matching it; with Schema nil it must emit some JSON object.
type ResponseFormat struct {
	Name   string          `json:"name,omitempty"`
	Schema json.RawMessage `json:"schema,omitempty"`
	Strict bool            `json:"strict,omitempty"`
}

// CompleteParams is a chat completion request. Model overrides the provider's
// configured default; zero-valued knobs are omitted so the provider decides.
type CompleteParams struct {
	Model          string
	Messages       []Message
	Tools          []Tool
	ToolChoice     ToolChoice
	ResponseFormat *ResponseFormat
	MaxTokens      int
	Temperature    *float64
	TopP           *float64
	Stop           []string
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type CompleteResult struct {
	Model        string       `json:"model"`
	Message      Message      `json:"message"`
	FinishReason FinishReason `json:"finish_reason"`
	Usage        Usage        `json:"usage"`
}

// Chunk is one incremental piece of a streamed completion: a text delta,
// partial tool calls, a terminal FinishReason, or a final Usage report.
type Chunk struct {
	ContentDelta string          `json:"content_delta,omitempty"`
	ToolCalls    []ToolCallDelta `json:"tool_calls,omitempty"`
	FinishReason FinishReason    `json:"finish_reason,omitempty"`
	Usage        *Usage          `json:"usage,omitempty"`
}

// ToolCallDelta is a fragment of the tool call at Index: ID and Name arrive
// once, ArgumentsDelta accumulates across chunks. Feed these to a
// ToolCallAccumulator to rebuild whole ToolCall values.
type ToolCallDelta struct {
	Index          int    `json:"index"`
	ID             string `json:"id,omitempty"`
	Name           string `json:"name,omitempty"`
	ArgumentsDelta string `json:"arguments_delta,omitempty"`
}

type EmbedParams struct {
	Model string
	Input []string
	// Dimensions truncates the output vector (models that support it); 0 keeps
	// the model's native size.
	Dimensions int
}

// EmbedResult holds one vector per EmbedParams.Input entry, in the same order.
type EmbedResult struct {
	Model   string      `json:"model"`
	Vectors [][]float32 `json:"vectors"`
	Usage   Usage       `json:"usage"`
}

type RerankParams struct {
	Model     string
	Query     string
	Documents []string
	// TopN caps how many hits come back; 0 means all documents.
	TopN int
}

// RerankHit points at RerankParams.Documents[Index] with its relevance Score.
type RerankHit struct {
	Index int     `json:"index"`
	Score float64 `json:"score"`
}

// RerankResult holds hits ordered most relevant first.
type RerankResult struct {
	Model string      `json:"model"`
	Hits  []RerankHit `json:"hits"`
	Usage Usage       `json:"usage"`
}

// Client is the LLM capability set. Providers that lack one return
// ErrNotSupported rather than faking it.
type Client interface {
	// Complete runs a chat completion to the end and returns the whole message.
	Complete(ctx context.Context, params CompleteParams) (CompleteResult, error)

	// Stream is Complete delivered incrementally. Errors — including request
	// failures — surface as a yielded (Chunk{}, err) pair, always the last one.
	// The underlying connection is released when iteration ends, so breaking
	// out of the loop early is safe.
	Stream(ctx context.Context, params CompleteParams) iter.Seq2[Chunk, error]

	// Transcribe turns a voice note into text. Its own method rather than a Message part: audio
	// goes to a different endpoint with a different model, and a caller that wants the words —
	// to show the seller what was heard — needs them before the completion runs.
	Transcribe(ctx context.Context, params TranscribeParams) (TranscribeResult, error)

	// Embed turns text into dense vectors.
	Embed(ctx context.Context, params EmbedParams) (EmbedResult, error)

	// Rerank scores documents against a query.
	Rerank(ctx context.Context, params RerankParams) (RerankResult, error)
}

// ToolCallAccumulator rebuilds complete ToolCall values from streamed
// ToolCallDelta fragments. The zero value is ready to use.
type ToolCallAccumulator struct {
	calls []ToolCall
	args  [][]byte
}

// Add folds deltas into the accumulator, growing it to fit the highest Index.
func (a *ToolCallAccumulator) Add(deltas ...ToolCallDelta) {
	for _, d := range deltas {
		if d.Index < 0 {
			continue
		}
		for d.Index >= len(a.calls) {
			a.calls = append(a.calls, ToolCall{})
			a.args = append(a.args, nil)
		}
		if d.ID != "" {
			a.calls[d.Index].ID = d.ID
		}
		if d.Name != "" {
			a.calls[d.Index].Name = d.Name
		}
		a.args[d.Index] = append(a.args[d.Index], d.ArgumentsDelta...)
	}
}

// Calls returns the assembled tool calls, or nil if none were accumulated.
func (a *ToolCallAccumulator) Calls() []ToolCall {
	if len(a.calls) == 0 {
		return nil
	}
	out := make([]ToolCall, len(a.calls))
	for i, c := range a.calls {
		c.Arguments = json.RawMessage(a.args[i])
		out[i] = c
	}
	return out
}
