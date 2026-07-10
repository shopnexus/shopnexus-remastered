package accountconfig

import "shopnexus-server/config"

type Config struct {
	Postgres  config.Postgres  `mapstructure:"postgres"`
	Redis     config.Redis     `mapstructure:"redis"`
	Log       config.Log       `mapstructure:"log"`
	Restate   config.Restate   `mapstructure:"restate"`
	Bus       config.Bus       `mapstructure:"bus"`
	RankedSet config.RankedSet `mapstructure:"rankedset"`

	JWT JWT `mapstructure:"jwt"`
}

type JWT struct {
	Secret               string `yaml:"secret"               mapstructure:"secret"               validate:"required"`
	AccessTokenDuration  int64  `yaml:"accessTokenDuration"  mapstructure:"accessTokenDuration"  validate:"required,gte=1"`
	RefreshTokenDuration int64  `yaml:"refreshTokenDuration" mapstructure:"refreshTokenDuration" validate:"required,gte=1"`
	RefreshSecret        string `yaml:"refreshSecret"        mapstructure:"refreshSecret"`
}

func NewConfig() (*Config, error) {
	var cfg Config
	return &cfg, config.LoadModule("account", &cfg)
}
