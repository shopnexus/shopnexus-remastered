package config

import (
	"fmt"
	"log"
	"os"
	"strings"
	"sync"

	"github.com/go-playground/validator/v10"
	"github.com/spf13/viper"
)

var (
	config   *Config
	once     sync.Once
	validate *validator.Validate
)

// GetConfig returns the global config instance, initializing it if necessary
func GetConfig() *Config {
	once.Do(func() {
		validate = validator.New()
		config = loadConfig()
	})
	return config
}

// ReloadConfig forces a reload of the configuration (useful for testing)
func ReloadConfig() *Config {
	if validate == nil {
		validate = validator.New()
	}
	config = loadConfig()
	return config
}

// loadConfig loads configuration from various sources
func loadConfig() *Config {
	v := viper.New()

	// Set up environment variable handling
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()
	v.SetEnvPrefix("APP") // All env vars should start with APP_

	// Load default configuration first
	if err := loadDefaultConfig(v); err != nil {
		log.Printf("Warning: Could not load default config: %v", err)
	}

	// Load main config files
	if err := loadConfigFile(v); err != nil {
		log.Printf("Warning: Could not load config file: %v", err)
	}

	// Unmarshal into struct
	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		log.Fatalf("Unable to decode config into struct: %v", err)
	}

	// Validate configuration
	if err := validateConfig(&cfg); err != nil {
		log.Fatalf("Configuration validation failed: %v", err)
	}

	return &cfg
}

// loadDefaultConfig loads the default configuration file
func loadDefaultConfig(v *viper.Viper) error {
	defaultPaths := []string{
		"config/config.default.yml",
	}

	for _, path := range defaultPaths {
		if fileExists(path) {
			// Use MergeInConfig to allow subsequent configs to override these defaults
			v.SetConfigFile(path)
			if err := v.ReadInConfig(); err == nil {
				log.Printf("Loaded default config from: %s", path)
				return nil
			}
		}
	}

	return fmt.Errorf("no default config file found")
}

// loadConfigFile tries to load a config file from various sources
func loadConfigFile(v *viper.Viper) error {
	// 1. Check if CONFIG_FILE environment variable is set (for Jenkins credentials)
	if configFile := os.Getenv("CONFIG_FILE"); configFile != "" {
		return mergeConfigFile(v, configFile)
	}

	// 2. Check if APP_CONFIG_FILE is set (alternative env var)
	if configFile := os.Getenv("APP_CONFIG_FILE"); configFile != "" {
		return mergeConfigFile(v, configFile)
	}

	// 3. Try default locations in order of preference
	configPaths := []string{
		"config/config.production.yml", // Production
		"config/config.dev.yml",        // Development
	}

	// Determine environment
	env := os.Getenv("APP_ENV")
	if env == "" {
		env = "development"
	}

	// Try environment-specific configs first
	if env != "production" {
		envSpecificPaths := []string{
			fmt.Sprintf("config/config.%s.yml", env),
			fmt.Sprintf("configs/config.%s.yml", env),
		}
		configPaths = append(envSpecificPaths, configPaths...)
	}

	var lastErr error
	for _, path := range configPaths {
		if fileExists(path) {
			if err := mergeConfigFile(v, path); err == nil {
				log.Printf("Loaded config from: %s", path)
				return nil
			} else {
				lastErr = err
			}
		}
	}

	return fmt.Errorf("no valid config file found, last error: %v", lastErr)
}

// mergeConfigFile merges a config file into the existing viper instance
func mergeConfigFile(v *viper.Viper, configFile string) error {
	v.SetConfigFile(configFile)

	// If this is the first config file being loaded, use ReadInConfig
	if v.ConfigFileUsed() == "" {
		return v.ReadInConfig()
	}

	// Otherwise, merge with existing configuration
	return v.MergeInConfig()
}

// validateConfig performs validation using go-playground/validator
func validateConfig(cfg *Config) error {
	if err := validate.Struct(cfg); err != nil {
		if validationErrors, ok := err.(validator.ValidationErrors); ok {
			var errorMessages []string
			for _, e := range validationErrors {
				errorMessages = append(errorMessages, e.Error())
			}
			return fmt.Errorf("validation errors: %s", strings.Join(errorMessages, "; "))
		}
		return fmt.Errorf("validation error: %v", err)
	}
	return nil
}

// fileExists checks if a file exists and is not a directory
func fileExists(filename string) bool {
	info, err := os.Stat(filename)
	if os.IsNotExist(err) {
		return false
	}
	return !info.IsDir()
}

// Environment-specific helper functions
func (c *Config) IsDevelopment() bool {
	return c.Env == "development" || c.Env == "dev"
}

func (c *Config) IsProduction() bool {
	return c.Env == "production" || c.Env == "prod"
}

func (c *Config) IsTest() bool {
	return c.Env == "test"
}
