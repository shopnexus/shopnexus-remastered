package commonconfig

import (
	"time"

	"shopnexus-server/config"
)

type Config struct {
	config.Shared `mapstructure:",squash"`

	Exchange  Exchange  `mapstructure:"exchange"`
	Filestore Filestore `mapstructure:"filestore"`
}

type Exchange struct {
	Base            string        `yaml:"base"                mapstructure:"base"                validate:"required"`
	Supported       []string      `yaml:"supported"           mapstructure:"supported"           validate:"required,min=1"`
	RefreshInterval time.Duration `yaml:"refreshInterval"     mapstructure:"refreshInterval"     validate:"gte=0"`
	HTTPTimeout     time.Duration `yaml:"httpTimeout"         mapstructure:"httpTimeout"         validate:"gte=0"`
	UpstreamURL     string        `yaml:"upstreamURL"         mapstructure:"upstreamURL"         validate:"required,url"`
	APIKey          string        `yaml:"apiKey"              mapstructure:"apiKey"              validate:"required"`
}

// Filestore + S3Filestore: only common needs them (object-store options, S3 uploads).
type Filestore struct {
	Type                string      `yaml:"type"                mapstructure:"type"                validate:"required,oneof=local s3"`
	PresignedDefaultTTL int64       `yaml:"presignedDefaultTTL" mapstructure:"presignedDefaultTTL" validate:"gte=1"`
	S3                  S3Filestore `yaml:"s3"                  mapstructure:"s3"`
	Placeholder404Url   string      `yaml:"placeholder404Url"   mapstructure:"placeholder404Url"   validate:"omitempty,url"`
}

type S3Filestore struct {
	AccessKeyID     string `yaml:"accessKeyID"     mapstructure:"accessKeyID"`
	SecretAccessKey string `yaml:"secretAccessKey" mapstructure:"secretAccessKey"`
	Region          string `yaml:"region"          mapstructure:"region"`
	Bucket          string `yaml:"bucket"          mapstructure:"bucket"`
	CloudfrontURL   string `yaml:"cloudfrontUrl"   mapstructure:"cloudfrontUrl"   validate:"omitempty"`
}

func NewConfig() (*Config, error) {
	var cfg Config
	return &cfg, config.LoadModule("common", &cfg)
}
