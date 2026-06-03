package agent

import (
	"context"
	"fmt"

	"chaosbot/internal/provider"
)

// Agent is the ReAct orchestrator. It owns the LLM provider, the
// tool registry, and the per-request configuration; Run
// (Phase 04-3) drives the loop by calling step until the model
// returns a final answer or a termination condition fires.
//
// MaxSteps is added in 04-3 alongside Run. Until then, callers
// are expected to call step directly.
type Agent struct {
	Provider    provider.Provider
	Registry    *Registry
	System      string
	Model       string
	Temperature float64
	MaxTokens   int
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
