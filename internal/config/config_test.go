package config_test

import (
	"strings"
	"testing"
	"time"

	"shopnexus/internal/config"
	"shopnexus/internal/shared/validation"
)

func setEnv(t *testing.T, kv map[string]string) {
	t.Helper()
	for k, v := range kv {
		t.Setenv(k, v)
	}
}

// fullEnv is the smallest environment that starts the gateway: every unconditional var,
// and the provider seams on their mock implementations.
func fullEnv() map[string]string {
	return map[string]string{
		"GATEWAY_ADDR":         "0.0.0.0:8080",
		"INSTANCE_ID":          "gateway-test",
		"ACCOUNT_DB_DSN":       "postgres://x",
		"CATALOG_DB_DSN":       "postgres://y",
		"ORDER_DB_DSN":         "postgres://o",
		"CHAT_DB_DSN":          "postgres://c",
		"COMMON_DB_DSN":        "postgres://cm",
		"TRUST_DB_DSN":         "postgres://tr",
		"FINANCE_DB_DSN":       "postgres://fin",
		"OBSERVABILITY_DB_DSN": "postgres://o2",
		"NATS_URL":             "nats://localhost:4222",
		"REDIS_ADDR":           "localhost:6379",
		"REDIS_PASSWORD":       "app",
		"JWT_SECRET":           "0123456789012345678901234567890123",
		"ID_CIPHER_KEY":        "0123456789abcdef0123456789abcdef",
		"LOG_LEVEL":            "info",
		"EMAIL_PROVIDER":       "mock",
		"SMS_PROVIDER":         "mock",
		"OAUTH_VERIFIER":       "mock",
		"KYC_PROVIDER":         "mock",
	}
}

// requiredVars is every var that is required whatever the deployment does.
var requiredVars = []string{
	"GATEWAY_ADDR", "INSTANCE_ID", "ACCOUNT_DB_DSN", "CATALOG_DB_DSN", "ORDER_DB_DSN",
	"CHAT_DB_DSN", "COMMON_DB_DSN", "TRUST_DB_DSN", "FINANCE_DB_DSN", "OBSERVABILITY_DB_DSN",
	"NATS_URL", "REDIS_ADDR", "REDIS_PASSWORD", "JWT_SECRET", "ID_CIPHER_KEY", "LOG_LEVEL",
	"EMAIL_PROVIDER", "SMS_PROVIDER", "OAUTH_VERIFIER", "KYC_PROVIDER",
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
