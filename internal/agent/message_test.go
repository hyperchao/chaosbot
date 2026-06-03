package agent_test

import (
	"encoding/json"
	"testing"

	"chaosbot/internal/agent"
	"chaosbot/internal/provider"
)

func TestNewUserMessage(t *testing.T) {
	cases := []struct {
		name    string
		content string
	}{
		{"empty", ""},
		{"ascii", "hello"},
		{"unicode", "你好"},
		{"multiline", "line1\nline2\nline3"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := agent.NewUserMessage(c.content)
			if m.Role != provider.RoleUser {
				t.Errorf("Role = %q, want %q", m.Role, provider.RoleUser)
			}
			if m.Content != c.content {
				t.Errorf("Content = %q, want %q", m.Content, c.content)
			}
			if m.ToolCalls != nil {
				t.Errorf("ToolCalls = %+v, want nil", m.ToolCalls)
			}
		})
	}
}

func TestNewAssistantMessage(t *testing.T) {
	calls := []provider.ToolCall{
		{ID: "1", Name: "echo", Arguments: json.RawMessage(`{}`)},
	}
	cases := []struct {
		name      string
		content   string
		calls     []provider.ToolCall
		wantCalls int
	}{
		{"final answer, no tools", "the answer", nil, 0},
		{"with one tool call", "", calls, 1},
		{"with empty calls slice", "", []provider.ToolCall{}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := agent.NewAssistantMessage(c.content, c.calls)
			if m.Role != provider.RoleAssistant {
				t.Errorf("Role = %q, want %q", m.Role, provider.RoleAssistant)
			}
			if m.Content != c.content {
				t.Errorf("Content = %q, want %q", m.Content, c.content)
			}
			if len(m.ToolCalls) != c.wantCalls {
				t.Errorf("len(ToolCalls) = %d, want %d", len(m.ToolCalls), c.wantCalls)
			}
		})
	}
}

func TestNewToolMessage(t *testing.T) {
	m := agent.NewToolMessage("call-id-42", "echo", "result-string")
	if m.Role != provider.RoleTool {
		t.Errorf("Role = %q, want %q", m.Role, provider.RoleTool)
	}
	if m.ToolCallID != "call-id-42" {
		t.Errorf("ToolCallID = %q, want %q", m.ToolCallID, "call-id-42")
	}
	if m.Name != "echo" {
		t.Errorf("Name = %q, want %q", m.Name, "echo")
	}
	if m.Content != "result-string" {
		t.Errorf("Content = %q, want %q", m.Content, "result-string")
	}
}
