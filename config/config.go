package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-playground/validator/v10"
	"github.com/spf13/viper"
)

// configDir holds the single YAML pair for the whole server, relative to the
// process working dir (repo root in dev, /app in the image). There is one
// Config and one file — no per-module config packages.
const configDir = "config"

// Config is the whole server's configuration. Process-level fields, the shared
// infra leaves (each module builds its OWN pool/redis/etc. from these — runtime
// ownership stays per-module, only the config source is shared), and every
// module's own section. Loaded once and provided as *Config across fx.
type Config struct {
	// process-level
	Port       string     `mapstructure:"port"       validate:"required"`
	Log        Log        `mapstructure:"log"`
	Restate    Restate    `mapstructure:"restate"`
	BestEffort BestEffort `mapstructure:"bestEffort"`

	// shared infra (single source; per-module pools built from these)
	Postgres  Postgres  `mapstructure:"postgres"`
	Redis     Redis     `mapstructure:"redis"`
	Bus       Bus       `mapstructure:"bus"`
	RankedSet RankedSet `mapstructure:"rankedset"`
	Public    Public    `mapstructure:"public"`

	// module sections
	JWT               JWT               `mapstructure:"jwt"`
	Exchange          Exchange          `mapstructure:"exchange"`
	Filestore         Filestore         `mapstructure:"filestore"`
	Search            Search            `mapstructure:"search"`
	LLM               LLM               `mapstructure:"llm"`
	PopularityWeights PopularityWeights `mapstructure:"popularityWeights"`
	Order             Order             `mapstructure:"order"`
	Vnpay             Vnpay             `mapstructure:"vnpay"`
	Sepay             Sepay             `mapstructure:"sepay"`
	CardPayment       CardPayment       `mapstructure:"cardPayment"`
	GHTK              GHTK              `mapstructure:"ghtk"`
	Mock              Mock              `mapstructure:"mock"`
}

// New reads config/config.default.yml + config.<env>.yml, applies APP_* env
// overrides (viper AutomaticEnv, "." -> "_", so APP_POSTGRES_HOST overrides
// postgres.host), decodes into Config, and validates it.
func New() (*Config, error) {
	v := viper.New()
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()
	v.SetEnvPrefix("APP")

	if err := loadDefaultConfig(v, configDir); err != nil {
		slog.Warn("Could not load default config",
			slog.String("dir", configDir), slog.Any("error", err))
	}
	if err := loadEnvConfig(v, configDir); err != nil {
		slog.Warn("Could not load env config",
			slog.String("dir", configDir), slog.Any("error", err))
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}
	if err := validator.New().Struct(&cfg); err != nil {
		if verr, ok := errors.AsType[validator.ValidationErrors](err); ok {
			msgs := make([]string, 0, len(verr))
			for _, e := range verr {
				msgs = append(msgs, e.Error())
			}
			return nil, fmt.Errorf("validate config: %s", strings.Join(msgs, "; "))
		}
		return nil, fmt.Errorf("validate config: %w", err)
	}
	return &cfg, nil
}

func loadDefaultConfig(v *viper.Viper, dir string) error {
	path := filepath.Join(dir, "config.default.yml")
	if !fileExists(path) {
		return fmt.Errorf("default config not found: %s", path)
	}
	v.SetConfigFile(path)
	if err := v.ReadInConfig(); err != nil {
		return err
	}
	slog.Info("Loaded default config", slog.String("path", path))
	return nil
}

func loadEnvConfig(v *viper.Viper, dir string) error {
	env := os.Getenv("APP_ENV")
	if env == "" {
		env = "development"
	}

	candidates := []string{
		filepath.Join(dir, fmt.Sprintf("config.%s.yml", env)),
	}
	if env == "development" {
		candidates = append(candidates, filepath.Join(dir, "config.dev.yml"))
	}
	if env == "production" {
		candidates = append(candidates, filepath.Join(dir, "config.prod.yml"))
	}

	var lastErr error
	for _, path := range candidates {
		if fileExists(path) {
			if err := mergeConfigFile(v, path); err == nil {
				slog.Info("Loaded config", slog.String("path", path))
				return nil
			} else {
				lastErr = err
			}
		}
	}
	if lastErr != nil {
		return fmt.Errorf("no valid config file found in %s, last error: %w", dir, lastErr)
	}
	return nil
}

func mergeConfigFile(v *viper.Viper, configFile string) error {
	v.SetConfigFile(configFile)
	if v.ConfigFileUsed() == "" {
		return v.ReadInConfig()
	}
	return v.MergeInConfig()
}

func fileExists(filename string) bool {
	info, err := os.Stat(filename)
	if os.IsNotExist(err) {
		return false
	}
	return !info.IsDir()
}
