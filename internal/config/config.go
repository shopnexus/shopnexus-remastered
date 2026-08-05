// Package config loads configuration from env and validates it, failing fast at startup.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-playground/validator/v10"
)

// Every field is required — no defaults, no fallback. A missing var fails fast.
//
// Each module keeps its own DSN so it can later be split onto a separate
// Postgres; today they all point at the same URL and isolate their tables by
// schema (search_path, set per pool — see module fx.go / cmd/migrate).
//
// The outbound providers are the one place where "required" is conditional, and it is
// still not a default: each seam has a selector, and a vendor's credentials are required
// only when that vendor is the one selected (`required_if`). Without that, running the
// stack locally would need an SMTP account, an SMS contract and a KYC subscription.
// The WORKFLOW_RUNTIME values this binary understands. Named here, beside the field that carries
// them, because the composition root and every module's own selector compare against the same
// two strings — the `oneof` tag above is the one place a literal is unavoidable.
const (
	WorkflowRestate = "restate"
	WorkflowOff     = "off"
)

type Config struct {
	GatewayAddr string `validate:"required,hostname_port"`
	// InstanceID tags every telemetry row with the pod/host that produced it;
	// without it several replicas collapse into one meaningless series. Required
	// rather than defaulted to the hostname: a wrong-but-plausible value is worse
	// than a startup failure. In Kubernetes use the downward API (metadata.name).
	InstanceID   string `validate:"required"`
	AccountDBDSN string `validate:"required"`
	CatalogDBDSN string `validate:"required"`
	OrderDBDSN   string `validate:"required"`
	ChatDBDSN    string `validate:"required"`
	TrustDBDSN   string `validate:"required"`
	FinanceDBDSN string `validate:"required"`
	// ObservabilityDBDSN backs the observability schema (product events + metrics hypertables).
	ObservabilityDBDSN string `validate:"required"`
	// NATSURL is the JetStream bus that buffers telemetry between the Sink and
	// the writer (e.g. nats://nats:4222).
	NATSURL       string `validate:"required"`
	RedisAddr     string `validate:"required,hostname_port"`
	RedisPassword string `validate:"required"`
	JWTSecret     string `validate:"required,min=32"`
	// IDCipherKey keys the permutation behind every opaque id (shared/id). 16, 24
	// or 32 bytes — id.SetCipher owns that rule, so it is not repeated here.
	// Rotating it invalidates every id ever handed out: back it up like a DB
	// credential.
	IDCipherKey string `validate:"required"`
	LogLevel    string `validate:"required,oneof=debug info warn error"`

	// StorageProvider is where a *new* uploaded byte goes. `local` keeps objects on this host's
	// disk and signs URLs back to the gateway's own upload route — a real store signs the
	// bucket directly and the bytes never touch this process. Same rule as the other seams: a
	// selector, not a default, because a deployment that thinks photos are in a bucket and
	// finds them on a pod's disk discovers it the first time the pod is replaced.
	//
	// It does not decide what can be *read*. Every `resource` row records the store holding it,
	// and storage.Registry keeps them all reachable, so changing this moves where the next
	// upload lands without stranding a single object that predates the change.
	StorageProvider string `validate:"required,oneof=local"`
	// StorageRoot is the directory `local` stores objects under.
	StorageRoot string `validate:"required_if=StorageProvider local"`
	// StorageBaseURL is where this gateway answers the upload and download routes as a client
	// sees them — behind a proxy that is not the address the server binds.
	StorageBaseURL string `validate:"required_if=StorageProvider local,omitempty,url"`
	// StorageSecret keys the signature on an upload slot. Its own secret, not the JWT's: this
	// one signs a URL that sits in a client's memory, and rotating it invalidates only the
	// slots still in flight.
	StorageSecret string `validate:"required_if=StorageProvider local,omitempty,min=32"`
	// StorageUploadTTL is how long a slot stays writable, StorageDownloadTTL how long a read
	// link lives. Both short: a link that outlives the page it was rendered for is a hole.
	StorageUploadTTL   time.Duration `validate:"required"`
	StorageDownloadTTL time.Duration `validate:"required"`
	// StorageMaxUploadBytes is the largest object accepted, refused before a byte moves.
	// StorageMaxVideoBytes is the same for a `video/*` type: one limit for both would have to be
	// the video's, and a 100 MB avatar or PDF is then a slot the platform happily signs.
	StorageMaxUploadBytes int64 `validate:"required,gt=0"`
	StorageMaxVideoBytes  int64 `validate:"required,gt=0"`
	// StorageAllowedMimes is what may be stored at all. An allowlist: a store that accepts
	// anything serves anything back, and `text/html` from your own domain is a stored script.
	StorageAllowedMimes []string `validate:"required,min=1"`

	// --- embedding (cmd/embedder) ---

	// EmbeddingProvider picks the model behind the vectors search ranks on. `mock` hashes the
	// words instead, which exercises the whole queue-to-column path without the multi-gigabyte
	// download — a stack that cannot run without a GPU is a stack nobody runs locally. Same
	// rule as the other seams: a selector, not a default.
	EmbeddingProvider string `validate:"required,oneof=bge-m3 mock"`
	EmbeddingBaseURL  string `validate:"required_if=EmbeddingProvider bge-m3,omitempty,url"`
	// EmbeddingAPIKey is the model service's bearer token. Required with the real provider like
	// every other vendor credential: the service holds a GPU and answers anyone who can reach
	// it, so an unauthenticated deployment is one somebody else can spend.
	EmbeddingAPIKey string `validate:"required_if=EmbeddingProvider bge-m3"`
	// EmbeddingTimeout bounds one batch. Generous next to the other providers: this is a
	// transformer over a batch of documents, often on a CPU.
	EmbeddingTimeout time.Duration `validate:"required_if=EmbeddingProvider bge-m3"`
	// EmbeddingDimensions is the dense width, and it must equal what
	// `catalog.listing_embedding.dense` was declared with — the model service lives in another
	// repository, so nothing but this check couples the two. Every answer is measured against
	// it, because a model of the wrong size does not degrade: every row fails until one of the
	// two changes, and a migration is the other half of changing it.
	EmbeddingDimensions int `validate:"required,gt=0"`
	// EmbeddingInterval is how often the queues are drained. One pass empties them, so this is
	// how long a new listing waits to become searchable, not how fast a backlog is worked off.
	EmbeddingInterval time.Duration `validate:"required"`
	// EmbeddingBatchSize is rows per model call, which also bounds the write transaction and
	// how much a crash repeats.
	EmbeddingBatchSize int `validate:"required,gt=0"`
	// EmbeddingMaxTextChars clips what the model reads. The sparse vector has one non-zero per
	// distinct token and the HNSW index refuses more than a thousand, so an unbounded
	// description is a failed write rather than a poor result.
	EmbeddingMaxTextChars int `validate:"required,gt=0"`

	// --- listing suggestions (the "photo in, listing out" route) ---

	// LLMProvider picks who reads a seller's photo and voice note. `mock` answers a plausible
	// filled form with no model, no key and no network call, so a local stack walks the whole
	// flow — same rule as the other seams: a selector, not a default, because a deployment that
	// thinks it has a model and does not is discovered by the seller whose form stayed empty.
	LLMProvider string `validate:"required,oneof=litellm mock"`
	// The proxy every model is reached through. One base URL and one key, because chat and
	// transcription are the same vendor account.
	LiteLLMBaseURL string `validate:"required_if=LLMProvider litellm,omitempty,url"`
	LiteLLMAPIKey  string `validate:"required_if=LLMProvider litellm"`
	// LiteLLMChatModel has to be one that reads images: the photo is the input a seller cares
	// about, and a text-only model answers a form filled from the note alone.
	LiteLLMChatModel       string        `validate:"required_if=LLMProvider litellm"`
	LiteLLMTranscribeModel string        `validate:"required_if=LLMProvider litellm"`
	LiteLLMRequestTimeout  time.Duration `validate:"required_if=LLMProvider litellm"`
	// LiteLLMStreamTimeout covers a whole streamed generation. Nothing streams today; it is
	// required because the client refuses to be built without one, for the reason its own
	// comment gives.
	LiteLLMStreamTimeout time.Duration `validate:"required_if=LLMProvider litellm"`

	// --- durable execution ---

	// WorkflowRuntime picks who holds the timers this marketplace waits on — an unpaid
	// checkout expiring, an escrow window closing, a refund deadline passing. `restate`
	// starts a run per entity and signals it; `off` leaves every transition to the sweep,
	// which is the same code on a slower clock. Same rule as the provider seams: a selector,
	// not a default, so a deployment that thinks it has durable timers and does not is found
	// at startup rather than by the seller who was never paid.
	WorkflowRuntime string `validate:"required,oneof=restate off"`
	// RestateServeAddr is where the SDK serves the workflow handlers for the runtime to
	// invoke; RestateIngressURL is where this process submits and signals runs.
	RestateServeAddr   string        `validate:"required_if=WorkflowRuntime restate,omitempty,hostname_port"`
	RestateIngressURL  string        `validate:"required_if=WorkflowRuntime restate,omitempty,url"`
	RestateSendTimeout time.Duration `validate:"required_if=WorkflowRuntime restate"`
	// SweepInterval is how often the timed transitions are swept. Required even with
	// Restate: the sweep is the net under a lost run, and with a runtime in place it finds
	// nothing, which is what makes it cheap to leave on.
	SweepInterval time.Duration `validate:"required"`

	// --- outbound providers: which implementation each seam gets ---

	// EmailProvider and SMSProvider are separate because no vendor is good at both:
	// "smtp" against any relay, "esms" for the Vietnamese brandname aggregator, "mock"
	// to log what would have been sent.
	EmailProvider string `validate:"required,oneof=smtp mock"`
	SMSProvider   string `validate:"required,oneof=esms mock"`
	// OAuthVerifier is "oidc" to verify a real id token, or "mock" to trust the
	// credential in dev.
	OAuthVerifier string `validate:"required,oneof=oidc mock"`
	// KYCProvider is "fpt-ai" for the real check, or "mock" to leave every case pending
	// for a moderator.
	KYCProvider string `validate:"required,oneof=fpt-ai mock"`
	// TransportProvider is the courier a shipping fee is quoted from. The buyer pays delivery on
	// every sale, so this is on the money path: a quote nobody asked for means the platform
	// silently absorbs the carrier's bill. `mock` prices a flat fee, which is enough to exercise
	// the whole flow without a courier contract.
	TransportProvider string `validate:"required,oneof=mock"`
	// PaymentReturnURLHosts is where a payment gateway may send a payer back. An allowlist
	// rather than any URL the client sends: a redirect target nobody checked is an open
	// redirect wearing a payment flow, and the platform's own domain is what lends it
	// credibility. Comma-separated hosts, no scheme.
	PaymentReturnURLHosts []string `validate:"required,min=1,dive,required,hostname_port|hostname"`
	// PaymentProvider picks the rail money actually moves on. `mock` settles
	// synchronously with no gateway, which is what dev and the tests use.
	PaymentProvider string `validate:"required,oneof=mock"`

	// --- SMTP ---

	SMTPHost     string `validate:"required_if=EmailProvider smtp"`
	SMTPPort     int    `validate:"required_if=EmailProvider smtp"`
	SMTPUsername string `validate:"required_if=EmailProvider smtp"`
	SMTPPassword string `validate:"required_if=EmailProvider smtp"`
	// SMTPFrom is the header and envelope sender, e.g. "ShopNexus <no-reply@shopnexus.vn>".
	SMTPFrom    string        `validate:"required_if=EmailProvider smtp"`
	SMTPTimeout time.Duration `validate:"required_if=EmailProvider smtp"`
	// VerifyEmailURL and ResetPasswordURL are the *client's* pages; the API appends the
	// token as a query parameter. They cannot be derived from a request path, because the
	// API does not serve those pages.
	VerifyEmailURL   string `validate:"required_if=EmailProvider smtp,omitempty,url"`
	ResetPasswordURL string `validate:"required_if=EmailProvider smtp,omitempty,url"`

	// --- eSMS.vn ---

	ESMSBaseURL   string `validate:"required_if=SMSProvider esms,omitempty,url"`
	ESMSAPIKey    string `validate:"required_if=SMSProvider esms"`
	ESMSSecretKey string `validate:"required_if=SMSProvider esms"`
	// ESMSBrandname is the registered sender name; an unregistered one is rejected by
	// the aggregator.
	ESMSBrandname string `validate:"required_if=SMSProvider esms"`
	// ESMSSMSType is eSMS's message class, which follows from the contract — customer
	// care and advertising traffic are priced and routed differently.
	ESMSSMSType string `validate:"required_if=SMSProvider esms"`
	// ESMSContentTemplate must match the template registered with the carriers. It is
	// given {{.Code}}.
	ESMSContentTemplate string `validate:"required_if=SMSProvider esms"`
	// ESMSUnicode doubles the cost per segment, so leave it false for a template written
	// without diacritics.
	ESMSUnicode bool
	// ESMSSandbox has the aggregator accept and drop the message: for staging, which
	// should not spend credit or ring a real phone.
	ESMSSandbox bool
	ESMSTimeout time.Duration `validate:"required_if=SMSProvider esms"`

	// --- OIDC ---

	// OIDCGoogleAudiences and OIDCAppleAudiences are comma-separated client ids. A
	// provider with no audience configured is simply not offered; at least one of them
	// has to be set when the verifier is "oidc", which the verifier itself enforces.
	OIDCGoogleAudiences []string
	OIDCAppleAudiences  []string
	OIDCTimeout         time.Duration `validate:"required_if=OAuthVerifier oidc"`

	// --- FPT.AI eKYC ---

	FPTAIBaseURL string `validate:"required_if=KYCProvider fpt-ai,omitempty,url"`
	FPTAIAPIKey  string `validate:"required_if=KYCProvider fpt-ai"`
	// FPTAIRequestTimeout bounds one vendor call, FPTAIDownloadTimeout one scan fetch
	// from storage. Separate because they are different dependencies.
	FPTAIRequestTimeout  time.Duration `validate:"required_if=KYCProvider fpt-ai"`
	FPTAIDownloadTimeout time.Duration `validate:"required_if=KYCProvider fpt-ai"`

	// --- WebSocket realtime ---

	// WSTicketTTL is how long a handshake ticket lives before it is useless — short,
	// because it only has to survive the moment between minting it and opening the socket.
	WSTicketTTL time.Duration `validate:"required"`
	// WSWriteTimeout bounds one write and one ping, same rule as every other outbound
	// deadline here: a socket write that never completes must not hang the pump forever.
	WSWriteTimeout time.Duration `validate:"required"`
	// WSPingInterval is how often an idle socket is probed, so a peer that vanished
	// without a FIN is found by a failed pong instead of an accumulating goroutine.
	WSPingInterval time.Duration `validate:"required"`
	// WSSendBuffer is how many envelopes a socket may fall behind before the hub drops it.
	WSSendBuffer int `validate:"required,gt=0"`
	// WSMaxPerAccount caps concurrent sockets per account, so a tab-spammer cannot exhaust
	// the process's file descriptors.
	WSMaxPerAccount int `validate:"required,gt=0"`
	// WSAllowedOrigins is matched against the Origin header's host, not a full URL —
	// that is what coder/websocket's OriginPatterns compares against.
	WSAllowedOrigins []string `validate:"required,min=1"`
}

func Load(v *validator.Validate) (*Config, error) {
	// p collects parse failures so a startup error names every malformed var at once,
	// rather than one per restart.
	var p parser
	cfg := &Config{
		GatewayAddr:        os.Getenv("GATEWAY_ADDR"),
		InstanceID:         os.Getenv("INSTANCE_ID"),
		AccountDBDSN:       os.Getenv("ACCOUNT_DB_DSN"),
		CatalogDBDSN:       os.Getenv("CATALOG_DB_DSN"),
		OrderDBDSN:         os.Getenv("ORDER_DB_DSN"),
		ChatDBDSN:          os.Getenv("CHAT_DB_DSN"),
		TrustDBDSN:         os.Getenv("TRUST_DB_DSN"),
		FinanceDBDSN:       os.Getenv("FINANCE_DB_DSN"),
		ObservabilityDBDSN: os.Getenv("OBSERVABILITY_DB_DSN"),
		NATSURL:            os.Getenv("NATS_URL"),
		RedisAddr:          os.Getenv("REDIS_ADDR"),
		RedisPassword:      os.Getenv("REDIS_PASSWORD"),
		JWTSecret:          os.Getenv("JWT_SECRET"),
		IDCipherKey:        os.Getenv("ID_CIPHER_KEY"),
		LogLevel:           os.Getenv("LOG_LEVEL"),

		StorageProvider:       os.Getenv("STORAGE_PROVIDER"),
		StorageRoot:           os.Getenv("STORAGE_ROOT"),
		StorageBaseURL:        os.Getenv("STORAGE_BASE_URL"),
		StorageSecret:         os.Getenv("STORAGE_SECRET"),
		StorageUploadTTL:      p.durationVar("STORAGE_UPLOAD_TTL"),
		StorageDownloadTTL:    p.durationVar("STORAGE_DOWNLOAD_TTL"),
		StorageMaxUploadBytes: p.int64Var("STORAGE_MAX_UPLOAD_BYTES"),
		StorageMaxVideoBytes:  p.int64Var("STORAGE_MAX_VIDEO_BYTES"),
		StorageAllowedMimes:   listVar("STORAGE_ALLOWED_MIMES"),

		LLMProvider:            os.Getenv("LLM_PROVIDER"),
		LiteLLMBaseURL:         os.Getenv("LITELLM_BASE_URL"),
		LiteLLMAPIKey:          os.Getenv("LITELLM_API_KEY"),
		LiteLLMChatModel:       os.Getenv("LITELLM_CHAT_MODEL"),
		LiteLLMTranscribeModel: os.Getenv("LITELLM_TRANSCRIBE_MODEL"),
		LiteLLMRequestTimeout:  p.durationVar("LITELLM_REQUEST_TIMEOUT"),
		LiteLLMStreamTimeout:   p.durationVar("LITELLM_STREAM_TIMEOUT"),

		EmbeddingProvider:     os.Getenv("EMBEDDING_PROVIDER"),
		EmbeddingBaseURL:      os.Getenv("EMBEDDING_BASE_URL"),
		EmbeddingAPIKey:       os.Getenv("EMBEDDING_API_KEY"),
		EmbeddingTimeout:      p.durationVar("EMBEDDING_TIMEOUT"),
		EmbeddingDimensions:   p.intVar("EMBEDDING_DIMENSIONS"),
		EmbeddingInterval:     p.durationVar("EMBEDDING_INTERVAL"),
		EmbeddingBatchSize:    p.intVar("EMBEDDING_BATCH_SIZE"),
		EmbeddingMaxTextChars: p.intVar("EMBEDDING_MAX_TEXT_CHARS"),

		WorkflowRuntime:    os.Getenv("WORKFLOW_RUNTIME"),
		RestateServeAddr:   os.Getenv("RESTATE_SERVE_ADDR"),
		RestateIngressURL:  os.Getenv("RESTATE_INGRESS_URL"),
		RestateSendTimeout: p.durationVar("RESTATE_SEND_TIMEOUT"),
		SweepInterval:      p.durationVar("SWEEP_INTERVAL"),

		EmailProvider:         os.Getenv("EMAIL_PROVIDER"),
		SMSProvider:           os.Getenv("SMS_PROVIDER"),
		OAuthVerifier:         os.Getenv("OAUTH_VERIFIER"),
		KYCProvider:           os.Getenv("KYC_PROVIDER"),
		PaymentProvider:       os.Getenv("PAYMENT_PROVIDER"),
		TransportProvider:     os.Getenv("TRANSPORT_PROVIDER"),
		PaymentReturnURLHosts: listVar("PAYMENT_RETURN_URL_HOSTS"),

		SMTPHost:         os.Getenv("SMTP_HOST"),
		SMTPPort:         p.intVar("SMTP_PORT"),
		SMTPUsername:     os.Getenv("SMTP_USERNAME"),
		SMTPPassword:     os.Getenv("SMTP_PASSWORD"),
		SMTPFrom:         os.Getenv("SMTP_FROM"),
		SMTPTimeout:      p.durationVar("SMTP_TIMEOUT"),
		VerifyEmailURL:   os.Getenv("VERIFY_EMAIL_URL"),
		ResetPasswordURL: os.Getenv("RESET_PASSWORD_URL"),

		ESMSBaseURL:         os.Getenv("ESMS_BASE_URL"),
		ESMSAPIKey:          os.Getenv("ESMS_API_KEY"),
		ESMSSecretKey:       os.Getenv("ESMS_SECRET_KEY"),
		ESMSBrandname:       os.Getenv("ESMS_BRANDNAME"),
		ESMSSMSType:         os.Getenv("ESMS_SMS_TYPE"),
		ESMSContentTemplate: os.Getenv("ESMS_CONTENT_TEMPLATE"),
		ESMSUnicode:         p.boolVar("ESMS_UNICODE"),
		ESMSSandbox:         p.boolVar("ESMS_SANDBOX"),
		ESMSTimeout:         p.durationVar("ESMS_TIMEOUT"),

		OIDCGoogleAudiences: listVar("OIDC_GOOGLE_AUDIENCES"),
		OIDCAppleAudiences:  listVar("OIDC_APPLE_AUDIENCES"),
		OIDCTimeout:         p.durationVar("OIDC_TIMEOUT"),

		FPTAIBaseURL:         os.Getenv("FPT_AI_BASE_URL"),
		FPTAIAPIKey:          os.Getenv("FPT_AI_API_KEY"),
		FPTAIRequestTimeout:  p.durationVar("FPT_AI_REQUEST_TIMEOUT"),
		FPTAIDownloadTimeout: p.durationVar("FPT_AI_DOWNLOAD_TIMEOUT"),

		WSTicketTTL:      p.durationVar("WS_TICKET_TTL"),
		WSWriteTimeout:   p.durationVar("WS_WRITE_TIMEOUT"),
		WSPingInterval:   p.durationVar("WS_PING_INTERVAL"),
		WSSendBuffer:     p.intVar("WS_SEND_BUFFER"),
		WSMaxPerAccount:  p.intVar("WS_MAX_PER_ACCOUNT"),
		WSAllowedOrigins: listVar("WS_ALLOWED_ORIGINS"),
	}
	if err := p.err(); err != nil {
		return nil, err
	}
	if err := v.Struct(cfg); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}
	return cfg, nil
}

// parser reads the typed vars. An empty var is the zero value rather than an error: the
// validate tags decide whether this deployment needed it, so a mock setup does not have
// to spell out an SMTP port it will never dial.
type parser struct {
	problems []string
}

func (p *parser) intVar(name string) int {
	raw := os.Getenv(name)
	if raw == "" {
		return 0
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		p.problems = append(p.problems, fmt.Sprintf("%s must be an integer, got %q", name, raw))
		return 0
	}
	return v
}

// int64Var is intVar for a value that is a size rather than a count — a byte limit outgrows an
// int on a 32-bit build, and a truncated one is a limit nobody set.
func (p *parser) int64Var(name string) int64 {
	raw := os.Getenv(name)
	if raw == "" {
		return 0
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		p.problems = append(p.problems, fmt.Sprintf("%s must be an integer, got %q", name, raw))
		return 0
	}
	return v
}

func (p *parser) durationVar(name string) time.Duration {
	raw := os.Getenv(name)
	if raw == "" {
		return 0
	}
	v, err := time.ParseDuration(raw)
	if err != nil {
		p.problems = append(p.problems, fmt.Sprintf("%s must be a duration such as 10s, got %q", name, raw))
		return 0
	}
	return v
}

func (p *parser) boolVar(name string) bool {
	raw := os.Getenv(name)
	if raw == "" {
		return false
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		p.problems = append(p.problems, fmt.Sprintf("%s must be true or false, got %q", name, raw))
		return false
	}
	return v
}

// listVar reads a comma-separated var, dropping blanks so a trailing comma is not an
// empty audience nobody notices.
func listVar(name string) []string {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if v := strings.TrimSpace(part); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func (p *parser) err() error {
	if len(p.problems) == 0 {
		return nil
	}
	return fmt.Errorf("parse config: %s", strings.Join(p.problems, "; "))
}
