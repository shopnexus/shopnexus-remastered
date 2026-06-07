package promotionconfig

import "shopnexus-server/config"

type Config struct {
	config.Shared `mapstructure:",squash"`
}

func NewConfig() (*Config, error) {
	var cfg Config
	return &cfg, config.LoadModule("promotion", &cfg)
}
