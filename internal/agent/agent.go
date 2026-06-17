package agent

import (
	"context"
	"errors"
	"fmt"
	"unsafe"

	"chaosbot/internal/provider"
	"chaosbot/internal/session"
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
	// Resume loads a saved session by id, replacing the
	// current in-memory history with the loaded messages.
	// Subsequent Run calls append to the loaded history and
	// auto-save to the same session id.
	Resume(ctx context.Context, id string) error
	// SessionID returns the current session id, or "" if
	// no session has been started yet (no successful Run
	// since construction or last Reset). The CLI uses
	// this to display the id to the user.
	SessionID() string
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
// Run/Reset/Resume, not an injected dependency. The di library
// ignores untagged fields, leaving the zero value ([]Message)
// in place.
//
// Store is injected via di; sessionID and sessionOffset are
// per-instance state managed by Run/Reset/Resume. When Store
// is nil the agent runs in pure in-memory mode with no
// auto-save.
type reActAgent struct {
	Provider provider.Provider `di:"type"`
	Registry *Registry         `di:"type"`
	Cfg      Config            `di:"type"`
	Store    session.Store     `di:"type"`
	History  []provider.Message

	sessionID     string
	sessionOffset int
}

// Resume implements Agent. Loads a saved session by id:
// reads the full history from Store, replaces the
// in-memory history, and sets sessionID/offset so the
// next Run continues from the loaded state and saves
// back to the same id. Returns os.ErrNotExist (wrapped)
// if the session doesn't exist; returns nil if Store
// is nil (no persistence configured).
func (a *reActAgent) Resume(ctx context.Context, id string) error {
	if a.Store == nil {
		return fmt.Errorf("agent: resume %s: no session store configured", id)
	}
	history, err := a.Store.Load(ctx, id)
	if err != nil {
		return fmt.Errorf("agent: resume %s: %w", id, err)
	}
	a.History = history
	a.sessionID = id
	a.sessionOffset = len(history)
	return nil
}

// SessionID returns the current session id, or "" if
// no session has been started yet (no successful Run
// since construction or last Reset). The CLI uses
// this to display the id to the user.
func (a *reActAgent) SessionID() string {
	return a.sessionID
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
//
// Windowing is applied per-step only to the LLM view; the
// cumulative `history` always reflects the full
// conversation, regardless of how many turns applyWindow
// drops. This keeps a.History and a.sessionOffset
// consistent with what the store has on disk.
//
// On success, if Store is configured, the new messages
// are auto-saved: a session id is generated on first
// successful Run, and subsequent Runs append the
// accumulated delta. The caller does not need to manage
// session ids unless resuming an existing session
// (see Resume).
func (a *reActAgent) Run(ctx context.Context, prompt string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	// Build a candidate cumulative history. We do NOT
	// mutate a.History until the run succeeds. append
	// returns a slice that shares a.History's backing
	// array if cap allows; that's safe because the
	// loop only appends to `history`, never re-slices
	// into a.History's existing elements.
	history := append(a.History, NewUserMessage(prompt))

	max := a.Cfg.MaxSteps
	if max <= 0 {
		max = defaultMaxSteps
	}
	for i := 0; i < max; i++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		// Apply the sliding window to a copy of `history`
		// for the LLM call only. The LLM never sees more
		// than MaxContextTokens worth of context.
		view, err := a.applyWindow(ctx, history)
		if err != nil {
			return "", err
		}
		newView, final, err := a.step(ctx, view)
		if err != nil {
			return "", err
		}
		// newView is the same as view + new messages
		// (assistant + optional tool messages). Append
		// those to the cumulative `history`. The windowed
		// view is discarded; only the delta matters.
		if len(newView) > len(view) {
			history = append(history, newView[len(view):]...)
		}
		if final != "" {
			a.History = history
			a.saveOnSuccess(ctx, history)
			return final, nil
		}
	}
	return "", fmt.Errorf("agent: %d steps exhausted: %w", max, ErrMaxSteps)
}

// saveOnSuccess persists the new turn. Generates a
// session id on the first successful Run. Subsequent
// Runs append the accumulated delta (history[oldOffset:]).
// A nil Store is a no-op (in-memory mode).
func (a *reActAgent) saveOnSuccess(ctx context.Context, history []provider.Message) {
	if a.Store == nil {
		return
	}
	if a.sessionID == "" {
		id, err := session.NewID()
		if err != nil {
			// Log via stderr? For now, swallow — the in-memory
			// state is correct; persistence is best-effort.
			return
		}
		a.sessionID = id
	}
	if len(history) <= a.sessionOffset {
		return
	}
	if err := a.Store.Append(ctx, a.sessionID, history[a.sessionOffset:]); err != nil {
		return
	}
	a.sessionOffset = len(history)
}

// Reset implements Agent. It drops the in-memory
// conversation history, deletes the current session
// file (if any), and resets the session offset so the
// next Run starts a fresh conversation with a new
// session id. The agent's Provider, Registry, Cfg, and
// Store are unaffected.
func (a *reActAgent) Reset() {
	a.History = a.History[:0]
	if a.Store != nil && a.sessionID != "" {
		_ = a.Store.Delete(context.Background(), a.sessionID)
	}
	a.sessionID = ""
	a.sessionOffset = 0
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
