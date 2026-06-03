package agent

import (
	"context"
	"errors"
	"fmt"

	"chaosbot/internal/provider"
)

// Agent is the ReAct orchestrator. It owns the LLM provider, the
// tool registry, and the per-request configuration. Run drives
// the loop by calling step until the model returns a final
// answer or a termination condition fires.
type Agent struct {
	Provider    provider.Provider
	Registry    *Registry
	System      string
	Model       string
	Temperature float64
	MaxTokens   int
	MaxSteps    int // <= 0 means defaultMaxSteps
}

// ErrMaxSteps is returned by Run when the model never produces
// a final answer within MaxSteps iterations. Wrap with %w so
// callers can errors.Is() while still seeing the configured
// limit in the formatted error.
var ErrMaxSteps = errors.New("agent: max steps reached without final answer")

// defaultMaxSteps is the fallback when Agent.MaxSteps <= 0.
// Kept as a package constant so tests can reference it and the
// godoc on MaxSteps is unambiguous.
const defaultMaxSteps = 10

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
func (a *Agent) step(ctx context.Context, history []provider.Message) ([]provider.Message, string, error) {
	req := provider.Request{
		System:      a.System,
		Messages:    history,
		Tools:       a.Registry.Specs(),
		Model:       a.Model,
		Temperature: a.Temperature,
		MaxTokens:   a.MaxTokens,
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

// Run drives the ReAct loop. It seeds the history with the
// user message and calls step up to MaxSteps times. The loop
// terminates when step returns a non-empty finalContent, when
// the context is canceled, when a provider / Validate error
// fires, or when MaxSteps is exhausted (in which case
// ErrMaxSteps is wrapped and returned).
//
// MaxSteps <= 0 falls back to defaultMaxSteps (10). Cancellation
// is checked at the top of each iteration so a Run that started
// with an already-canceled ctx bails before the first Chat.
func (a *Agent) Run(ctx context.Context, userInput string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	max := a.MaxSteps
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
