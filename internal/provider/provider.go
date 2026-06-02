// Package provider defines the LLM boundary for chaosbot.
//
// The agent loop depends on this interface only; concrete
// implementations (OpenAI-compatible, Anthropic, etc.) live in
// subpackages and are wired at the composition root
// (cmd/chaosbot/main.go) via hyperchao/di.
//
// All fields on the public types are exported for the agent's
// convenience; concrete providers must treat them as read-only inputs.
package provider

import (
	"context"
	"encoding/json"
	"errors"
)

// Role identifies the author of a Message.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message is one turn in the conversation. Only fields relevant to the
// role are populated:
//
//   - system, user:      Content.
//   - assistant:         Content and/or ToolCalls.
//   - tool:              Content (string the tool returned) +
//     ToolCallID + Name.
type Message struct {
	Role       Role
	Content    string
	ToolCalls  []ToolCall
	ToolCallID string
	Name       string
}

// ToolSpec describes a tool the model is allowed to call. Parameters
// is a JSON Schema object; providers serialize it into the wire
// format they understand.
type ToolSpec struct {
	Name        string
	Description string
	Parameters  json.RawMessage
}

// ToolCall is one tool invocation requested by the model. Arguments
// is a raw JSON object — the agent hands it to Tool.Invoke unchanged.
type ToolCall struct {
	ID        string
	Name      string
	Arguments json.RawMessage
}

// Usage reports token accounting for a single Chat call. Providers
// that don't track tokens may leave fields at zero.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

// Request bundles one model call. System is the developer prompt and
// is serialized as a leading system message by the provider.
//
// System is mutually exclusive with Messages[0]{Role:RoleSystem}; see
// Validate. It is modeled as a top-level field — not a leading
// message — so the agent loop can treat the system prompt as
// session-scoped configuration, distinct from turn-scoped Messages.
type Request struct {
	System      string
	Messages    []Message
	Tools       []ToolSpec
	Model       string
	Temperature float64
	MaxTokens   int
}

// ErrSystemConflict is returned by Request.Validate when both the
// top-level System field and a leading RoleSystem message are set.
var ErrSystemConflict = errors.New("provider: Request.System and Messages[0]{Role:RoleSystem} are mutually exclusive")

// Validate checks the request for well-formedness. Providers may
// assume a Request has been validated before being passed to Chat;
// the agent loop is responsible for calling Validate at the boundary.
func (r Request) Validate() error {
	if r.System == "" {
		return nil
	}
	if len(r.Messages) > 0 && r.Messages[0].Role == RoleSystem {
		return ErrSystemConflict
	}
	return nil
}

// Response is the model's answer for one Chat call. The agent loop
// branches on whether ToolCalls is empty.
type Response struct {
	Content   string
	ToolCalls []ToolCall
	Usage     Usage
}

// Provider is the LLM boundary. Implementations must be safe for
// concurrent use by the agent loop (it currently calls sequentially
// but the interface should not preclude fan-out).
//
// Implementations may assume the Request has been validated; see
// Request.Validate. The agent loop is responsible for calling
// Validate at the boundary before dispatch.
type Provider interface {
	Chat(ctx context.Context, req Request) (*Response, error)
	Name() string
}
