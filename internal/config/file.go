package config

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/go-playground/validator/v10"
	"gopkg.in/yaml.v3"
)

// The configuration file, and the only place a value comes from. No environment variables and no
// defaults: a deployment says what it is out loud, in one grouped document, or it does not start.
//
// Two structures, on purpose. This one is the *file* — nested, so the document can be read by
// section — and it carries the `validate` tags, so a failure names the path somebody has to edit
// (`Storage.Secret` is `storage: secret:`). The flat `Config` next door is the *program's* shape,
// which every module reads field by field; keeping them apart is what lets the document be grouped
// without a hundred call sites learning a new path.
//
// Conditionals are `required_if` against a sibling in the same section, which is why each seam's
// selector sits with the vendor fields it governs. The one rule a tag cannot express — "these
// credentials are required because this *list* names their provider" — is a struct-level validation
// registered below.

// DefaultPath is where the document is read from when nothing says otherwise: relative to the
// working directory, which for `go run ./cmd/...` is this repository. Deliberately *not* embedded —
// the file is gitignored so it can hold real credentials, and embedding it would make a fresh
// checkout fail to build.
const DefaultPath = "internal/config/config.dev.yml"

// FileVar names the document to read instead — how a container and a real deployment supply their
// own. The one thing the environment still decides is *where the file is*; no value in it has an
// environment fallback, and none has a default.
const FileVar = "CONFIG_FILE"

// Duration is a YAML duration ("15m", "5s"). Its own type because yaml.v3 will not decode a string
// into time.Duration, and a config that took plain seconds would be one nobody can read.
type Duration time.Duration

func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	var raw string
	if err := node.Decode(&raw); err != nil {
		return fmt.Errorf("a duration has to be a string such as 10s, got %q", node.Value)
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil {
		return fmt.Errorf("%q is not a duration such as 10s or 15m", raw)
	}
	*d = Duration(parsed)
	return nil
}

func (d Duration) std() time.Duration { return time.Duration(d) }

type file struct {
	Gateway   gatewaySection   `yaml:"gateway" validate:"required"`
	Database  databaseSection  `yaml:"database" validate:"required"`
	Redis     redisSection     `yaml:"redis" validate:"required"`
	NATS      natsSection      `yaml:"nats" validate:"required"`
	Security  securitySection  `yaml:"security" validate:"required"`
	Storage   storageSection   `yaml:"storage" validate:"required"`
	Embedding embeddingSection `yaml:"embedding" validate:"required"`
	LLM       llmSection       `yaml:"llm" validate:"required"`
	Workflow  workflowSection  `yaml:"workflow" validate:"required"`
	Email     emailSection     `yaml:"email" validate:"required"`
	SMS       smsSection       `yaml:"sms" validate:"required"`
	OAuth     oauthSection     `yaml:"oauth" validate:"required"`
	KYC       kycSection       `yaml:"kyc" validate:"required"`
	Payment   paymentSection   `yaml:"payment" validate:"required"`
	Transport transportSection `yaml:"transport" validate:"required"`
	WebSocket websocketSection `yaml:"websocket" validate:"required"`
}

type gatewaySection struct {
	Addr string `yaml:"addr" validate:"required,hostname_port"`
	// PublicBaseURL is where this gateway answers as a client sees it, API base path included —
	// behind a proxy that is not the address it binds. One value, because every user of it needs the
	// same answer: `local` storage signs links back to this gateway's own routes, and a redirect
	// rail hands a browser an absolute link to a page this process serves.
	PublicBaseURL string `yaml:"public_base_url" validate:"required,url"`
	// AllowedOrigins is where a browser may call this API from: full origins, matched exactly. Full
	// origins rather than the hosts the WebSocket list holds, because that is the form the browser
	// sends and the form the allow header has to echo.
	AllowedOrigins []string `yaml:"allowed_origins" validate:"required,min=1,dive,required"`
	LogLevel       string   `yaml:"log_level" validate:"required,oneof=debug info warn error"`
}

// databaseSection is one DSN per module, so a module can later move to its own Postgres. Today they
// all point at the same server and isolate their tables by schema.
type databaseSection struct {
	Account string `yaml:"account" validate:"required"`
	Catalog string `yaml:"catalog" validate:"required"`
	Order   string `yaml:"order" validate:"required"`
	Chat    string `yaml:"chat" validate:"required"`
	Trust   string `yaml:"trust" validate:"required"`
	Finance string `yaml:"finance" validate:"required"`
	// Observability backs the telemetry hypertables.
	Observability string `yaml:"observability" validate:"required"`
}

type redisSection struct {
	Addr     string `yaml:"addr" validate:"required,hostname_port"`
	Password string `yaml:"password" validate:"required"`
}

// natsSection is the JetStream bus that buffers telemetry between the Sink and the writer.
type natsSection struct {
	URL string `yaml:"url" validate:"required"`
}

type securitySection struct {
	JWTSecret string `yaml:"jwt_secret" validate:"required,min=32"`
	// IDCipherKey keys the permutation behind every opaque id (shared/id). 16, 24 or 32 bytes —
	// id.SetCipher owns that rule, so it is not repeated here. Rotating it invalidates every id ever
	// handed out: back it up like a database credential.
	IDCipherKey string `yaml:"id_cipher_key" validate:"required"`
	// InstanceID tags every telemetry row with the pod that produced it; without it several replicas
	// collapse into one meaningless series. In Kubernetes take it from the downward API.
	InstanceID string `yaml:"instance_id" validate:"required"`
}

// storageSection decides where a *new* uploaded byte goes, not what can be read: every `resource`
// row records the store holding it and all of them stay reachable, so changing the provider moves
// where the next upload lands without stranding a single object that predates the change.
type storageSection struct {
	Provider string `yaml:"provider" validate:"required,oneof=local"`
	Root     string `yaml:"root" validate:"required_if=Provider local"`
	// Secret keys the signature on an upload slot. Its own secret, not the JWT's: this one signs a
	// URL that sits in a client's memory, and rotating it invalidates only the slots in flight.
	Secret string `yaml:"secret" validate:"required_if=Provider local,omitempty,min=32"`
	// UploadTTL is how long a slot stays writable, DownloadTTL how long a read link lives. Both
	// short: a link that outlives the page it was rendered for is a hole.
	UploadTTL   Duration `yaml:"upload_ttl" validate:"required"`
	DownloadTTL Duration `yaml:"download_ttl" validate:"required"`
	// MaxUploadBytes is the largest object accepted, refused before a byte moves. MaxVideoBytes is
	// the same for a `video/*` type: one limit for both would have to be the video's, and a 100 MB
	// avatar is then a slot the platform happily signs.
	MaxUploadBytes int64 `yaml:"max_upload_bytes" validate:"required,gt=0"`
	MaxVideoBytes  int64 `yaml:"max_video_bytes" validate:"required,gt=0"`
	// AllowedMimes is what may be stored at all. An allowlist: a store that accepts anything serves
	// anything back, and `text/html` from your own domain is a stored script.
	AllowedMimes []string `yaml:"allowed_mimes" validate:"required,min=1"`
}

// embeddingSection is the model behind the vectors search ranks on. `mock` hashes the words instead,
// which exercises the whole queue-to-column path without a multi-gigabyte download.
type embeddingSection struct {
	Provider string `yaml:"provider" validate:"required,oneof=bge-m3 mock"`
	BaseURL  string `yaml:"base_url" validate:"required_if=Provider bge-m3,omitempty,url"`
	// APIKey is the model service's bearer token, required with the real provider like every other
	// vendor credential: the service holds a GPU and answers anyone who can reach it.
	APIKey string `yaml:"api_key" validate:"required_if=Provider bge-m3"`
	// Timeout bounds one batch. Generous next to the other providers: this is a transformer over a
	// batch of documents, often on a CPU.
	Timeout Duration `yaml:"timeout" validate:"required_if=Provider bge-m3"`
	// Dimensions is the dense width, and it must equal what `catalog.listing_embedding.dense` was
	// declared with — the model service lives in another repository, so nothing but this check
	// couples the two, and a model of the wrong size does not degrade: every row fails.
	Dimensions int `yaml:"dimensions" validate:"required,gt=0"`
	// Interval is how often the queues are drained. One pass empties them, so this is how long a new
	// listing waits to become searchable, not how fast a backlog is worked off.
	Interval Duration `yaml:"interval" validate:"required"`
	// BatchSize is rows per model call, which also bounds the write transaction and how much a
	// crash repeats.
	BatchSize int `yaml:"batch_size" validate:"required,gt=0"`
	// MaxTextChars clips what the model reads: the sparse vector has one non-zero per distinct token
	// and the index refuses more than a thousand, so an unbounded description is a failed write.
	MaxTextChars int `yaml:"max_text_chars" validate:"required,gt=0"`
}

// llmSection is who reads a seller's photo and voice note for the suggestion route.
type llmSection struct {
	Provider string `yaml:"provider" validate:"required,oneof=litellm mock"`
	// One base URL and one key: chat and transcription are the same vendor account.
	BaseURL string `yaml:"base_url" validate:"required_if=Provider litellm,omitempty,url"`
	APIKey  string `yaml:"api_key" validate:"required_if=Provider litellm"`
	// ChatModel has to be one that reads images: the photo is the input a seller cares about, and a
	// text-only model answers a form filled from the note alone.
	ChatModel       string   `yaml:"chat_model" validate:"required_if=Provider litellm"`
	TranscribeModel string   `yaml:"transcribe_model" validate:"required_if=Provider litellm"`
	RequestTimeout  Duration `yaml:"request_timeout" validate:"required_if=Provider litellm"`
	// StreamTimeout covers a whole streamed generation. Nothing streams today; it is required
	// because the client refuses to be built without one.
	StreamTimeout Duration `yaml:"stream_timeout" validate:"required_if=Provider litellm"`
}

// workflowSection picks who holds the timers this marketplace waits on. `restate` starts a run per
// entity; `off` leaves every transition to the sweep, which is the same code on a slower clock.
type workflowSection struct {
	Runtime string `yaml:"runtime" validate:"required,oneof=restate off"`
	// ServeAddr is where the SDK serves the handlers for the runtime to invoke; IngressURL is where
	// this process submits and signals runs.
	ServeAddr   string   `yaml:"serve_addr" validate:"required_if=Runtime restate,omitempty,hostname_port"`
	IngressURL  string   `yaml:"ingress_url" validate:"required_if=Runtime restate,omitempty,url"`
	SendTimeout Duration `yaml:"send_timeout" validate:"required_if=Runtime restate"`
	// SweepInterval is required even with Restate: the sweep is the net under a lost run, and with a
	// runtime in place it finds nothing, which is what makes it cheap to leave on.
	SweepInterval Duration `yaml:"sweep_interval" validate:"required"`
}

// emailSection and smsSection are separate seams because no vendor is good at both.
type emailSection struct {
	Provider     string `yaml:"provider" validate:"required,oneof=smtp mock"`
	SMTPHost     string `yaml:"smtp_host" validate:"required_if=Provider smtp"`
	SMTPPort     int    `yaml:"smtp_port" validate:"required_if=Provider smtp"`
	SMTPUsername string `yaml:"smtp_username" validate:"required_if=Provider smtp"`
	SMTPPassword string `yaml:"smtp_password" validate:"required_if=Provider smtp"`
	// From is the header and envelope sender, e.g. "ShopNexus <no-reply@shopnexus.vn>".
	From    string   `yaml:"from" validate:"required_if=Provider smtp"`
	Timeout Duration `yaml:"timeout" validate:"required_if=Provider smtp"`
	// VerifyURL and ResetPasswordURL are the *client's* pages; the API appends the token as a query
	// parameter. They cannot be derived from a request path, because the API does not serve them.
	VerifyURL        string `yaml:"verify_url" validate:"required_if=Provider smtp,omitempty,url"`
	ResetPasswordURL string `yaml:"reset_password_url" validate:"required_if=Provider smtp,omitempty,url"`
}

type smsSection struct {
	Provider  string `yaml:"provider" validate:"required,oneof=esms mock"`
	ESMSBase  string `yaml:"esms_base_url" validate:"required_if=Provider esms,omitempty,url"`
	APIKey    string `yaml:"esms_api_key" validate:"required_if=Provider esms"`
	SecretKey string `yaml:"esms_secret_key" validate:"required_if=Provider esms"`
	// Brandname is the registered sender name; an unregistered one is rejected by the aggregator.
	Brandname string `yaml:"esms_brandname" validate:"required_if=Provider esms"`
	// SMSType is eSMS's message class, which follows from the contract — customer care and
	// advertising traffic are priced and routed differently.
	SMSType string `yaml:"esms_sms_type" validate:"required_if=Provider esms"`
	// ContentTemplate must match the template registered with the carriers. It is given {{.Code}}.
	ContentTemplate string `yaml:"esms_content_template" validate:"required_if=Provider esms"`
	// Unicode doubles the cost per segment, so leave it false for a template written without
	// diacritics. Sandbox has the aggregator accept and drop the message.
	Unicode bool     `yaml:"esms_unicode"`
	Sandbox bool     `yaml:"esms_sandbox"`
	Timeout Duration `yaml:"esms_timeout" validate:"required_if=Provider esms"`
}

type oauthSection struct {
	Verifier string `yaml:"verifier" validate:"required,oneof=oidc mock"`
	// A provider with no audience configured is simply not offered; at least one has to be set when
	// the verifier is "oidc", which the verifier itself enforces.
	GoogleAudiences []string `yaml:"google_audiences"`
	AppleAudiences  []string `yaml:"apple_audiences"`
	Timeout         Duration `yaml:"timeout" validate:"required_if=Verifier oidc"`
}

type kycSection struct {
	Provider string `yaml:"provider" validate:"required,oneof=fpt-ai mock"`
	BaseURL  string `yaml:"base_url" validate:"required_if=Provider fpt-ai,omitempty,url"`
	APIKey   string `yaml:"api_key" validate:"required_if=Provider fpt-ai"`
	// RequestTimeout bounds one vendor call, DownloadTimeout one scan fetch from storage. Separate
	// because they are different dependencies.
	RequestTimeout  Duration `yaml:"request_timeout" validate:"required_if=Provider fpt-ai"`
	DownloadTimeout Duration `yaml:"download_timeout" validate:"required_if=Provider fpt-ai"`
}

// paymentSection is a *list* of implementations to register, not a selector: an `option` row names
// which one serves it, so two rails can be live at once and an admin can move one without a restart.
// A provider left out is one no row can name.
type paymentSection struct {
	Providers []string `yaml:"providers" validate:"required,min=1,dive,oneof=mock sepay stripe"`
	// ReturnURLHosts is where a gateway may send a payer back. An allowlist rather than any URL the
	// client sends: an unchecked redirect target is an open redirect wearing a payment flow.
	ReturnURLHosts []string      `yaml:"return_url_hosts" validate:"required,min=1,dive,required,hostname_port|hostname"`
	SePay          sepaySection  `yaml:"sepay"`
	Stripe         stripeSection `yaml:"stripe"`
}

// sepaySection's fields are required when `providers` names sepay — a list a tag cannot ask about,
// so the check is the struct-level validation below.
type sepaySection struct {
	MerchantID string `yaml:"merchant_id"`
	// SecretKey signs our checkout to SePay; IPNSecretKey authenticates SePay's callback to us. Two
	// secrets, opposite directions.
	SecretKey    string `yaml:"secret_key"`
	IPNSecretKey string `yaml:"ipn_secret_key"`
	// Sandbox picks SePay's test host. Its own value and not inferred from the key, because a
	// deployment that thinks it is taking real transfers and is not would be discovered by the
	// seller who was never paid.
	Sandbox bool `yaml:"sandbox"`
}

type stripeSection struct {
	SecretKey string `yaml:"secret_key"`
	// WebhookSecret verifies the callback signature. Without it the webhook is an endpoint anybody
	// can use to mark an order paid.
	WebhookSecret  string   `yaml:"webhook_secret"`
	RequestTimeout Duration `yaml:"request_timeout"`
}

type transportSection struct {
	Providers []string `yaml:"providers" validate:"required,min=1,dive,oneof=mock"`
}

type websocketSection struct {
	// TicketTTL is how long a handshake ticket lives — short, because it only has to survive the
	// moment between minting it and opening the socket.
	TicketTTL Duration `yaml:"ticket_ttl" validate:"required"`
	// WriteTimeout bounds one write and one ping: a socket write that never completes must not hang
	// the pump forever. PingInterval is how often an idle socket is probed, so a peer that vanished
	// without a FIN is found by a failed pong instead of an accumulating goroutine.
	WriteTimeout Duration `yaml:"write_timeout" validate:"required"`
	PingInterval Duration `yaml:"ping_interval" validate:"required"`
	// SendBuffer is how many envelopes a socket may fall behind before the hub drops it;
	// MaxPerAccount caps concurrent sockets, so a tab-spammer cannot exhaust the file descriptors.
	SendBuffer    int `yaml:"send_buffer" validate:"required,gt=0"`
	MaxPerAccount int `yaml:"max_per_account" validate:"required,gt=0"`
	// AllowedOrigins is matched against the Origin header's *host*, not a full URL — that is what
	// coder/websocket's OriginPatterns compares against, unlike the gateway's CORS list.
	AllowedOrigins []string `yaml:"allowed_origins" validate:"required,min=1"`
}

// Load reads the document, validates it, and assembles what the program reads. Every failure comes
// back at once: a deployment missing four values learns about four, not one per restart.
func Load(v *validator.Validate) (*Config, error) {
	raw, source, err := read()
	if err != nil {
		return nil, err
	}
	var f file
	// KnownFields, so a key nobody reads is a startup failure rather than a setting somebody
	// believes is in effect. A typo in a document with no defaults is otherwise invisible.
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", source, err)
	}
	v.RegisterStructValidation(validatePaymentSection, paymentSection{})
	if err := v.Struct(f); err != nil {
		return nil, fmt.Errorf("validate %s: %w", source, err)
	}
	return f.config(), nil
}

// read answers the document and where it came from, which is what an error has to name.
func read() ([]byte, string, error) {
	path := os.Getenv(FileVar)
	if path == "" {
		path = DefaultPath
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, path, fmt.Errorf("read config %s (set %s to point elsewhere): %w", path, FileVar, err)
	}
	return raw, path, nil
}

// validatePaymentSection is `required_if` for a list. A tag can ask whether a *field* equals a
// value, not whether a slice contains one — and a rail registered without its credentials is one
// that fails at the till, so the check is written out here and reported like every other.
func validatePaymentSection(sl validator.StructLevel) {
	section := sl.Current().Interface().(paymentSection)

	if slices.Contains(section.Providers, "sepay") {
		required := map[string]string{
			"sepay.merchant_id":    section.SePay.MerchantID,
			"sepay.secret_key":     section.SePay.SecretKey,
			"sepay.ipn_secret_key": section.SePay.IPNSecretKey,
		}
		for path, value := range required {
			if strings.TrimSpace(value) == "" {
				sl.ReportError(value, path, "", "required_by_providers", "sepay")
			}
		}
	}
	if slices.Contains(section.Providers, "stripe") {
		if strings.TrimSpace(section.Stripe.SecretKey) == "" {
			sl.ReportError(section.Stripe.SecretKey, "stripe.secret_key", "", "required_by_providers", "stripe")
		}
		if strings.TrimSpace(section.Stripe.WebhookSecret) == "" {
			sl.ReportError(section.Stripe.WebhookSecret, "stripe.webhook_secret", "", "required_by_providers", "stripe")
		}
		if section.Stripe.RequestTimeout == 0 {
			sl.ReportError(section.Stripe.RequestTimeout, "stripe.request_timeout", "", "required_by_providers", "stripe")
		}
	}
}

// config flattens the document into what the program reads. The one place the two shapes meet.
func (f file) config() *Config {
	return &Config{
		GatewayAddr:        f.Gateway.Addr,
		PublicBaseURL:      f.Gateway.PublicBaseURL,
		CORSAllowedOrigins: f.Gateway.AllowedOrigins,
		LogLevel:           f.Gateway.LogLevel,
		InstanceID:         f.Security.InstanceID,
		JWTSecret:          f.Security.JWTSecret,
		IDCipherKey:        f.Security.IDCipherKey,

		AccountDBDSN:       f.Database.Account,
		CatalogDBDSN:       f.Database.Catalog,
		OrderDBDSN:         f.Database.Order,
		ChatDBDSN:          f.Database.Chat,
		TrustDBDSN:         f.Database.Trust,
		FinanceDBDSN:       f.Database.Finance,
		ObservabilityDBDSN: f.Database.Observability,
		NATSURL:            f.NATS.URL,
		RedisAddr:          f.Redis.Addr,
		RedisPassword:      f.Redis.Password,

		StorageProvider:       f.Storage.Provider,
		StorageRoot:           f.Storage.Root,
		StorageSecret:         f.Storage.Secret,
		StorageUploadTTL:      f.Storage.UploadTTL.std(),
		StorageDownloadTTL:    f.Storage.DownloadTTL.std(),
		StorageMaxUploadBytes: f.Storage.MaxUploadBytes,
		StorageMaxVideoBytes:  f.Storage.MaxVideoBytes,
		StorageAllowedMimes:   f.Storage.AllowedMimes,

		EmbeddingProvider:     f.Embedding.Provider,
		EmbeddingBaseURL:      f.Embedding.BaseURL,
		EmbeddingAPIKey:       f.Embedding.APIKey,
		EmbeddingTimeout:      f.Embedding.Timeout.std(),
		EmbeddingDimensions:   f.Embedding.Dimensions,
		EmbeddingInterval:     f.Embedding.Interval.std(),
		EmbeddingBatchSize:    f.Embedding.BatchSize,
		EmbeddingMaxTextChars: f.Embedding.MaxTextChars,

		LLMProvider:            f.LLM.Provider,
		LiteLLMBaseURL:         f.LLM.BaseURL,
		LiteLLMAPIKey:          f.LLM.APIKey,
		LiteLLMChatModel:       f.LLM.ChatModel,
		LiteLLMTranscribeModel: f.LLM.TranscribeModel,
		LiteLLMRequestTimeout:  f.LLM.RequestTimeout.std(),
		LiteLLMStreamTimeout:   f.LLM.StreamTimeout.std(),

		WorkflowRuntime:    f.Workflow.Runtime,
		RestateServeAddr:   f.Workflow.ServeAddr,
		RestateIngressURL:  f.Workflow.IngressURL,
		RestateSendTimeout: f.Workflow.SendTimeout.std(),
		SweepInterval:      f.Workflow.SweepInterval.std(),

		EmailProvider:    f.Email.Provider,
		SMTPHost:         f.Email.SMTPHost,
		SMTPPort:         f.Email.SMTPPort,
		SMTPUsername:     f.Email.SMTPUsername,
		SMTPPassword:     f.Email.SMTPPassword,
		SMTPFrom:         f.Email.From,
		SMTPTimeout:      f.Email.Timeout.std(),
		VerifyEmailURL:   f.Email.VerifyURL,
		ResetPasswordURL: f.Email.ResetPasswordURL,

		SMSProvider:         f.SMS.Provider,
		ESMSBaseURL:         f.SMS.ESMSBase,
		ESMSAPIKey:          f.SMS.APIKey,
		ESMSSecretKey:       f.SMS.SecretKey,
		ESMSBrandname:       f.SMS.Brandname,
		ESMSSMSType:         f.SMS.SMSType,
		ESMSContentTemplate: f.SMS.ContentTemplate,
		ESMSUnicode:         f.SMS.Unicode,
		ESMSSandbox:         f.SMS.Sandbox,
		ESMSTimeout:         f.SMS.Timeout.std(),

		OAuthVerifier:       f.OAuth.Verifier,
		OIDCGoogleAudiences: f.OAuth.GoogleAudiences,
		OIDCAppleAudiences:  f.OAuth.AppleAudiences,
		OIDCTimeout:         f.OAuth.Timeout.std(),

		KYCProvider:          f.KYC.Provider,
		FPTAIBaseURL:         f.KYC.BaseURL,
		FPTAIAPIKey:          f.KYC.APIKey,
		FPTAIRequestTimeout:  f.KYC.RequestTimeout.std(),
		FPTAIDownloadTimeout: f.KYC.DownloadTimeout.std(),

		PaymentProviders:      f.Payment.Providers,
		PaymentReturnURLHosts: f.Payment.ReturnURLHosts,
		SePayMerchantID:       f.Payment.SePay.MerchantID,
		SePaySecretKey:        f.Payment.SePay.SecretKey,
		SePayIPNSecretKey:     f.Payment.SePay.IPNSecretKey,
		SePaySandbox:          f.Payment.SePay.Sandbox,
		StripeSecretKey:       f.Payment.Stripe.SecretKey,
		StripeWebhookSecret:   f.Payment.Stripe.WebhookSecret,
		StripeRequestTimeout:  f.Payment.Stripe.RequestTimeout.std(),
		TransportProviders:    f.Transport.Providers,

		WSTicketTTL:      f.WebSocket.TicketTTL.std(),
		WSWriteTimeout:   f.WebSocket.WriteTimeout.std(),
		WSPingInterval:   f.WebSocket.PingInterval.std(),
		WSSendBuffer:     f.WebSocket.SendBuffer,
		WSMaxPerAccount:  f.WebSocket.MaxPerAccount,
		WSAllowedOrigins: f.WebSocket.AllowedOrigins,
	}
}
