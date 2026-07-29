// Package config loads configuration from env and validates it, failing fast at startup.
package config

import (
	"fmt"
	"os"

	"github.com/go-playground/validator/v10"
)

// Every field is required — no defaults, no fallback. A missing var fails fast.
//
// Each module keeps its own DSN so it can later be split onto a separate
// Postgres; today they all point at the same URL and isolate their tables by
// schema (search_path, set per pool — see module fx.go / cmd/migrate).
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
	CommonDBDSN  string `validate:"required"`
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
}

func Load(v *validator.Validate) (*Config, error) {
	cfg := &Config{
		GatewayAddr:        os.Getenv("GATEWAY_ADDR"),
		InstanceID:         os.Getenv("INSTANCE_ID"),
		AccountDBDSN:       os.Getenv("ACCOUNT_DB_DSN"),
		CatalogDBDSN:       os.Getenv("CATALOG_DB_DSN"),
		OrderDBDSN:         os.Getenv("ORDER_DB_DSN"),
		ChatDBDSN:          os.Getenv("CHAT_DB_DSN"),
		CommonDBDSN:        os.Getenv("COMMON_DB_DSN"),
		TrustDBDSN:         os.Getenv("TRUST_DB_DSN"),
		FinanceDBDSN:       os.Getenv("FINANCE_DB_DSN"),
		ObservabilityDBDSN: os.Getenv("OBSERVABILITY_DB_DSN"),
		NATSURL:            os.Getenv("NATS_URL"),
		RedisAddr:          os.Getenv("REDIS_ADDR"),
		RedisPassword:      os.Getenv("REDIS_PASSWORD"),
		JWTSecret:          os.Getenv("JWT_SECRET"),
		IDCipherKey:        os.Getenv("ID_CIPHER_KEY"),
		LogLevel:           os.Getenv("LOG_LEVEL"),
	}
	if err := v.Struct(cfg); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}
	return cfg, nil
}
