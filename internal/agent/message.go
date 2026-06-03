package agent

import "chaosbot/internal/provider"

// NewUserMessage returns a Message with Role: RoleUser and the given
// content. This is the only field set for a user turn; ToolCalls,
// ToolCallID, and Name are left as their zero values.
func NewUserMessage(content string) provider.Message {
	return provider.Message{Role: provider.RoleUser, Content: content}
}

// NewAssistantMessage returns a Message with Role: RoleAssistant. If
// toolCalls is non-empty the assistant turn requested one or more
// tool invocations; otherwise the turn is a final answer (or an
// empty content turn paired with tool calls).
func NewAssistantMessage(content string, toolCalls []provider.ToolCall) provider.Message {
	return provider.Message{Role: provider.RoleAssistant, Content: content, ToolCalls: toolCalls}
}

// NewToolMessage returns a Message with Role: RoleTool, Content set
// to the tool's string output (or the wrapped error string if the
// tool returned a Go error — see agent.step), plus the matching
// ToolCallID from the assistant turn and the tool's registered Name
// so the LLM can match which tool produced the result.
func NewToolMessage(toolCallID, name, content string) provider.Message {
	return provider.Message{Role: provider.RoleTool, Content: content, ToolCallID: toolCallID, Name: name}
}
