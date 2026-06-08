package agent

import (
	"context"
	"errors"
	"fmt"

	"chaosbot/internal/provider"
)

// Agent is the boundary the CLI, session, and tests depend on.
// One method: drive the ReAct loop with the user's prompt and
// return the final answer.
type Agent interface {
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

// Run implements Agent. It drives the ReAct loop: seed history
// with the user message and call step up to MaxSteps times.
// The loop terminates when step returns a non-empty finalContent,
// when the context is canceled, when a provider / Validate error
// fires, or when MaxSteps is exhausted (in which case
// ErrMaxSteps is wrapped and returned).
func (a *reActAgent) Run(ctx context.Context, userInput string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	max := a.Cfg.MaxSteps
	if max <= 0 {
		max = defaultMaxSteps
	}
	var final string
	var err error
	history := []provider.Message{NewUserMessage(userInput)}
	for i := 0; i < max; i++ {
		if err = ctx.Err(); err != nil {
			return "", err
		}
		history, final, err = a.step(ctx, history)
		if err != nil {
			return "", err
		}
		if final != "" {
			return final, nil
		}
	}
	return "", fmt.Errorf("agent: %d steps exhausted: %w", max, ErrMaxSteps)
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
