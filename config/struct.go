package config

import "time"

// Shared is the infra config block embedded (squashed) into every module's
// Config. Values stay duplicated per module yaml — each module owns its infra
// connections — but the struct and the fx providers over it (infras/fxinfra)
// are written once.
type Shared struct {
	Postgres  Postgres  `mapstructure:"postgres"`
	Redis     Redis     `mapstructure:"redis"`
	Log       Log       `mapstructure:"log"`
	Restate   Restate   `mapstructure:"restate"`
	Bus       Bus       `mapstructure:"bus"`
	RankedSet RankedSet `mapstructure:"rankedset"`
}

// SharedConfig returns itself. Embedding promotes it, so every module Config
// satisfies HasShared with no extra code. (Named SharedConfig because the
// embedded field is already named Shared.)
func (s Shared) SharedConfig() Shared { return s }

// HasShared is the constraint for generic infra providers (infras/fxinfra).
type HasShared interface{ SharedConfig() Shared }

// Postgres is duplicated into every module's Config. Each module then
// constructs its own connection pool from its own values — no shared root pool.
type Postgres struct {
	Url                     string        `yaml:"url"                     mapstructure:"url"`
	Host                    string        `yaml:"host"                    mapstructure:"host"                    validate:"required_without=Url"`
	Port                    int           `yaml:"port"                    mapstructure:"port"                    validate:"required_without=Url"`
	Username                string        `yaml:"username"                mapstructure:"username"                validate:"required_without=Url"`
	Password                string        `yaml:"password"                mapstructure:"password"                validate:"required_without=Url"`
	Database                string        `yaml:"database"                mapstructure:"database"                validate:"required_without=Url"`
	MaxConnections          int32         `yaml:"maxConnections"          mapstructure:"maxConnections"          validate:"gte=1"`
	MaxIdleConnections      int32         `yaml:"maxIdleConnections"      mapstructure:"maxIdleConnections"      validate:"gte=0"`
	MaxConnIdleTime         time.Duration `yaml:"maxConnIdleTime"         mapstructure:"maxConnIdleTime"         validate:"gte=0"`
	LogQuery                bool          `yaml:"logQuery"                mapstructure:"logQuery"`
	AllowNestedTransactions bool          `yaml:"allowNestedTransactions" mapstructure:"allowNestedTransactions"`
}

// Redis is duplicated into every module's Config; each module owns its rueidis.Client.
type Redis struct {
	Host     string `yaml:"host"     mapstructure:"host"     validate:"required"`
	Port     string `yaml:"port"     mapstructure:"port"     validate:"required"`
	Password string `yaml:"password" mapstructure:"password"`
	DB       int64  `yaml:"db"       mapstructure:"db"       validate:"gte=0"`
}

// Bus is duplicated into every module's Config; transport picks the event bus
// backing: "memory" shares the app-wide in-process transport, "redis" runs on
// Redis Streams over the module's own Redis connection.
type Bus struct {
	Transport string `yaml:"transport" mapstructure:"transport" validate:"required,oneof=memory redis"`
}

// RankedSet is duplicated into every module's Config; transport picks the
// ranked-set backing: "memory" is in-process (dev / single instance), "redis"
// runs on a sorted set over the module's own Redis connection.
type RankedSet struct {
	Transport string `yaml:"transport" mapstructure:"transport" validate:"required,oneof=memory redis"`
}

// Log is duplicated into every module's Config; each module owns its *slog.Logger.
// internal/app/config also has a Log block — that one is used for slog.SetDefault.
type Log struct {
	Level      string `yaml:"level"      mapstructure:"level"      validate:"required,oneof=debug info warn error"`
	Format     string `yaml:"format"     mapstructure:"format"     validate:"oneof=json text"`
	AddSource  bool   `yaml:"addSource"  mapstructure:"addSource"`
	TimeFormat string `yaml:"timeFormat" mapstructure:"timeFormat" validate:"required"`
}

// Restate is duplicated into every module's Config (modules use IngressAddress
// for their own proxy clients) and into internal/app/config (admin/serviceHost
// /servicePort for SetupRestate registration).
type Restate struct {
	IngressAddress string `yaml:"ingressAddress" mapstructure:"ingressAddress" validate:"required,url"`
	AdminAddress   string `yaml:"adminAddress"   mapstructure:"adminAddress"   validate:"required,url"`
	ServiceHost    string `yaml:"serviceHost"    mapstructure:"serviceHost"    validate:"required"`
	ServicePort    string `yaml:"servicePort"    mapstructure:"servicePort"    validate:"required"`
}
