package config

type Config struct {
	Env         string      `yaml:"env" mapstructure:"env" validate:"required,oneof=dev staging production"`
	App         App         `yaml:"app" mapstructure:"app" validate:"required"`
	TelegramBot TelegramBot `yaml:"telegramBot" mapstructure:"telegramBot" validate:"required"`
	MediaSaver  MediaSaver  `yaml:"mediaSaver" mapstructure:"mediaSaver" validate:"required"`
	Log         Log         `yaml:"log" mapstructure:"log" validate:"required"`
	Postgres    Postgres    `yaml:"postgres" mapstructure:"postgres" validate:"required"`
	Redis       Redis       `yaml:"redis" mapstructure:"redis" validate:"required"`
	BrowserPool BrowserPool `yaml:"browserpool" mapstructure:"browserpool" validate:"required"`
}

type App struct {
	Name string `yaml:"name" mapstructure:"name" validate:"required"`
}

type TelegramBot struct {
	Token    string           `yaml:"token" mapstructure:"token" validate:"required"`
	LogDebug bool             `yaml:"logDebug" mapstructure:"logDebug"`
	Proxy    TelegramBotProxy `yaml:"proxy" mapstructure:"proxy"`
}

type TelegramBotProxy struct {
	Enabled  bool   `yaml:"enabled" mapstructure:"enabled"`
	Type     string `yaml:"type" mapstructure:"type" validate:"omitempty,oneof=socks5"`
	Address  string `yaml:"address" mapstructure:"address"`
	Port     int    `yaml:"port" mapstructure:"port" validate:"gte=0,lte=65535"`
	Username string `yaml:"username" mapstructure:"username"`
	Password string `yaml:"password" mapstructure:"password"`
}

type MediaSaver struct {
	UseRandomUA       bool     `yaml:"useRandomUA" mapstructure:"useRandomUA"`
	UserAgents        []string `yaml:"userAgents" mapstructure:"userAgents"`
	Quality           string   `yaml:"quality" mapstructure:"quality" validate:"oneof=low high"`
	RetryCount        int      `yaml:"retryCount" mapstructure:"retryCount" validate:"gte=0"`
	Timeout           int      `yaml:"timeout" mapstructure:"timeout" validate:"gt=0"`
	MaxGroupMediaSize int64    `yaml:"maxGroupMediaSize" mapstructure:"maxGroupMediaSize" validate:"gt=0"`
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

type BrowserPool struct {
	Headless      bool     `yaml:"headless" mapstructure:"headless"`
	PoolSize      int      `yaml:"poolSize" mapstructure:"poolSize" validate:"gte=1"`
	Proxies       []string `yaml:"proxies" mapstructure:"proxies"`
	TaskQueueSize int      `yaml:"taskQueueSize" mapstructure:"taskQueueSize" validate:"gte=1"`
}
