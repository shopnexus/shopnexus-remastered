package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

// Compile-time interface check.
var _ Client = (*OpenRouterClient)(nil)

// OpenRouterConfig holds configuration for the OpenRouter client.
type OpenRouterConfig struct {
	APIKey     string `yaml:"apiKey"`
	BaseURL    string `yaml:"baseURL"`    // optional, defaults to https://openrouter.ai/api/v1
	EmbedModel string `yaml:"embedModel"` // e.g. "baai/bge-m3"
	ChatModel  string `yaml:"chatModel"`  // e.g. "openai/gpt-4o"
}

// OpenRouterClient implements the Client interface via OpenRouter's OpenAI-compatible API.
type OpenRouterClient struct {
	client     *openai.Client
	embedModel string
	chatModel  string
}

// NewOpenRouterClient creates a new OpenRouterClient with the given configuration.
func NewOpenRouterClient(cfg OpenRouterConfig) *OpenRouterClient {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://openrouter.ai/api/v1"
	}

	opts := []option.RequestOption{
		option.WithAPIKey(cfg.APIKey),
		option.WithBaseURL(baseURL),
	}
	client := openai.NewClient(opts...)
	return &OpenRouterClient{
		client:     &client,
		embedModel: cfg.EmbedModel,
		chatModel:  cfg.ChatModel,
	}
}

// Embed sends texts to the OpenRouter embeddings API and returns dense vectors.
// OpenRouter only returns dense embeddings; Sparse will be nil.
func (c *OpenRouterClient) Embed(ctx context.Context, texts []string) ([]EmbedResult, error) {
	resp, err := c.client.Embeddings.New(ctx, openai.EmbeddingNewParams{
		Model: c.embedModel,
		Input: openai.EmbeddingNewParamsInputUnion{
			OfArrayOfStrings: texts,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("llm/openrouter: embed: %w", err)
	}

	out := make([]EmbedResult, len(resp.Data))
	for i, d := range resp.Data {
		f32 := make([]float32, len(d.Embedding))
		for j, v := range d.Embedding {
			f32[j] = float32(v)
		}
		out[i] = EmbedResult{
			Dense:  f32,
			Sparse: nil,
		}
	}
	return out, nil
}

// GenerateText calls the chat completions API with a single user message.
func (c *OpenRouterClient) GenerateText(ctx context.Context, params GenerateTextParams) (string, error) {
	p := openai.ChatCompletionNewParams{
		Model: c.chatModel,
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.UserMessage(params.Prompt),
		},
	}
	if params.MaxTokens > 0 {
		p.MaxTokens = openai.Int(int64(params.MaxTokens))
	}
	if params.Temperature > 0 {
		p.Temperature = openai.Float(params.Temperature)
	}

	resp, err := c.client.Chat.Completions.New(ctx, p)
	if err != nil {
		return "", fmt.Errorf("llm/openrouter: generate text: %w", err)
	}
	if len(resp.Choices) == 0 {
		return "", errors.New("llm/openrouter: generate text: no choices returned")
	}
	return resp.Choices[0].Message.Content, nil
}

// Chat calls the chat completions API mapping our ChatMessage roles to SDK types.
func (c *OpenRouterClient) Chat(ctx context.Context, params ChatParams) (ChatMessage, error) {
	msgs := make([]openai.ChatCompletionMessageParamUnion, len(params.Messages))
	for i, m := range params.Messages {
		switch m.Role {
		case RoleSystem:
			msgs[i] = openai.SystemMessage(m.Content)
		case RoleUser:
			msgs[i] = openai.UserMessage(m.Content)
		case RoleAssistant:
			msgs[i] = openai.AssistantMessage(m.Content)
		default:
			msgs[i] = openai.UserMessage(m.Content)
		}
	}

	p := openai.ChatCompletionNewParams{
		Model:    c.chatModel,
		Messages: msgs,
	}
	if params.MaxTokens > 0 {
		p.MaxTokens = openai.Int(int64(params.MaxTokens))
	}
	if params.Temperature > 0 {
		p.Temperature = openai.Float(params.Temperature)
	}

	resp, err := c.client.Chat.Completions.New(ctx, p)
	if err != nil {
		return ChatMessage{}, fmt.Errorf("llm/openrouter: chat: %w", err)
	}
	if len(resp.Choices) == 0 {
		return ChatMessage{}, errors.New("llm/openrouter: chat: no choices returned")
	}
	msg := resp.Choices[0].Message
	return ChatMessage{
		Role:    RoleAssistant,
		Content: msg.Content,
	}, nil
}

// GenerateStructured calls the chat completions API expecting a JSON response.
// Uses prompt-based JSON extraction since OpenRouter does not guarantee
// structured output support across all models.
func (c *OpenRouterClient) GenerateStructured(
	ctx context.Context,
	params GenerateStructuredParams,
) (json.RawMessage, error) {
	p := openai.ChatCompletionNewParams{
		Model: c.chatModel,
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.UserMessage(params.Prompt),
		},
	}
	if params.MaxTokens > 0 {
		p.MaxTokens = openai.Int(int64(params.MaxTokens))
	}
	if params.Temperature > 0 {
		p.Temperature = openai.Float(params.Temperature)
	}

	resp, err := c.client.Chat.Completions.New(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("llm/openrouter: generate structured: %w", err)
	}
	if len(resp.Choices) == 0 {
		return nil, errors.New("llm/openrouter: generate structured: no choices returned")
	}
	return json.RawMessage(resp.Choices[0].Message.Content), nil
}
