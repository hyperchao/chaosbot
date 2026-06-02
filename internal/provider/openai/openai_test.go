package openai_test

import (
	"testing"

	"chaosbot/internal/provider"
	"chaosbot/internal/provider/openai"
)

func TestNew_ReturnsProviderWithName(t *testing.T) {
	p := openai.New(provider.Config{APIKey: "test-key", Name: "deepseek"})
	if p == nil {
		t.Fatal("New returned nil")
	}
	if got := p.Name(); got != "deepseek" {
		t.Errorf("Name() = %q, want %q (preserved case)", got, "deepseek")
	}
}

func TestNew_EmptyName_DefaultsToOpenAI(t *testing.T) {
	p := openai.New(provider.Config{APIKey: "test-key"})
	if got := p.Name(); got != "openai" {
		t.Errorf("Name() = %q, want %q", got, "openai")
	}
}
