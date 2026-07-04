package catalogconfig

import (
	"time"

	"shopnexus-server/config"
)

type Config struct {
	config.Shared `mapstructure:",squash"`

	Search Search `mapstructure:"search"`
	LLM    LLM    `mapstructure:"llm"`
}

type Search struct {
	DenseWeight           float32       `yaml:"denseWeight"           mapstructure:"denseWeight"           validate:"required,gte=0,lte=1"`
	SparseWeight          float32       `yaml:"sparseWeight"          mapstructure:"sparseWeight"          validate:"required,gte=0,lte=1"`
	LexicalWeight         float32       `yaml:"lexicalWeight"         mapstructure:"lexicalWeight"         validate:"required,gte=0,lte=1"`
	InteractionBatchSize  int           `yaml:"interactionBatchSize"  mapstructure:"interactionBatchSize"  validate:"required,gte=1"`
	InteractionLinger     time.Duration `yaml:"interactionLinger"     mapstructure:"interactionLinger"     validate:"gte=0"`
	EmbeddingSyncInterval time.Duration `yaml:"embeddingSyncInterval" mapstructure:"embeddingSyncInterval" validate:"gte=0"`
}

// LLM config — only catalog (search embeddings) needs it.
type LLM struct {
	Provider      string         `yaml:"provider"      mapstructure:"provider" validate:"required,oneof=python openai bedrock openrouter"`
	EmbedCacheTTL time.Duration  `yaml:"embedCacheTTL" mapstructure:"embedCacheTTL" validate:"gte=0"` // 0 = cache forever
	Python        LLMPython      `yaml:"python"        mapstructure:"python"`
	OpenAI        LLMOpenAI      `yaml:"openai"        mapstructure:"openai"`
	Bedrock       LLMBedrock     `yaml:"bedrock"       mapstructure:"bedrock"`
	OpenRouter    LLMOpenRouter  `yaml:"openrouter"    mapstructure:"openrouter"`
}

type LLMOpenRouter struct {
	APIKey     string `yaml:"apiKey"     mapstructure:"apiKey"`
	BaseURL    string `yaml:"baseURL"    mapstructure:"baseURL"    validate:"omitempty,url"`
	EmbedModel string `yaml:"embedModel" mapstructure:"embedModel"`
	ChatModel  string `yaml:"chatModel"  mapstructure:"chatModel"`
}

type LLMPython struct {
	URL string `yaml:"url" mapstructure:"url" validate:"omitempty,url"`
}

type LLMOpenAI struct {
	APIKey     string `yaml:"apiKey"     mapstructure:"apiKey"`
	BaseURL    string `yaml:"baseURL"    mapstructure:"baseURL"    validate:"omitempty,url"`
	EmbedModel string `yaml:"embedModel" mapstructure:"embedModel"`
	ChatModel  string `yaml:"chatModel"  mapstructure:"chatModel"`
}

type LLMBedrock struct {
	Region       string `yaml:"region"       mapstructure:"region"`
	EmbedModelID string `yaml:"embedModelId" mapstructure:"embedModelId"`
	ChatModelID  string `yaml:"chatModelId"  mapstructure:"chatModelId"`
}

func NewConfig() (*Config, error) {
	var cfg Config
	return &cfg, config.LoadModule("catalog", &cfg)
}
