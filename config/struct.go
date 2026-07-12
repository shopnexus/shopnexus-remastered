package config

import "time"

// The leaf infra config types below are declared once here and referenced as
// explicit fields by each module's own Config. Values stay duplicated per
// module yaml — each module owns its infra connections. The plain provider
// funcs over these leaves live in internal/infras/infra.

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

// Public is the app's single public-facing origin (the storefront URL as users
// reach it). Downstream public URLs — payment return, SePay callback base — are
// DERIVED from this in code, so a domain change is one value in one place.
// Wired from the deploy ConfigMap's SITE_URL via APP_PUBLIC_SITEURL.
type Public struct {
	SiteURL string `yaml:"siteUrl" mapstructure:"siteUrl" validate:"required,url"`
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

	// BestEffortAddress is the in-process BestEffort HTTP/2 endpoint. Used only
	// by modules that fall back to the Remote client to avoid an fx cycle.
	BestEffortAddress string `yaml:"bestEffortAddress" mapstructure:"bestEffortAddress"`
}
