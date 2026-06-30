package openai

import (
	"chaosbot/internal/provider"
	"testing"
)

func TestToOpenAIMessage_ToolRole(t *testing.T) {
	m := provider.Message{
		Role:       provider.RoleTool,
		Content:    "result content",
		Name:       "my_tool",
		ToolCallID: "call_123",
	}
	got, err := toOpenAIMessage(m)
	if err != nil {
		t.Fatalf("toOpenAIMessage(RoleTool): %v", err)
	}
	if got.Role != "tool" {
		t.Errorf("Role = %q, want %q", got.Role, "tool")
	}
	if got.Content != "result content" {
		t.Errorf("Content = %q, want %q", got.Content, "result content")
	}
	if got.ToolCallID != "call_123" {
		t.Errorf("ToolCallID = %q, want %q", got.ToolCallID, "call_123")
	}
	if got.Name != "my_tool" {
		t.Errorf("Name = %q, want %q", got.Name, "my_tool")
	}
}

func TestToOpenAIMessage_UnknownRole(t *testing.T) {
	_, err := toOpenAIMessage(provider.Message{Role: provider.Role("bogus")})
	if err == nil {
		t.Fatal("unknown role: got nil, want error")
	}
}

func TestEstimateTokens_EmptyString(t *testing.T) {
	p := New(provider.Config{APIKey: "test"})
	got := p.EstimateTokens("")
	if got != 0 {
		t.Errorf("EstimateTokens(%q) = %d, want 0", "", got)
	}
}
