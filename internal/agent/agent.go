package agent

import (
	"context"
	"errors"
	"fmt"

	"chaosbot/internal/provider"
)

// Agent is the boundary the CLI, session, and tests depend on.
//
// The agent owns the conversation history: callers (the one-shot
// `run` subcommand, the REPL, future session persistence) call
// Run to drive one ReAct turn, and call Reset to start a new
// conversation. History never leaves the agent — keeping it
// internal means callers don't have to know about message
// construction, ordering, or append rules. Phase 06 wraps this
// in a Session struct (which adds JSON persistence on top) but
// doesn't change the Agent interface.
type Agent interface {
	Run(ctx context.Context, prompt string) (string, error)
	Reset()
}

// Config holds the agent's non-DI runtime config. The chaosbot
// config package populates this; main.go maps from config.Config
// to agent.Config. Temperature and MaxTokens default to 0
// ("use provider default") when not set.
type Config struct {
	System      string
	Model       string
	Temperature float64
	MaxTokens   int
	MaxSteps    int
}

// reActAgent is the concrete ReAct implementation. The TYPE
// is unexported (callers go through the Agent interface) but
// the FIELDS are exported so the di library can fill them via
// reflection — unexported fields cannot be reflect.Set. The
// fields' exported-ness is a DI requirement, not a public API:
// callers should not construct or read reActAgent directly;
// they use the Agent interface. The package's own tests in
// package agent have internal access and use struct literals
// for direct construction; tests in package agent_test build
// the agent via the di library (per AGENTS.md: "For tests,
// build a fresh di.New() and register hand-written fakes").
//
// History has no di tag: it is per-instance state managed by
// Run/Reset, not an injected dependency. The di library
// ignores untagged fields, leaving the zero value ([]Message)
// in place.
type reActAgent struct {
	Provider provider.Provider `di:"type"`
	Registry *Registry         `di:"type"`
	Cfg      Config            `di:"type"`
	History  []provider.Message
}

// New is the no-arg constructor used by the di library. The
// returned *reActAgent has all fields zero; the library
// populates them via reflection by walking the di tags.
func New() Agent {
	return &reActAgent{}
}

// ErrMaxSteps is returned by Run when the model never produces
// a final answer within MaxSteps iterations. Wrap with %w so
// callers can errors.Is() while still seeing the configured
// limit in the formatted error.
var ErrMaxSteps = errors.New("agent: max steps reached without final answer")

// defaultMaxSteps is the fallback when Config.MaxSteps <= 0.
const defaultMaxSteps = 10

// Run implements Agent. It appends the user message to the
// agent's history and drives the ReAct loop up to MaxSteps
// times. The final assistant message is appended to history
// (so subsequent calls see the full exchange) and its text
// content is returned. The loop terminates when the model
// produces a final answer (no tool calls), when ctx is
// canceled, when a provider / Validate error fires, or when
// MaxSteps is exhausted (in which case ErrMaxSteps is wrapped
// and returned, and no assistant message is appended).
func (a *reActAgent) Run(ctx context.Context, prompt string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	a.History = append(a.History, NewUserMessage(prompt))

	max := a.Cfg.MaxSteps
	if max <= 0 {
		max = defaultMaxSteps
	}
	var final string
	var err error
	for i := 0; i < max; i++ {
		if err = ctx.Err(); err != nil {
			return "", err
		}
		a.History, final, err = a.step(ctx, a.History)
		if err != nil {
			return "", err
		}
		if final != "" {
			return final, nil
		}
	}
	return "", fmt.Errorf("agent: %d steps exhausted: %w", max, ErrMaxSteps)
}

// Reset implements Agent. It drops the in-memory conversation
// history. The agent's Provider, Registry, and Cfg are
// unaffected; subsequent Run calls start a fresh conversation
// (with the same model / system prompt / tools).
func (a *reActAgent) Reset() {
	a.History = a.History[:0]
}

// step performs one ReAct iteration: send history to the
// provider, dispatch any tool calls, return the updated
// history. If the assistant did not request any tools,
// finalContent is the answer and the caller should stop;
// otherwise the caller should call step again with the
// returned history.
//
// Tool execution errors are NOT returned as Go errors — they
// are embedded in the appended tool message (Content set to
// the error string) so the LLM can decide how to react.
// Provider / Validate errors ARE returned as Go errors and
// abort the loop; the caller surfaces them to the user.
func (a *reActAgent) step(ctx context.Context, history []provider.Message) ([]provider.Message, string, error) {
	req := provider.Request{
		System:      a.Cfg.System,
		Messages:    history,
		Tools:       a.Registry.Specs(),
		Model:       a.Cfg.Model,
		Temperature: a.Cfg.Temperature,
		MaxTokens:   a.Cfg.MaxTokens,
	}
	if err := req.Validate(); err != nil {
		return nil, "", fmt.Errorf("agent: invalid request: %w", err)
	}
	resp, err := a.Provider.Chat(ctx, req)
	if err != nil {
		return nil, "", fmt.Errorf("agent: chat: %w", err)
	}

	newHistory := append(history, NewAssistantMessage(resp.Content, resp.ToolCalls))

	if len(resp.ToolCalls) == 0 {
		return newHistory, resp.Content, nil
	}

	for _, call := range resp.ToolCalls {
		result, err := a.Registry.Invoke(ctx, call.Name, call.Arguments)
		if err != nil {
			result = err.Error()
		}
		newHistory = append(newHistory, NewToolMessage(call.ID, call.Name, result))
	}
	return newHistory, "", nil
}
