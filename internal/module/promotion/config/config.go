package promotionconfig

import "shopnexus-server/config"

type Config struct {
	Postgres  config.Postgres  `mapstructure:"postgres"`
	Redis     config.Redis     `mapstructure:"redis"`
	Log       config.Log       `mapstructure:"log"`
	Restate   config.Restate   `mapstructure:"restate"`
	Bus       config.Bus       `mapstructure:"bus"`
	RankedSet config.RankedSet `mapstructure:"rankedset"`
}

func NewConfig() (*Config, error) {
	var cfg Config
	return &cfg, config.LoadModule("promotion", &cfg)
}
