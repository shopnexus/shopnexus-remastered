package config

type Config struct {
	// General configuration
	Env string `yaml:"env" mapstructure:"env" validate:"required,oneof=dev staging production"`
	Log Log    `yaml:"log" mapstructure:"log" validate:"required"`
	App App    `yaml:"app" mapstructure:"app" validate:"required"`

	// Infrastructure components
	Postgres Postgres `yaml:"postgres" mapstructure:"postgres" validate:"required"`
	Redis    Redis    `yaml:"redis" mapstructure:"redis" validate:"required"`
}

type App struct {
	Name string `yaml:"name" mapstructure:"name" validate:"required"`
	JWT  JWT    `yaml:"jwt" mapstructure:"jwt" validate:"required"`
}

type JWT struct {
	Secret              string `yaml:"secret" mapstructure:"secret" validate:"required"`
	AccessTokenDuration int64  `yaml:"accessTokenDuration" mapstructure:"accessTokenDuration" validate:"required,gte=1"`
}

type Log struct {
	Level           string `yaml:"level" mapstructure:"level" validate:"oneof=debug info warn error dpanic panic fatal"`
	StacktraceLevel string `yaml:"stacktraceLevel" mapstructure:"stacktraceLevel" validate:"oneof=debug info warn error dpanic panic fatal"`
	FileEnabled     bool   `yaml:"fileEnabled" mapstructure:"fileEnabled"`
	FileSize        int    `yaml:"fileSize" mapstructure:"fileSize" validate:"gte=1"`
	FilePath        string `yaml:"filePath" mapstructure:"filePath" validate:"required_if=FileEnabled true"`
	FileCompress    bool   `yaml:"fileCompress" mapstructure:"fileCompress"`
	MaxAge          int    `yaml:"maxAge" mapstructure:"maxAge" validate:"gte=0"`
	MaxBackups      int    `yaml:"maxBackups" mapstructure:"maxBackups" validate:"gte=0"`
}

type Postgres struct {
	Url                string `yaml:"url" mapstructure:"url"`
	Host               string `yaml:"host" mapstructure:"host" validate:"required_without=Url"`
	Port               int    `yaml:"port" mapstructure:"port" validate:"required_without=Url"`
	Username           string `yaml:"username" mapstructure:"username" validate:"required_without=Url"`
	Password           string `yaml:"password" mapstructure:"password" validate:"required_without=Url"`
	Database           string `yaml:"database" mapstructure:"database" validate:"required_without=Url"`
	MaxConnections     int32  `yaml:"maxConnections" mapstructure:"maxConnections" validate:"gte=1"`
	MaxIdleConnections int32  `yaml:"maxIdleConnections" mapstructure:"maxIdleConnections" validate:"gte=0"`
	MaxConnIdleTime    int32  `yaml:"maxConnIdleTime" mapstructure:"maxConnIdleTime" validate:"gte=0"`
	LogQuery           bool   `yaml:"logQuery" mapstructure:"logQuery"`
}

type Redis struct {
	Host     string `yaml:"host" mapstructure:"host" validate:"required"`
	Port     string `yaml:"port" mapstructure:"port" validate:"required"`
	Password string `yaml:"password" mapstructure:"password"`
	DB       int    `yaml:"db" mapstructure:"db" validate:"gte=0"`
}
