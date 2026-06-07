package inventoryconfig

import "shopnexus-server/config"

type Config struct {
	config.Shared `mapstructure:",squash"`
}

func NewConfig() (*Config, error) {
	var cfg Config
	return &cfg, config.LoadModule("inventory", &cfg)
}
