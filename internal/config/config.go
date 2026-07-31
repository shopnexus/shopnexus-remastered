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

		WorkflowRuntime:    os.Getenv("WORKFLOW_RUNTIME"),
		RestateServeAddr:   os.Getenv("RESTATE_SERVE_ADDR"),
		RestateIngressURL:  os.Getenv("RESTATE_INGRESS_URL"),
		RestateSendTimeout: p.durationVar("RESTATE_SEND_TIMEOUT"),
		SweepInterval:      p.durationVar("SWEEP_INTERVAL"),

		EmailProvider:   os.Getenv("EMAIL_PROVIDER"),
		SMSProvider:     os.Getenv("SMS_PROVIDER"),
		OAuthVerifier:   os.Getenv("OAUTH_VERIFIER"),
		KYCProvider:     os.Getenv("KYC_PROVIDER"),
		PaymentProvider: os.Getenv("PAYMENT_PROVIDER"),

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
