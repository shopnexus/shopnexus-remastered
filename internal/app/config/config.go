package appconfig

import "shopnexus-server/config"

// Config is what internal/app/ itself reads. Modules each have their own
// pool/redis/restate-proxy so app no longer carries shared infra; what
// remains is process-level: the HTTP server port, slog.Default setup, and
// Restate admin/service registration.
type Config struct {
	Port       string         `mapstructure:"port"    validate:"required"`
	Log        config.Log     `mapstructure:"log"`
	Restate    config.Restate `mapstructure:"restate"`
	BestEffort BestEffort     `mapstructure:"bestEffort"`
}

// BestEffort configures the in-process BestEffort HTTP/2 endpoint.
type BestEffort struct {
	Port string `mapstructure:"port" validate:"required"`
}

func NewConfig() (*Config, error) {
	var cfg Config
	return &cfg, config.LoadDir("internal/app/config", &cfg)
}
