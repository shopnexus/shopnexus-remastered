package config_test

import (
	"strings"
	"testing"
	"time"

	"shopnexus/internal/config"
	"shopnexus/internal/shared/validation"
)

// setEnv installs exactly kv and blanks every other var Load reads. Without the blanking, a var
// left in the developer's shell — RESTATE_INGRESS_URL, say — makes a "selector chosen without its
// credentials" case pass, which is the one thing these tests exist to catch.
func setEnv(t *testing.T, kv map[string]string) {
	t.Helper()
	for _, name := range readVars {
		if _, ok := kv[name]; !ok {
			t.Setenv(name, "")
		}
	}
	for k, v := range kv {
		t.Setenv(k, v)
	}
}

// readVars is every variable config.Load looks at.
var readVars = []string{
	"ACCOUNT_DB_DSN",
	"CATALOG_DB_DSN",
	"CHAT_DB_DSN",
	"EMAIL_PROVIDER",
	"ESMS_API_KEY",
	"ESMS_BASE_URL",
	"ESMS_BRANDNAME",
	"ESMS_CONTENT_TEMPLATE",
	"ESMS_SANDBOX",
	"ESMS_SECRET_KEY",
	"ESMS_SMS_TYPE",
	"ESMS_TIMEOUT",
	"ESMS_UNICODE",
	"FINANCE_DB_DSN",
	"FPT_AI_API_KEY",
	"FPT_AI_BASE_URL",
	"FPT_AI_DOWNLOAD_TIMEOUT",
	"FPT_AI_REQUEST_TIMEOUT",
	"GATEWAY_ADDR",
	"ID_CIPHER_KEY",
	"INSTANCE_ID",
	"JWT_SECRET",
	"KYC_PROVIDER",
	"LOG_LEVEL",
	"NATS_URL",
	"OAUTH_VERIFIER",
	"OBSERVABILITY_DB_DSN",
	"OIDC_TIMEOUT",
	"ORDER_DB_DSN",
	"PAYMENT_PROVIDER",
	"REDIS_ADDR",
	"REDIS_PASSWORD",
	"RESET_PASSWORD_URL",
	"RESTATE_INGRESS_URL",
	"RESTATE_SEND_TIMEOUT",
	"RESTATE_SERVE_ADDR",
	"EMBEDDING_BASE_URL",
	"EMBEDDING_BATCH_SIZE",
	"EMBEDDING_DIMENSIONS",
	"EMBEDDING_INTERVAL",
	"EMBEDDING_MAX_TEXT_CHARS",
	"EMBEDDING_PROVIDER",
	"EMBEDDING_TIMEOUT",
	"SMS_PROVIDER",
	"SMTP_FROM",
	"SMTP_HOST",
	"SMTP_PASSWORD",
	"SMTP_PORT",
	"SMTP_TIMEOUT",
	"SMTP_USERNAME",
	"STORAGE_BASE_URL",
	"STORAGE_DOWNLOAD_TTL",
	"STORAGE_MAX_UPLOAD_BYTES",
	"STORAGE_PROVIDER",
	"STORAGE_ROOT",
	"STORAGE_SECRET",
	"STORAGE_UPLOAD_TTL",
	"SWEEP_INTERVAL",
	"TRANSPORT_PROVIDER",
	"TRUST_DB_DSN",
	"VERIFY_EMAIL_URL",
	"WORKFLOW_RUNTIME",
	"WS_TICKET_TTL",
	"WS_WRITE_TIMEOUT",
	"WS_PING_INTERVAL",
	"WS_SEND_BUFFER",
	"WS_MAX_PER_ACCOUNT",
	"WS_ALLOWED_ORIGINS",
}

// fullEnv is the smallest environment that starts the gateway: every unconditional var,
// and the provider seams on their mock implementations.
func fullEnv() map[string]string {
	return map[string]string{
		"GATEWAY_ADDR":             "0.0.0.0:8080",
		"INSTANCE_ID":              "gateway-test",
		"ACCOUNT_DB_DSN":           "postgres://x",
		"CATALOG_DB_DSN":           "postgres://y",
		"ORDER_DB_DSN":             "postgres://o",
		"CHAT_DB_DSN":              "postgres://c",
		"TRUST_DB_DSN":             "postgres://tr",
		"FINANCE_DB_DSN":           "postgres://fin",
		"OBSERVABILITY_DB_DSN":     "postgres://o2",
		"NATS_URL":                 "nats://localhost:4222",
		"REDIS_ADDR":               "localhost:6379",
		"REDIS_PASSWORD":           "app",
		"JWT_SECRET":               "0123456789012345678901234567890123",
		"ID_CIPHER_KEY":            "0123456789abcdef0123456789abcdef",
		"LOG_LEVEL":                "info",
		"EMAIL_PROVIDER":           "mock",
		"SMS_PROVIDER":             "mock",
		"OAUTH_VERIFIER":           "mock",
		"KYC_PROVIDER":             "mock",
		"PAYMENT_PROVIDER":         "mock",
		"TRANSPORT_PROVIDER":       "mock",
		"PAYMENT_RETURN_URL_HOSTS": "localhost:5000",
		// No durable runtime in the base env: the sweep is the clock, which is the shape a
		// local stack runs in.
		"STORAGE_PROVIDER":         "local",
		"STORAGE_ROOT":             "/tmp/shopnexus-objects",
		"STORAGE_BASE_URL":         "http://localhost:5000/api/v1",
		"STORAGE_SECRET":           "0123456789abcdef0123456789abcdef",
		"STORAGE_UPLOAD_TTL":       "15m",
		"STORAGE_DOWNLOAD_TTL":     "1h",
		"STORAGE_MAX_UPLOAD_BYTES": "10485760",
		"STORAGE_ALLOWED_MIMES":    "image/jpeg,image/png",

		// The mock model, for the same reason as the other seams: the base env must not need
		// a multi-gigabyte download to be valid.
		"EMBEDDING_PROVIDER":       "mock",
		"EMBEDDING_DIMENSIONS":     "1024",
		"EMBEDDING_INTERVAL":       "1m",
		"EMBEDDING_BATCH_SIZE":     "32",
		"EMBEDDING_MAX_TEXT_CHARS": "2000",

		"WORKFLOW_RUNTIME": "off",
		"SWEEP_INTERVAL":   "1m",

		"WS_TICKET_TTL":      "30s",
		"WS_WRITE_TIMEOUT":   "10s",
		"WS_PING_INTERVAL":   "30s",
		"WS_SEND_BUFFER":     "64",
		"WS_MAX_PER_ACCOUNT": "5",
		"WS_ALLOWED_ORIGINS": "localhost:3000",
	}
}

// requiredVars is every var that is required whatever the deployment does.
var requiredVars = []string{
	"GATEWAY_ADDR", "INSTANCE_ID", "ACCOUNT_DB_DSN", "CATALOG_DB_DSN", "ORDER_DB_DSN",
	"CHAT_DB_DSN", "TRUST_DB_DSN", "FINANCE_DB_DSN", "OBSERVABILITY_DB_DSN",
	"NATS_URL", "REDIS_ADDR", "REDIS_PASSWORD", "JWT_SECRET", "ID_CIPHER_KEY", "LOG_LEVEL",
	"EMAIL_PROVIDER", "SMS_PROVIDER", "OAUTH_VERIFIER", "KYC_PROVIDER",
	"WORKFLOW_RUNTIME", "SWEEP_INTERVAL", "PAYMENT_RETURN_URL_HOSTS", "TRANSPORT_PROVIDER",
	"STORAGE_PROVIDER", "STORAGE_UPLOAD_TTL", "STORAGE_DOWNLOAD_TTL",
	"STORAGE_MAX_UPLOAD_BYTES", "STORAGE_ALLOWED_MIMES",
	"EMBEDDING_PROVIDER", "EMBEDDING_DIMENSIONS", "EMBEDDING_INTERVAL",
	"EMBEDDING_BATCH_SIZE", "EMBEDDING_MAX_TEXT_CHARS",
	"WS_TICKET_TTL", "WS_WRITE_TIMEOUT", "WS_PING_INTERVAL",
	"WS_SEND_BUFFER", "WS_MAX_PER_ACCOUNT", "WS_ALLOWED_ORIGINS",
}

func TestLoad_MissingAnyRequiredFails(t *testing.T) {
	// Drop one var at a time -> each must fail (every var is required, no default).
	for _, omit := range requiredVars {
		env := fullEnv()
		delete(env, omit)
		setEnv(t, env)
		t.Setenv(omit, "") // ensure empty
		if _, err := config.Load(validation.Default()); err == nil {
			t.Fatalf("expected error when %s is missing", omit)
		}
	}
}

func TestLoad_Valid(t *testing.T) {
	setEnv(t, fullEnv())
	cfg, err := config.Load(validation.Default())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.GatewayAddr != "0.0.0.0:8080" || cfg.LogLevel != "info" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

// An unknown selector fails at startup rather than falling back to a mock: a deployment
// that thinks it is sending real email and is not would only be noticed by a user who
// never got their reset link.
func TestLoad_UnknownProviderSelectorFails(t *testing.T) {
	for _, name := range []string{"EMAIL_PROVIDER", "SMS_PROVIDER", "OAUTH_VERIFIER", "KYC_PROVIDER"} {
		setEnv(t, fullEnv())
		t.Setenv(name, "sendgrid")
		if _, err := config.Load(validation.Default()); err == nil {
			t.Fatalf("expected error for an unknown %s", name)
		}
	}
}

// A vendor's credentials are required only when that vendor is selected. This is the one
// conditional in the config, and it is still not a default: choosing "smtp" and leaving
// the host empty has to fail.
func TestLoad_VendorVarsRequiredOnlyWhenSelected(t *testing.T) {
	t.Run("smtp selected without its vars fails", func(t *testing.T) {
		env := fullEnv()
		env["EMAIL_PROVIDER"] = "smtp"
		setEnv(t, env)
		if _, err := config.Load(validation.Default()); err == nil {
			t.Fatal("expected error when EMAIL_PROVIDER=smtp and the SMTP vars are empty")
		}
	})

	t.Run("smtp selected with its vars loads", func(t *testing.T) {
		env := fullEnv()
		env["EMAIL_PROVIDER"] = "smtp"
		env["SMTP_HOST"] = "smtp.example.com"
		env["SMTP_PORT"] = "587"
		env["SMTP_USERNAME"] = "apikey"
		env["SMTP_PASSWORD"] = "secret"
		env["SMTP_FROM"] = "ShopNexus <no-reply@example.com>"
		env["SMTP_TIMEOUT"] = "10s"
		env["VERIFY_EMAIL_URL"] = "https://shopnexus.vn/verify-email"
		env["RESET_PASSWORD_URL"] = "https://shopnexus.vn/reset-password"
		setEnv(t, env)

		cfg, err := config.Load(validation.Default())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.SMTPPort != 587 || cfg.SMTPTimeout != 10*time.Second {
			t.Fatalf("typed vars not parsed: port=%d timeout=%v", cfg.SMTPPort, cfg.SMTPTimeout)
		}
	})

	t.Run("mock selected leaves the vendor vars alone", func(t *testing.T) {
		setEnv(t, fullEnv())
		if _, err := config.Load(validation.Default()); err != nil {
			t.Fatalf("mock providers must not need vendor credentials: %v", err)
		}
	})

	// The durable runtime is the same shape of selector, and the reason it matters is the same:
	// a deployment that thinks it has durable timers and has no ingress URL would discover it
	// when a seller was never paid.
	t.Run("restate selected without its vars fails", func(t *testing.T) {
		env := fullEnv()
		env["WORKFLOW_RUNTIME"] = "restate"
		setEnv(t, env)
		if _, err := config.Load(validation.Default()); err == nil {
			t.Fatal("expected error when WORKFLOW_RUNTIME=restate and the Restate vars are empty")
		}
	})

	t.Run("restate selected with its vars loads", func(t *testing.T) {
		env := fullEnv()
		env["WORKFLOW_RUNTIME"] = "restate"
		env["RESTATE_SERVE_ADDR"] = "0.0.0.0:9080"
		env["RESTATE_INGRESS_URL"] = "http://restate:8080"
		env["RESTATE_SEND_TIMEOUT"] = "5s"
		setEnv(t, env)

		cfg, err := config.Load(validation.Default())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.RestateSendTimeout != 5*time.Second || cfg.RestateIngressURL != "http://restate:8080" {
			t.Fatalf("restate vars not parsed: %+v", cfg)
		}
	})
}

// A malformed typed var names itself in the error, so a bad duration does not come back as
// "field is required" and send someone looking in the wrong place.
func TestLoad_MalformedTypedVarNamesItself(t *testing.T) {
	env := fullEnv()
	env["SMTP_TIMEOUT"] = "ten seconds"
	setEnv(t, env)

	_, err := config.Load(validation.Default())
	if err == nil {
		t.Fatal("expected an error for a malformed duration")
	}
	if !strings.Contains(err.Error(), "SMTP_TIMEOUT") {
		t.Fatalf("error does not name the variable: %v", err)
	}
}

// A comma-separated list drops blanks, so a trailing comma is not an empty audience that
// would accept a token issued to anybody.
func TestLoad_AudienceListParsing(t *testing.T) {
	env := fullEnv()
	env["OIDC_GOOGLE_AUDIENCES"] = " one.apps.googleusercontent.com , two.apps.googleusercontent.com ,"
	setEnv(t, env)

	cfg, err := config.Load(validation.Default())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.OIDCGoogleAudiences) != 2 || cfg.OIDCGoogleAudiences[0] != "one.apps.googleusercontent.com" {
		t.Fatalf("audiences = %#v", cfg.OIDCGoogleAudiences)
	}
}
