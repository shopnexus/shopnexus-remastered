package config_test

import (
	"testing"

	"shopnexus/internal/config"
	"shopnexus/internal/shared/validation"
)

func setEnv(t *testing.T, kv map[string]string) {
	t.Helper()
	for k, v := range kv {
		t.Setenv(k, v)
	}
}

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
	}
}

func TestLoad_MissingAnyRequiredFails(t *testing.T) {
	// Drop one var at a time -> each must fail (every var is required, no default).
	for _, omit := range []string{"GATEWAY_ADDR", "INSTANCE_ID", "ACCOUNT_DB_DSN", "CATALOG_DB_DSN", "ORDER_DB_DSN", "CHAT_DB_DSN", "COMMON_DB_DSN", "TRUST_DB_DSN", "FINANCE_DB_DSN", "OBSERVABILITY_DB_DSN", "NATS_URL", "REDIS_ADDR", "REDIS_PASSWORD", "JWT_SECRET", "ID_CIPHER_KEY", "LOG_LEVEL"} {
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
