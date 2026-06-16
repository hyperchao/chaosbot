package agent

import (
	"context"
	"errors"
	"fmt"
	"unsafe"

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
// ("use provider default") when not set. MaxContextTokens
// is the soft context budget (ADR-0002); 0 uses the
// default, > 0 is verbatim. SafetyMarginFraction is the
// safety margin against heuristic inaccuracy.
type Config struct {
	System               string
	Model                string
	Temperature          float64
	MaxTokens            int
	MaxSteps             int
	MaxContextTokens     int
	SafetyMarginFraction float64
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
// Set generously enough for multi-tool programming tasks
// (read N files + edit + test + commit usually lands in
// 15-25 steps). Real workloads that exceed this should
// configure the value explicitly in the chaosbot config.
const defaultMaxSteps = 30

// defaultMaxContextTokens is the soft context budget.
// Defaults to 128_000 (the context window of mainstream
// long-context models like GPT-4 Turbo / Claude 3 /
// Gemini 1.5); users should override this to match
// their actual model's context window. The
// SafetyMarginFraction further trims it for
// heuristic inaccuracy.
const defaultMaxContextTokens = 128_000

// defaultSafetyMarginFraction is the safety margin
// against heuristic inaccuracy. 10% is well above
// the ±20% error of EstimateTokensDefault.
const defaultSafetyMarginFraction = 0.10

// Run implements Agent. It drives the ReAct loop up to
// MaxSteps times on top of the agent's history. The user
// message is added to a local candidate slice; only when
// the run completes successfully (the model produced a
// final answer) is the candidate committed back to
// a.History. A failed run — ctx cancel, provider /
// Validate error, or MaxSteps exhausted — leaves a.History
// untouched, so the next Run starts from the same point.
// The loop terminates when the model produces a final
// answer (no tool calls), when ctx is canceled, when a
// provider / Validate error fires, or when MaxSteps is
// exhausted (in which case ErrMaxSteps is wrapped and
// returned, and no assistant message is appended).
func (a *reActAgent) Run(ctx context.Context, prompt string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	// Build a candidate history with the new user message.
	// We do NOT mutate a.History until the run succeeds.
	// append() may or may not allocate a new backing array;
	// either way, a.History is left unchanged because we
	// never assign back until the final answer arrives.
	history := append(a.History, NewUserMessage(prompt))

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
		// Apply the sliding window before each step so
		// the request we send to the provider is bounded
		// by MaxContextTokens. summarizeBeforeDrop may add
		// an extra Chat call if the budget is exceeded.
		history, err = a.applyWindow(ctx, history)
		if err != nil {
			return "", err
		}
		history, final, err = a.step(ctx, history)
		if err != nil {
			return "", err
		}
		if final != "" {
			a.History = history
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

// contextBudget returns the effective token budget
// (MaxContextTokens minus the safety margin). Values
// are clamped: negative MaxContextTokens → 0, frac
// outside [0,1) → nearest valid edge, final budget ≥ 0.
func (a *reActAgent) contextBudget() int {
	max := a.Cfg.MaxContextTokens
	if max <= 0 {
		max = defaultMaxContextTokens
	}
	frac := a.Cfg.SafetyMarginFraction
	if frac == 0 {
		frac = defaultSafetyMarginFraction
	} else if frac < 0 {
		frac = 0
	} else if frac >= 1 {
		frac = defaultSafetyMarginFraction
	}
	budget := int(float64(max) * (1 - frac))
	if budget < 0 {
		budget = 0
	}
	return budget
}

// estimateHistoryTokens sums the provider's EstimateTokens
// for each message's Content plus each tool call's Arguments
// (zero-alloc []byte→string via unsafe). Provider-specific
// tokenizers (e.g. tiktoken) are respected.
func (a *reActAgent) estimateHistoryTokens(history []provider.Message) int {
	total := 0
	for _, m := range history {
		total += a.Provider.EstimateTokens(m.Content)
		for _, tc := range m.ToolCalls {
			if len(tc.Arguments) > 0 {
				total += a.Provider.EstimateTokens(
					unsafe.String(unsafe.SliceData(tc.Arguments), len(tc.Arguments)),
				)
			}
		}
	}
	return total
}

// applyWindow returns a windowed view of history that
// fits within the configured context budget; oldest
// whole turns are dropped otherwise. A single turn
// that alone exceeds the budget is left for the
// safety net. Summarization is a planned follow-up
// (ADR-0002 "Future work").
func (a *reActAgent) applyWindow(_ context.Context, history []provider.Message) ([]provider.Message, error) {
	budget := a.contextBudget()
	if a.estimateHistoryTokens(history) <= budget {
		return history, nil
	}
	return dropOldestTurns(history, budget, a.estimateHistoryTokens), nil
}

// dropOldestTurns removes whole turns from the head
// until the estimate fits the budget. A turn is a
// user message + everything up to the next user
// message.
func dropOldestTurns(history []provider.Message, budget int, estimate func([]provider.Message) int) []provider.Message {
	for len(history) > 0 {
		end := turnEnd(history)
		if end < 0 {
			return history
		}
		candidate := history[end:]
		if estimate(candidate) <= budget {
			return candidate
		}
		history = candidate
	}
	return history
}

// turnEnd returns the index that ends the first turn
// (the index of the second user message), or -1 if
// history contains at most one turn.
func turnEnd(history []provider.Message) int {
	seen := 0
	for i, m := range history {
		if m.Role == provider.RoleUser {
			if seen == 1 {
				return i
			}
			seen++
		}
	}
	return -1
}
