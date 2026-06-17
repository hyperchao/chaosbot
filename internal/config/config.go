// Package config loads chaosbot's runtime configuration from
// a YAML file and the process environment, with environment
// variables winning over the file. The CLI flag --config
// picks the file path; if empty, only env + built-in defaults
// apply. No XDG / cwd auto-discovery in this phase.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the top-level runtime configuration. The Provider
// block feeds provider.Config; System / MaxSteps / Temperature
// / MaxTokens / Workspace feed agent.Agent.
type Config struct {
	Provider    ProviderConfig `yaml:"provider"`
	System      string         `yaml:"system"`
	MaxSteps    int            `yaml:"max_steps"`
	Temperature float64        `yaml:"temperature"`
	MaxTokens   int            `yaml:"max_tokens"`
	Workspace   string         `yaml:"workspace"`
	SessionsDir string         `yaml:"sessions_dir"`
}

// ProviderConfig is the chaosbot-facing subset of provider
// settings plus a few chaosbot-specific conveniences (api_key_env
// for indirection, model for the Agent).
type ProviderConfig struct {
	Name      string        `yaml:"name"`
	APIKey    string        `yaml:"api_key"`
	APIKeyEnv string        `yaml:"api_key_env"`
	BaseURL   string        `yaml:"base_url"`
	Model     string        `yaml:"model"`
	OrgID     string        `yaml:"org_id"`
	Timeout   time.Duration `yaml:"timeout"`
}

// Load reads YAML from path (if non-empty), then overlays
// environment variables, applies built-in defaults, resolves
// the API key, and validates. Returns an error if the API key
// cannot be located.
func Load(path string) (*Config, error) {
	cfg := defaults()
	if path != "" {
		if err := loadYAML(path, cfg); err != nil {
			return nil, err
		}
	}
	applyEnv(cfg)
	applyDefaults(cfg)
	resolveAPIKey(cfg)
	if err := validate(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func loadYAML(path string, cfg *Config) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("config: read %s: %w", path, err)
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return fmt.Errorf("config: parse %s: %w", path, err)
	}
	return nil
}

// applyEnv overlays CHAOSBOT_* env vars on top of whatever
// Load / YAML produced. Unknown or unparseable values are
// ignored (a malformed MAX_STEPS stays at the default).
func applyEnv(cfg *Config) {
	if v := os.Getenv("CHAOSBOT_PROVIDER"); v != "" {
		cfg.Provider.Name = v
	}
	if v := os.Getenv("CHAOSBOT_API_KEY"); v != "" {
		cfg.Provider.APIKey = v
	}
	if v := os.Getenv("CHAOSBOT_BASE_URL"); v != "" {
		cfg.Provider.BaseURL = v
	}
	if v := os.Getenv("CHAOSBOT_MODEL"); v != "" {
		cfg.Provider.Model = v
	}
	if v := os.Getenv("CHAOSBOT_SYSTEM"); v != "" {
		cfg.System = v
	}
	if v := os.Getenv("CHAOSBOT_MAX_STEPS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.MaxSteps = n
		}
	}
	if v := os.Getenv("CHAOSBOT_WORKSPACE"); v != "" {
		cfg.Workspace = v
	}
	if v := os.Getenv("CHAOSBOT_SESSIONS_DIR"); v != "" {
		cfg.SessionsDir = v
	}
}

// applyDefaults fills zero-valued fields with sensible
// built-in defaults. Called after env overlay so a user
// who explicitly sets CHAOSBOT_PROVIDER="" still gets
// "openai" (the env-var check uses != "").
func applyDefaults(cfg *Config) {
	if cfg.Provider.Name == "" {
		cfg.Provider.Name = "openai"
	}
	if cfg.Provider.APIKeyEnv == "" {
		cfg.Provider.APIKeyEnv = "OPENAI_API_KEY"
	}
	if cfg.Provider.Timeout == 0 {
		cfg.Provider.Timeout = 60 * time.Second
	}
	if cfg.MaxSteps == 0 {
		cfg.MaxSteps = 30
	}
	if cfg.Temperature == 0 {
		cfg.Temperature = 0.7
	}
	if cfg.SessionsDir == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			cfg.SessionsDir = home + "/.chaosbot/sessions"
		} else {
			cfg.SessionsDir = ".chaosbot/sessions"
		}
	}
}

// resolveAPIKey picks the first non-empty source in the
// precedence order: explicit APIKey, then APIKeyEnv.
func resolveAPIKey(cfg *Config) {
	if cfg.Provider.APIKey != "" {
		return
	}
	if cfg.Provider.APIKeyEnv != "" {
		cfg.Provider.APIKey = os.Getenv(cfg.Provider.APIKeyEnv)
	}
}

func validate(cfg *Config) error {
	if cfg.Provider.APIKey == "" {
		return errors.New("config: API key not set (use CHAOSBOT_API_KEY, provider.api_key, or provider.api_key_env)")
	}
	return nil
}

func defaults() *Config {
	return &Config{}
}
