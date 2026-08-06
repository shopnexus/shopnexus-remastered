package litellm

import (
	"encoding/base64"
	"encoding/json/jsontext"
	"strings"

	"shopnexus/internal/provider/llm"
)

// Wire types for the OpenAI-compatible payloads the LiteLLM proxy speaks, plus
// the mapping to and from the provider-agnostic llm types.

type chatRequest struct {
	Model          string          `json:"model"`
	Messages       []chatMessage   `json:"messages"`
	Tools          []chatTool      `json:"tools,omitempty"`
	ToolChoice     string          `json:"tool_choice,omitempty"`
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
	MaxTokens      int             `json:"max_tokens,omitzero"`
	Temperature    *float64        `json:"temperature,omitempty"`
	TopP           *float64        `json:"top_p,omitempty"`
	Stop           []string        `json:"stop,omitempty"`
	Stream         bool            `json:"stream,omitzero"`
	StreamOptions  *streamOptions  `json:"stream_options,omitempty"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// chatMessage's Content is `any` because the OpenAI schema has two shapes for it: a plain string,
// and an array of parts once a turn carries pictures. Decoding only ever sees the string form — a
// model answers text — so toMessage asserts it rather than handling both.
type chatMessage struct {
	Role       string         `json:"role"`
	Content    any            `json:"content"`
	ToolCalls  []wireToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

// contentPart is one element of the array form: text, or an image as a data URI. A data URI rather
// than a link, because the objects this platform holds are behind signed URLs only its own gateway
// serves and a hosted model cannot follow one.
type contentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *imageURL `json:"image_url,omitempty"`
}

type imageURL struct {
	URL string `json:"url"`
}

type wireToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function wireFunction `json:"function"`
}

// wireFunction holds the call arguments as a JSON *string*, per the OpenAI schema.
type wireFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type chatTool struct {
	Type     string       `json:"type"`
	Function toolFunction `json:"function"`
}

type toolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  jsontext.Value `json:"parameters,omitempty"`
}

type responseFormat struct {
	Type       string      `json:"type"`
	JSONSchema *jsonSchema `json:"json_schema,omitempty"`
}

type jsonSchema struct {
	Name   string         `json:"name"`
	Schema jsontext.Value `json:"schema"`
	Strict bool           `json:"strict,omitzero"`
}

type chatResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message      chatMessage `json:"message"`
		FinishReason string      `json:"finish_reason"`
	} `json:"choices"`
	Usage *wireUsage `json:"usage"`
}

type streamResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Delta struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int          `json:"index"`
				ID       string       `json:"id"`
				Function wireFunction `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *wireUsage `json:"usage"`
}

type wireUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

func (u *wireUsage) toUsage() llm.Usage {
	if u == nil {
		return llm.Usage{}
	}
	return llm.Usage{PromptTokens: u.PromptTokens, CompletionTokens: u.CompletionTokens, TotalTokens: u.TotalTokens}
}

type embedRequest struct {
	Model          string   `json:"model"`
	Input          []string `json:"input"`
	EncodingFormat string   `json:"encoding_format"`
	Dimensions     int      `json:"dimensions,omitzero"`
}

type embedResponse struct {
	Model string `json:"model"`
	Data  []struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Usage *wireUsage `json:"usage"`
}

type rerankRequest struct {
	Model     string   `json:"model"`
	Query     string   `json:"query"`
	Documents []string `json:"documents"`
	TopN      int      `json:"top_n,omitzero"`
}

// rerankResponse is Cohere-shaped, which is what the proxy returns for /v1/rerank.
type rerankResponse struct {
	Results []struct {
		Index          int     `json:"index"`
		RelevanceScore float64 `json:"relevance_score"`
	} `json:"results"`
	Meta struct {
		Tokens struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"tokens"`
	} `json:"meta"`
}

// errorResponse is the proxy's error envelope. Code is either a string
// ("invalid_api_key") or a number, so it is decoded loosely.
type errorResponse struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    any    `json:"code"`
	} `json:"error"`
}

func (c *Client) buildChatRequest(params llm.CompleteParams, stream bool) chatRequest {
	req := chatRequest{
		Model:       c.model(params.Model, c.chatModel),
		Messages:    toWireMessages(params.Messages),
		ToolChoice:  string(params.ToolChoice),
		MaxTokens:   params.MaxTokens,
		Temperature: params.Temperature,
		TopP:        params.TopP,
		Stop:        params.Stop,
	}
	for _, t := range params.Tools {
		req.Tools = append(req.Tools, chatTool{
			Type:     "function",
			Function: toolFunction{Name: t.Name, Description: t.Description, Parameters: t.Parameters},
		})
	}
	if rf := params.ResponseFormat; rf != nil {
		if rf.Schema == nil {
			req.ResponseFormat = &responseFormat{Type: "json_object"}
		} else {
			req.ResponseFormat = &responseFormat{
				Type:       "json_schema",
				JSONSchema: &jsonSchema{Name: rf.Name, Schema: rf.Schema, Strict: rf.Strict},
			}
		}
	}
	if stream {
		req.Stream = true
		req.StreamOptions = &streamOptions{IncludeUsage: true}
	}
	return req
}

func toWireMessages(msgs []llm.Message) []chatMessage {
	out := make([]chatMessage, 0, len(msgs))
	for _, m := range msgs {
		wm := chatMessage{Role: string(m.Role), Content: m.Content, ToolCallID: m.ToolCallID}
		if len(m.Images) > 0 {
			parts := make([]contentPart, 0, len(m.Images)+1)
			if m.Content != "" {
				parts = append(parts, contentPart{Type: "text", Text: m.Content})
			}
			for _, img := range m.Images {
				parts = append(parts, contentPart{
					Type:     "image_url",
					ImageURL: &imageURL{URL: dataURI(img)},
				})
			}
			wm.Content = parts
		}
		for _, tc := range m.ToolCalls {
			wm.ToolCalls = append(wm.ToolCalls, wireToolCall{
				ID:       tc.ID,
				Type:     "function",
				Function: wireFunction{Name: tc.Name, Arguments: string(tc.Arguments)},
			})
		}
		out = append(out, wm)
	}
	return out
}

// dataURI is how a picture travels inside a JSON request.
func dataURI(img llm.Image) string {
	mime := img.Mime
	if mime == "" {
		mime = "image/jpeg"
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(img.Data)
}

func (m chatMessage) toMessage() llm.Message {
	text, _ := m.Content.(string)
	msg := llm.Message{Role: llm.Role(m.Role), Content: text, ToolCallID: m.ToolCallID}
	for _, tc := range m.ToolCalls {
		msg.ToolCalls = append(msg.ToolCalls, llm.ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: jsontext.Value(tc.Function.Arguments),
		})
	}
	return msg
}

// toFinishReason maps the wire value to our kebab-case enum, passing unknown
// reasons through in the same shape (e.g. "content_filter" -> "content-filter").
func toFinishReason(s string) llm.FinishReason {
	return llm.FinishReason(strings.ReplaceAll(s, "_", "-"))
}
