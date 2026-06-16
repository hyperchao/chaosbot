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
	"time"
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

// Config bundles the connection settings for a Provider backend.
// Every concrete implementation (openai.New, future anthropic.New,
// etc.) accepts this single Config — there is no per-provider Config
// type. The factory Build(cfg) dispatches on Name.
//
// Fields that some implementations don't use (e.g. OrgID for
// non-OpenAI vendors) are silently ignored. Timeout=0 means
// "implementation default".
type Config struct {
	Name    string
	APIKey  string
	BaseURL string
	OrgID   string
	Timeout time.Duration
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

	// EstimateTokens returns the approximate token count of
	// the given content. The default heuristic (see
	// EstimateTokensDefault) is ±20% accurate vs. real
	// tokenizers, which is good enough for windowing
	// with a safety buffer. Providers with access to an
	// exact tokenizer (e.g. tiktoken for OpenAI) can
	// override for higher accuracy.
	EstimateTokens(content string) int
}

// EstimateTokensDefault is the heuristic fallback used by
// providers that don't override Provider.EstimateTokens.
//
// The heuristic:
//   - Detect CJK (runes >= 0x3000 in a 200-rune sample)
//     and use 1 rune/token (closer to 1.0-1.5 for
//     Chinese / Japanese / Korean tokenizers).
//   - Otherwise use 3 bytes/token (closer to 4 for
//     English in real GPT tokenizers; 3 is conservative).
//
// ±20% accuracy vs. real tokenizers; the agent's safety
// buffer (ContextBufferFraction, default 10%) absorbs the
// error. Returning a slightly higher count is safer
// (windowing triggers earlier, never missing the cap).
func EstimateTokensDefault(content string) int {
	if content == "" {
		return 0
	}
	const sample = 200
	runes := []rune(content)
	end := min(len(runes), sample)
	cjk := false
	for _, r := range runes[:end] {
		if r >= 0x3000 {
			cjk = true
			break
		}
	}
	if cjk {
		// Count runes (chars); 1 rune ≈ 1 token for CJK.
		return max(len(runes), 1)
	}
	// Latin: bytes / 3 (ASCII is 1 byte per char).
	return max(len(content)/3, 1)
}

// ErrContextLength is the sentinel returned (or wrapped) by a
// provider when the LLM rejected the request because the
// combined prompt exceeds the model's context window. The
// agent uses this signal to clear the in-memory history and
// ask the LLM to repeat its last request.
var ErrContextLength = errors.New("provider: context length exceeded")
