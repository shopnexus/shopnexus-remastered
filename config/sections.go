package config

import "time"

// This file holds the module-specific config sections. Infra leaf types
// (Postgres, Redis, Log, Restate, Bus, RankedSet, Public) live in struct.go.
// Everything is one package so there is a single Config (see config.go) and a
// single YAML pair — no per-module config packages/files.

// BestEffort configures the in-process BestEffort HTTP/2 endpoint (app-level).
type BestEffort struct {
	Port string `mapstructure:"port" validate:"required"`
}

// --- account ---

type JWT struct {
	Secret               string `yaml:"secret"               mapstructure:"secret"               validate:"required"`
	AccessTokenDuration  int64  `yaml:"accessTokenDuration"  mapstructure:"accessTokenDuration"  validate:"required,gte=1"`
	RefreshTokenDuration int64  `yaml:"refreshTokenDuration" mapstructure:"refreshTokenDuration" validate:"required,gte=1"`
	RefreshSecret        string `yaml:"refreshSecret"        mapstructure:"refreshSecret"`
}

// --- common (currency exchange + object store) ---

type Exchange struct {
	Base            string        `yaml:"base"            mapstructure:"base"            validate:"required"`
	Supported       []string      `yaml:"supported"       mapstructure:"supported"       validate:"required,min=1"`
	RefreshInterval time.Duration `yaml:"refreshInterval" mapstructure:"refreshInterval" validate:"gte=0"`
	HTTPTimeout     time.Duration `yaml:"httpTimeout"     mapstructure:"httpTimeout"     validate:"gte=0"`
	UpstreamURL     string        `yaml:"upstreamURL"     mapstructure:"upstreamURL"     validate:"required,url"`
	APIKey          string        `yaml:"apiKey"          mapstructure:"apiKey"          validate:"required"`
}

type Filestore struct {
	Type                string      `yaml:"type"                mapstructure:"type"                validate:"required,oneof=local s3"`
	PresignedDefaultTTL int64       `yaml:"presignedDefaultTTL" mapstructure:"presignedDefaultTTL" validate:"gte=1"`
	S3                  S3Filestore `yaml:"s3"                  mapstructure:"s3"`
	Placeholder404Url   string      `yaml:"placeholder404Url"   mapstructure:"placeholder404Url"   validate:"omitempty,url"`
}

type S3Filestore struct {
	AccessKeyID     string `yaml:"accessKeyID"     mapstructure:"accessKeyID"`
	SecretAccessKey string `yaml:"secretAccessKey" mapstructure:"secretAccessKey"`
	Region          string `yaml:"region"          mapstructure:"region"`
	Bucket          string `yaml:"bucket"          mapstructure:"bucket"`
	CloudfrontURL   string `yaml:"cloudfrontUrl"   mapstructure:"cloudfrontUrl"   validate:"omitempty"`
}

// --- catalog (hybrid search + embedding LLM) ---

type Search struct {
	DenseWeight           float32       `yaml:"denseWeight"           mapstructure:"denseWeight"           validate:"required,gte=0,lte=1"`
	SparseWeight          float32       `yaml:"sparseWeight"          mapstructure:"sparseWeight"          validate:"required,gte=0,lte=1"`
	LexicalWeight         float32       `yaml:"lexicalWeight"         mapstructure:"lexicalWeight"         validate:"required,gte=0,lte=1"`
	InteractionBatchSize  int           `yaml:"interactionBatchSize"  mapstructure:"interactionBatchSize"  validate:"required,gte=1"`
	InteractionLinger     time.Duration `yaml:"interactionLinger"     mapstructure:"interactionLinger"     validate:"gte=0"`
	EmbeddingSyncInterval time.Duration `yaml:"embeddingSyncInterval" mapstructure:"embeddingSyncInterval" validate:"gte=0"`
}

type LLM struct {
	Provider      string        `yaml:"provider"      mapstructure:"provider" validate:"required,oneof=python openai bedrock openrouter"`
	EmbedCacheTTL time.Duration `yaml:"embedCacheTTL" mapstructure:"embedCacheTTL" validate:"gte=0"` // 0 = cache forever
	Python        LLMPython     `yaml:"python"        mapstructure:"python"`
	OpenAI        LLMOpenAI     `yaml:"openai"        mapstructure:"openai"`
	Bedrock       LLMBedrock    `yaml:"bedrock"       mapstructure:"bedrock"`
	OpenRouter    LLMOpenRouter `yaml:"openrouter"    mapstructure:"openrouter"`
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

type LLMOpenRouter struct {
	APIKey     string `yaml:"apiKey"     mapstructure:"apiKey"`
	BaseURL    string `yaml:"baseURL"    mapstructure:"baseURL"    validate:"omitempty,url"`
	EmbedModel string `yaml:"embedModel" mapstructure:"embedModel"`
	ChatModel  string `yaml:"chatModel"  mapstructure:"chatModel"`
}

// --- analytic (popularity scoring) ---

// PopularityWeights maps each user-interaction event to its contribution to a
// product's popularity score. Weights can be negative (e.g. ReturnProduct). The
// reflection into map[Event]float64 lives in the analytic module (WeightMap).
type PopularityWeights struct {
	Purchase            float64 `yaml:"purchase"                  mapstructure:"purchase"                  validate:"required"`
	AddToCart           float64 `yaml:"add_to_cart"               mapstructure:"add_to_cart"               validate:"required"`
	View                float64 `yaml:"view"                      mapstructure:"view"                      validate:"required"`
	AddToFavorites      float64 `yaml:"add_to_favorites"          mapstructure:"add_to_favorites"          validate:"required"`
	WriteReview         float64 `yaml:"write_review"              mapstructure:"write_review"              validate:"required"`
	RatingHigh          float64 `yaml:"rating_high"               mapstructure:"rating_high"               validate:"required"`
	RatingMedium        float64 `yaml:"rating_medium"             mapstructure:"rating_medium"             validate:"required"`
	AskQuestion         float64 `yaml:"ask_question"              mapstructure:"ask_question"              validate:"required"`
	ClickFromSearch     float64 `yaml:"click_from_search"         mapstructure:"click_from_search"         validate:"required"`
	ClickFromRecommend  float64 `yaml:"click_from_recommendation" mapstructure:"click_from_recommendation" validate:"required"`
	ClickFromCategory   float64 `yaml:"click_from_category"       mapstructure:"click_from_category"       validate:"required"`
	ViewSimilarProducts float64 `yaml:"view_similar_products"     mapstructure:"view_similar_products"     validate:"required"`
	ProductImpression   float64 `yaml:"product_impression"        mapstructure:"product_impression"        validate:"required"`
	CheckoutStarted     float64 `yaml:"checkout_started"          mapstructure:"checkout_started"          validate:"required"`
	RemoveFromCart      float64 `yaml:"remove_from_cart"          mapstructure:"remove_from_cart"          validate:"required"`
	ReturnProduct       float64 `yaml:"return_product"            mapstructure:"return_product"            validate:"required"`
	RefundRequested     float64 `yaml:"refund_requested"          mapstructure:"refund_requested"          validate:"required"`
	CancelOrder         float64 `yaml:"cancel_order"              mapstructure:"cancel_order"              validate:"required"`
	RatingLow           float64 `yaml:"rating_low"                mapstructure:"rating_low"                validate:"required"`
	ReportProduct       float64 `yaml:"report_product"            mapstructure:"report_product"            validate:"required"`
	Dislike             float64 `yaml:"dislike"                   mapstructure:"dislike"                   validate:"required"`
	HideItem            float64 `yaml:"hide_item"                 mapstructure:"hide_item"                 validate:"required"`
	NotInterested       float64 `yaml:"not_interested"            mapstructure:"not_interested"            validate:"required"`
	ViewBounce          float64 `yaml:"view_bounce"               mapstructure:"view_bounce"               validate:"required"`
}

// --- order (payments + transport) ---

type Order struct {
	PaymentExpiryDays int64 `yaml:"paymentExpiryDays" mapstructure:"paymentExpiryDays" validate:"required,gte=1"`
	// ReturnURL is derived from Public.SiteURL in order/biz/base/options.go — not
	// a config key, so the public origin lives in exactly one place.
}

type Vnpay struct {
	TmnCode    string `yaml:"tmnCode"    mapstructure:"tmnCode"    validate:"required"`
	HashSecret string `yaml:"hashSecret" mapstructure:"hashSecret" validate:"required"`
}

type Sepay struct {
	MerchantID   string `yaml:"merchantId"   mapstructure:"merchantId"`
	SecretKey    string `yaml:"secretKey"    mapstructure:"secretKey"`
	IPNSecretKey string `yaml:"ipnSecretKey" mapstructure:"ipnSecretKey"`
	// PublicBaseURL is derived from Public.SiteURL in order/biz/base/options.go.
	Sandbox bool `yaml:"sandbox" mapstructure:"sandbox"`
}

type CardPayment struct {
	Provider  string `yaml:"provider"  mapstructure:"provider"`
	SecretKey string `yaml:"secretKey" mapstructure:"secretKey"`
	PublicKey string `yaml:"publicKey" mapstructure:"publicKey"`
}

type GHTK struct {
	BaseURL  string `yaml:"baseURL"  mapstructure:"baseURL"`
	APIKey   string `yaml:"apiKey"   mapstructure:"apiKey"`
	ClientID string `yaml:"clientID" mapstructure:"clientID"`
	Secret   string `yaml:"secret"   mapstructure:"secret"`
}

// Mock is a dev-only transport provider that auto-delivers shipments after a
// fixed delay. Leave Enabled=false outside dev.
type Mock struct {
	Enabled      bool `yaml:"enabled"      mapstructure:"enabled"`
	DelaySeconds int  `yaml:"delaySeconds" mapstructure:"delaySeconds"`
}
