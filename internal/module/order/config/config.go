package orderconfig

import "shopnexus-server/config"

type Config struct {
	Postgres  config.Postgres  `mapstructure:"postgres"`
	Redis     config.Redis     `mapstructure:"redis"`
	Log       config.Log       `mapstructure:"log"`
	Restate   config.Restate   `mapstructure:"restate"`
	Bus       config.Bus       `mapstructure:"bus"`
	RankedSet config.RankedSet `mapstructure:"rankedset"`

	Order       Order       `mapstructure:"order"`
	Vnpay       Vnpay       `mapstructure:"vnpay"`
	Sepay       Sepay       `mapstructure:"sepay"`
	CardPayment CardPayment `mapstructure:"cardPayment"`
	GHTK        GHTK        `mapstructure:"ghtk"`
	Mock        Mock        `mapstructure:"mock"`
}

type Order struct {
	PaymentExpiryDays int64  `yaml:"paymentExpiryDays" mapstructure:"paymentExpiryDays" validate:"required,gte=1"`
	ReturnURL         string `yaml:"returnUrl"         mapstructure:"returnUrl"         validate:"required,url"`
}

type Vnpay struct {
	TmnCode    string `yaml:"tmnCode"    mapstructure:"tmnCode"    validate:"required"`
	HashSecret string `yaml:"hashSecret" mapstructure:"hashSecret" validate:"required"`
}

type Sepay struct {
	MerchantID    string `yaml:"merchantId"    mapstructure:"merchantId"`
	SecretKey     string `yaml:"secretKey"     mapstructure:"secretKey"`
	IPNSecretKey  string `yaml:"ipnSecretKey"  mapstructure:"ipnSecretKey"`
	PublicBaseURL string `yaml:"publicBaseUrl" mapstructure:"publicBaseUrl"`
	Sandbox       bool   `yaml:"sandbox"       mapstructure:"sandbox"`
}

type CardPayment struct {
	Provider  string `yaml:"provider"  mapstructure:"provider"`
	SecretKey string `yaml:"secretKey" mapstructure:"secretKey"`
	PublicKey string `yaml:"publicKey" mapstructure:"publicKey"`
}

type GHTK struct {
	BaseURL  string `yaml:"baseURL"  mapstructure:"baseURL"`
	APIKey   string `yaml:"apiKey"   mapstructure:"apiKey"`
	ClientID string `yaml:"clientID" mapstructure:"clientID"`
	Secret   string `yaml:"secret"   mapstructure:"secret"`
}

// Mock is a dev-only transport provider that auto-delivers shipments after a
// fixed delay. Leave Enabled=false outside dev.
type Mock struct {
	Enabled      bool `yaml:"enabled"      mapstructure:"enabled"`
	DelaySeconds int  `yaml:"delaySeconds" mapstructure:"delaySeconds"`
}

func NewConfig() (*Config, error) {
	var cfg Config
	return &cfg, config.LoadModule("order", &cfg)
}
