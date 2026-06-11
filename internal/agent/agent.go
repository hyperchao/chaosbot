package agent

import (
	"context"
	"errors"
	"fmt"

	"chaosbot/internal/provider"
)

// Agent is the boundary the CLI, session, and tests depend on.
//
// Chat is the primary entry: pass a full conversation history
// (system / user / assistant / tool messages in order) and get
// back the final assistant message. The ReAct loop runs to
// completion or until ctx is canceled.
//
// Run is a one-shot wrapper around Chat for callers that don't
// maintain their own history (e.g. `chaosbot run "<prompt>"`).
// Callers that want multi-turn (the REPL, future session
// persistence) use Chat directly and accumulate history
// themselves.
type Agent interface {
	Chat(ctx context.Context, msgs []provider.Message) (provider.Message, error)
	Run(ctx context.Context, userInput string) (string, error)
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
type reActAgent struct {
	Provider provider.Provider `di:"type"`
	Registry *Registry         `di:"type"`
	Cfg      Config            `di:"type"`
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

// Run implements Agent as a one-shot wrapper around Chat. It
// seeds the history with the user's message and returns the
// assistant's final text content. Callers that want multi-turn
// (the REPL, session persistence) should use Chat directly and
// accumulate history themselves.
func (a *reActAgent) Run(ctx context.Context, userInput string) (string, error) {
	reply, err := a.Chat(ctx, []provider.Message{NewUserMessage(userInput)})
	if err != nil {
		return "", err
	}
	return reply.Content, nil
}

// Chat implements Agent. It drives the ReAct loop on top of the
// caller's history and returns the final assistant message.
// The loop terminates when step returns an assistant message
// with no tool calls, when ctx is canceled, when a provider /
// Validate error fires, or when MaxSteps is exhausted (in
// which case ErrMaxSteps is wrapped and returned).
func (a *reActAgent) Chat(ctx context.Context, msgs []provider.Message) (provider.Message, error) {
	if err := ctx.Err(); err != nil {
		return provider.Message{}, err
	}
	max := a.Cfg.MaxSteps
	if max <= 0 {
		max = defaultMaxSteps
	}
	history := append([]provider.Message(nil), msgs...)
	for i := 0; i < max; i++ {
		if err := ctx.Err(); err != nil {
			return provider.Message{}, err
		}
		var final string
		var err error
		history, final, err = a.step(ctx, history)
		if err != nil {
			return provider.Message{}, err
		}
		if final != "" {
			return history[len(history)-1], nil
		}
	}
	return provider.Message{}, fmt.Errorf("agent: %d steps exhausted: %w", max, ErrMaxSteps)
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
