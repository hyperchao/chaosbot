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

// Options bundles the wiring for the ReAct agent. Passed to
// New; New returns the Agent interface.
type Options struct {
	Provider    provider.Provider
	Registry    *Registry
	System      string
	Model       string
	Temperature float64
	MaxTokens   int
	MaxSteps    int
}

// New constructs the default ReAct agent. Returns the Agent
// interface; the concrete type is unexported so callers go
// through this constructor.
func New(opts Options) Agent {
	return &reActAgent{
		provider:    opts.Provider,
		registry:    opts.Registry,
		system:      opts.System,
		model:       opts.Model,
		temperature: opts.Temperature,
		maxTokens:   opts.MaxTokens,
		maxSteps:    opts.MaxSteps,
	}
}

// reActAgent is the concrete ReAct implementation. Unexported
// so the only way to get one is through New. Methods are
// accessible to tests in package agent.
type reActAgent struct {
	provider    provider.Provider
	registry    *Registry
	system      string
	model       string
	temperature float64
	maxTokens   int
	maxSteps    int
}

// ErrMaxSteps is returned by Run when the model never produces
// a final answer within MaxSteps iterations. Wrap with %w so
// callers can errors.Is() while still seeing the configured
// limit in the formatted error.
var ErrMaxSteps = errors.New("agent: max steps reached without final answer")

// defaultMaxSteps is the fallback when Options.MaxSteps <= 0.
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
	max := a.maxSteps
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
		System:      a.system,
		Messages:    history,
		Tools:       a.registry.Specs(),
		Model:       a.model,
		Temperature: a.temperature,
		MaxTokens:   a.maxTokens,
	}
	if err := req.Validate(); err != nil {
		return nil, "", fmt.Errorf("agent: invalid request: %w", err)
	}
	resp, err := a.provider.Chat(ctx, req)
	if err != nil {
		return nil, "", fmt.Errorf("agent: chat: %w", err)
	}

	newHistory := append(history, NewAssistantMessage(resp.Content, resp.ToolCalls))

	if len(resp.ToolCalls) == 0 {
		return newHistory, resp.Content, nil
	}

	for _, call := range resp.ToolCalls {
		result, err := a.registry.Invoke(ctx, call.Name, call.Arguments)
		if err != nil {
			result = err.Error()
		}
		newHistory = append(newHistory, NewToolMessage(call.ID, call.Name, result))
	}
	return newHistory, "", nil
}
