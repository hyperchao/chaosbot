package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"chaosbot/internal/config"
)

func TestLoad_Defaults(t *testing.T) {
	t.Setenv("CHAOSBOT_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	cfg, err := config.Load("")
	if err == nil {
		t.Fatalf("Load: want error when no API key, got cfg=%+v", cfg)
	}
}

func TestLoad_FromEnv_APIKey(t *testing.T) {
	t.Setenv("CHAOSBOT_API_KEY", "sk-test-123")
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Provider.APIKey != "sk-test-123" {
		t.Errorf("APIKey = %q, want %q", cfg.Provider.APIKey, "sk-test-123")
	}
	if cfg.Provider.Name != "openai" {
		t.Errorf("Name = %q, want default 'openai'", cfg.Provider.Name)
	}
	if cfg.Provider.APIKeyEnv != "OPENAI_API_KEY" {
		t.Errorf("APIKeyEnv = %q, want default", cfg.Provider.APIKeyEnv)
	}
	if cfg.Provider.Timeout != 60*time.Second {
		t.Errorf("Timeout = %v, want 60s", cfg.Provider.Timeout)
	}
	if cfg.MaxSteps != 30 {
		t.Errorf("MaxSteps = %d, want 30", cfg.MaxSteps)
	}
	if cfg.Temperature != 0.7 {
		t.Errorf("Temperature = %v, want default 0.7", cfg.Temperature)
	}
}

func TestLoad_FromYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yaml := `
provider:
  name: deepseek
  base_url: https://api.deepseek.com
  api_key_env: DEEPSEEK_API_KEY
  model: deepseek-chat
system: "you are a helper"
max_steps: 25
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
	t.Setenv("DEEPSEEK_API_KEY", "sk-deep-xyz")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Provider.Name != "deepseek" {
		t.Errorf("Name = %q, want %q", cfg.Provider.Name, "deepseek")
	}
	if cfg.Provider.BaseURL != "https://api.deepseek.com" {
		t.Errorf("BaseURL = %q", cfg.Provider.BaseURL)
	}
	if cfg.Provider.Model != "deepseek-chat" {
		t.Errorf("Model = %q", cfg.Provider.Model)
	}
	if cfg.System != "you are a helper" {
		t.Errorf("System = %q", cfg.System)
	}
	if cfg.MaxSteps != 25 {
		t.Errorf("MaxSteps = %d, want 25", cfg.MaxSteps)
	}
	if cfg.Provider.APIKey != "sk-deep-xyz" {
		t.Errorf("APIKey = %q (api_key_env resolution)", cfg.Provider.APIKey)
	}
}

func TestLoad_EnvOverridesYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yaml := `
provider:
  name: openai
  api_key: sk-from-yaml
  model: gpt-4o-mini
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
	t.Setenv("CHAOSBOT_API_KEY", "sk-from-env")
	t.Setenv("CHAOSBOT_MODEL", "gpt-4o")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Provider.APIKey != "sk-from-env" {
		t.Errorf("APIKey = %q, want env to win (%q)", cfg.Provider.APIKey, "sk-from-env")
	}
	if cfg.Provider.Model != "gpt-4o" {
		t.Errorf("Model = %q, want env to win", cfg.Provider.Model)
	}
	if cfg.Provider.Name != "openai" {
		t.Errorf("Name = %q, should remain from YAML (not overridden)", cfg.Provider.Name)
	}
}

func TestLoad_APIKeyEnvFallsBackToProviderEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yaml := `
provider:
  name: openai
  api_key_env: CUSTOM_KEY
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
	t.Setenv("CUSTOM_KEY", "sk-custom")
	t.Setenv("CHAOSBOT_API_KEY", "")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Provider.APIKey != "sk-custom" {
		t.Errorf("APIKey = %q, want %q (resolved from api_key_env)", cfg.Provider.APIKey, "sk-custom")
	}
}

func TestLoad_MissingAPIKey_ReturnsError(t *testing.T) {
	t.Setenv("CHAOSBOT_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("DEEPSEEK_API_KEY", "")
	_, err := config.Load("")
	if err == nil {
		t.Fatal("Load: want error when no API key source is set, got nil")
	}
}

func TestLoad_MalformedYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(":::not yaml"), 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
	_, err := config.Load(path)
	if err == nil {
		t.Fatal("Load: want error on malformed YAML, got nil")
	}
}

func TestLoad_YAMLFileNotFound(t *testing.T) {
	_, err := config.Load("/nonexistent/config.yaml")
	if err == nil {
		t.Fatal("Load: want error when file does not exist, got nil")
	}
}

func TestLoad_SessionsDirDefault(t *testing.T) {
	t.Setenv("CHAOSBOT_API_KEY", "sk-test")
	t.Setenv("CHAOSBOT_SESSIONS_DIR", "")
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.SessionsDir == "" {
		t.Error("SessionsDir is empty; want default")
	}
	// The default should reference the user's home dir.
	home, _ := os.UserHomeDir()
	if home != "" && !strings.HasPrefix(cfg.SessionsDir, home) {
		t.Errorf("SessionsDir = %q, want prefix %q", cfg.SessionsDir, home)
	}
}

func TestLoad_SessionsDirFromEnv(t *testing.T) {
	t.Setenv("CHAOSBOT_API_KEY", "sk-test")
	t.Setenv("CHAOSBOT_SESSIONS_DIR", "/tmp/my-sessions")
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.SessionsDir != "/tmp/my-sessions" {
		t.Errorf("SessionsDir = %q, want /tmp/my-sessions", cfg.SessionsDir)
	}
}
