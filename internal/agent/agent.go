package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strings"
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
	SummaryDisabled      bool // opt-out: true disables LLM summarization (default false = enabled)
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
	Provider        provider.Provider `di:"type"`
	Registry        *Registry         `di:"type"`
	Cfg             Config            `di:"type"`
	Store           session.Store     `di:"type"`
	History         []provider.Message
	summaryMsg      *provider.Message // nil if never summarized
	committedPrefix int               // count of leading messages from a.History
	// already represented by summaryMsg (if set)
	// or just trimmed away
	trimmedTotal int // absolute count of on-disk messages already discarded
	// (via prune or Resume cursor). History[0] corresponds
	// to disk position trimmedTotal. SaveSummary uses
	// trimmedTotal + committedPrefix so the persisted
	// cursor is always absolute.

	sessionID     string
	sessionOffset int
}

// Resume implements Agent. Loads a saved session by id:
// reads the persisted summary, then loads only the
// not-yet-summarized tail of the history into memory, and
// sets sessionID/offset so the next Run continues from the
// loaded state and saves back to the same id. Returns
// os.ErrNotExist (wrapped) if the session doesn't exist;
// returns nil if Store is nil (no persistence configured).
// Falls back to a full Load when the persisted cursor is
// stale (offset beyond file end) so old or corrupted
// summaries don't break Resume.
func (a *reActAgent) Resume(ctx context.Context, id string) error {
	if a.Store == nil {
		return fmt.Errorf("agent: resume %s: no session store configured", id)
	}
	a.clearSessionState()
	info, err := a.Store.LoadSummary(ctx, id)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("agent: resume %s summary: %w", id, err)
	}
	if err := a.loadHistory(ctx, id, info); err != nil {
		return fmt.Errorf("agent: resume %s: %w", id, err)
	}
	a.sessionID = id
	a.sessionOffset = len(a.History)
	return nil
}

// loadHistory populates a.History and a.summaryMsg from the
// store. info.Cursor > 0 tries Store.LoadFrom for memory
// savings; a stale cursor (beyond file end) falls back to
// Store.Load so old or corrupted summaries don't break
// Resume. info.Content is restored into a.summaryMsg only
// when LoadFrom succeeds (cursor genuinely covered a prefix).
func (a *reActAgent) loadHistory(ctx context.Context, id string, info session.SummaryInfo) error {
	if info.Cursor > 0 {
		tail, err := a.Store.LoadFrom(ctx, id, info.Cursor)
		if err == nil {
			a.History = tail
			a.trimmedTotal = info.Cursor
			if info.Content != "" {
				a.summaryMsg = &provider.Message{Role: provider.RoleUser, Content: info.Content}
			}
			return nil
		}
		if !errors.Is(err, session.ErrStaleCursor) {
			return err
		}
		// Stale cursor — fall through to full Load below.
	}
	full, err := a.Store.Load(ctx, id)
	if err != nil {
		return err
	}
	a.History = full
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

const summarizePrompt = `Summarize the following conversation fragment concisely.
Preserve: file paths, key decisions, current task state, errors.
Output only the summary, no preamble.`

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
	var (
		newHistory []provider.Message
		final      string
		stepErr    error
	)
	forceCompress := false
	for i := 0; i < max && stepErr == nil && final == ""; i++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		newHistory, final, stepErr = a.step(ctx, history, forceCompress)
		if errors.Is(stepErr, provider.ErrContextLength) {
			forceCompress = true
			stepErr = nil
			continue
		}
		history = newHistory
		forceCompress = false
	}

	if stepErr != nil {
		return "", stepErr
	}

	if final != "" {
		a.History = history
		a.saveOnSuccess(ctx, history)
		return final, nil
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
			slog.Warn("saveOnSuccess: NewID failed", "err", err)
			return
		}
		a.sessionID = id
	}
	if len(history) <= a.sessionOffset {
		return
	}
	offset := a.trimmedTotal + a.sessionOffset
	if err := a.Store.Append(ctx, a.sessionID, offset, history[a.sessionOffset:]); err != nil {
		// Failure here means we have new messages in memory that
		// did NOT make it to disk. Without this log, the user
		// would silently lose them on next Resume (issue 001).
		slog.Error("saveOnSuccess: Append failed; new messages in memory but not on disk",
			"err", err, "sessionID", a.sessionID, "delta", len(history)-a.sessionOffset)
		return
	}
	if a.summaryMsg != nil {
		// Best-effort: summary persistence failure doesn't roll
		// back the history append. Worst case: Resume loses the
		// summary and re-summarizes lazily.
		tokens := provider.EstimateTokensDefault(a.summaryMsg.Content)
		// Cursor must be the absolute on-disk position covered
		// by the current summary; committedPrefix is relative
		// to the in-memory slice (zero after a prune).
		absCursor := a.trimmedTotal + a.committedPrefix
		if err := a.Store.SaveSummary(ctx, a.sessionID, session.SummaryInfo{
			Content: a.summaryMsg.Content,
			Cursor:  absCursor,
			Tokens:  tokens,
		}); err != nil {
			slog.Warn("saveOnSuccess: SaveSummary failed", "err", err, "sessionID", a.sessionID)
		}
	} else if a.committedPrefix > 0 {
		// No summary yet (e.g. summarization disabled during this
		// session) but the window has slid forward. Persist the
		// cursor so Resume knows how many leading messages are
		// already committed to storage and need not be re-sent
		// to the LLM or re-summarized.
		absCursor := a.trimmedTotal + a.committedPrefix
		if err := a.Store.SaveSummary(ctx, a.sessionID, session.SummaryInfo{
			Cursor: absCursor,
		}); err != nil {
			slog.Warn("saveOnSuccess: SaveSummary(cursor only) failed", "err", err, "sessionID", a.sessionID)
		}
	}
	a.sessionOffset = len(history)
	a.pruneHistory()
}

// pruneHistory releases committedPrefix messages from the head
// of a.History. Those messages are already on disk and are never
// sent to the LLM again, so keeping them in memory is pure waste.
// sessionOffset and committedPrefix are adjusted to keep the
// remaining slice consistent; trimmedTotal accumulates the
// absolute on-disk position so future SaveSummary calls remain
// cumulative even after repeated prunes.
func (a *reActAgent) pruneHistory() {
	n := a.committedPrefix
	if n <= 0 || n > len(a.History) {
		return
	}
	a.History = a.History[n:]
	a.committedPrefix = 0
	a.sessionOffset = max(0, a.sessionOffset-n)
	a.trimmedTotal += n
	slog.Info("pruneHistory: released committed prefix", "dropped", n, "remaining", len(a.History))
}

// Reset implements Agent. It drops the in-memory
// conversation history, deletes the current session
// file (if any), and resets the session offset so the
// next Run starts a fresh conversation with a new
// session id. The agent's Provider, Registry, Cfg, and
// Store are unaffected.
func (a *reActAgent) Reset() {
	a.clearSessionState()
	if a.Store != nil && a.sessionID != "" {
		if err := a.Store.Delete(context.Background(), a.sessionID); err != nil {
			slog.Warn("Reset: Store.Delete failed; on-disk session will linger",
				"err", err, "sessionID", a.sessionID)
		}
	}
	a.sessionID = ""
	a.sessionOffset = 0
}

// clearSessionState resets the per-conversation fields (history
// and summary-related offsets) to their zero values. The
// backing array of a.History is retained so subsequent
// appends can reuse the capacity without re-allocating.
func (a *reActAgent) clearSessionState() {
	a.History = a.History[:0]
	a.summaryMsg = nil
	a.committedPrefix = 0
	a.trimmedTotal = 0
}

// step performs one ReAct iteration: apply the sliding
// window to `history` to get the LLM view, send it to the
// provider, dispatch any tool calls, and append the
// assistant + tool messages back to `history`. The
// returned slice is the same backing array (possibly
// grown via append) — the caller uses it as the input
// to the next step or as the final committed history.
//
// Windowing happens internally: the LLM never sees more
// than MaxContextTokens worth of context, but the
// cumulative `history` always grows. This keeps a
// single backing array growing in place instead of
// allocating a fresh view + newHistory per step.
//
// Tool execution errors are NOT returned as Go errors — they
// are embedded in the appended tool message (Content set to
// the error string) so the LLM can decide how to react.
// Provider / Validate errors ARE returned as Go errors and
// abort the loop; the caller surfaces them to the user.
//
// When forceCompress is true, the budget check is skipped: the
// view is always compressed (via summarize or dropOldestTurns)
// before being sent to the LLM. Used by the reactive path when
// the previous step returned ErrContextLength.
func (a *reActAgent) step(ctx context.Context, history []provider.Message, forceCompress bool) ([]provider.Message, string, error) {
	// Apply the sliding window. The returned view is a
	// sub-slice of history when windowing was needed; trim
	// is committed only after all fallible operations succeed,
	// so a failed step never leaves a.committedPrefix out of sync.
	view, trim, err := a.applyWindow(ctx, history, forceCompress)
	if err != nil {
		return nil, "", err
	}

	req := provider.Request{
		System:      a.Cfg.System,
		Messages:    view,
		Tools:       a.Registry.Specs(),
		Model:       a.Cfg.Model,
		Temperature: a.Cfg.Temperature,
		MaxTokens:   a.Cfg.MaxTokens,
	}
	if err := req.Validate(); err != nil {
		return nil, "", fmt.Errorf("agent: invalid request: %w", err)
	}
	slog.Debug("agent.Chat: request",
		"model", req.Model,
		"temperature", req.Temperature,
		"maxTokens", req.MaxTokens,
		"toolCount", len(req.Tools),
		"system", req.System,
		"messages", req.Messages,
		"tools", req.Tools,
	)
	resp, err := a.Provider.Chat(ctx, req)
	if err != nil {
		return nil, "", fmt.Errorf("agent: chat: %w", err)
	}

	// Append the assistant message. When the
	// underlying array has cap to spare this is free.
	history = append(history, NewAssistantMessage(resp.Content, resp.ToolCalls))

	if len(resp.ToolCalls) == 0 {
		a.commitTrim(trim)
		return history, resp.Content, nil
	}

	for _, call := range resp.ToolCalls {
		result, err := a.Registry.Invoke(ctx, call.Name, call.Arguments)
		if err != nil {
			result = err.Error()
		}
		history = append(history, NewToolMessage(call.ID, call.Name, result))
	}
	a.commitTrim(trim)
	return history, "", nil
}

// commitTrim advances a.committedPrefix to reflect how many
// messages from the head of history have been dropped by
// the latest applyWindow. Called only after all fallible
// operations in step succeed.
func (a *reActAgent) commitTrim(trim int) {
	a.committedPrefix = trim
}

// summarizeHistory calls the LLM to summarize the given
// messages into a single user-role summary message. The
// caller decides where to place the summary in history.
// If a.summaryMsg is non-nil it is prepended to history so
// the LLM has the prior summary as context and the new
// summary is incremental rather than recomputing from scratch.
// Returns the summary as a RoleUser message.
func (a *reActAgent) summarizeHistory(ctx context.Context, history []provider.Message) (provider.Message, error) {
	msgs := history
	if a.summaryMsg != nil {
		msgs = append([]provider.Message{*a.summaryMsg}, history...)
	}
	fragment := serializeHistoryFragment(msgs)
	req := provider.Request{
		System:    summarizePrompt,
		Messages:  []provider.Message{NewUserMessage(fragment)},
		Model:     a.Cfg.Model,
		MaxTokens: 1024,
	}
	if err := req.Validate(); err != nil {
		return provider.Message{}, fmt.Errorf("agent: summarize request: %w", err)
	}
	slog.Debug("agent.summarize: request",
		"model", req.Model,
		"system", req.System,
		"messages", req.Messages,
	)
	resp, err := a.Provider.Chat(ctx, req)
	if err != nil {
		return provider.Message{}, fmt.Errorf("agent: summarize: %w", err)
	}
	return provider.Message{Role: provider.RoleUser, Content: StripThink(resp.Content)}, nil
}

// contextBudget returns the effective input-side token
// budget. It starts from MaxContextTokens, applies the
// safety margin, then subtracts MaxTokens so the output
// reservation is honored (output also occupies the
// context window on every mainstream LLM — the KV cache
// holds the full sequence). MaxTokens ≤ 0 means "use
// provider default"; we can't know that size, so we
// don't try to subtract it. Users who hit output-side
// limits under the default should set MaxTokens
// explicitly. Values are clamped: negative
// MaxContextTokens → 0, frac outside [0,1) → nearest
// valid edge, final budget ≥ 0.
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
	if a.Cfg.MaxTokens > 0 {
		budget -= a.Cfg.MaxTokens
	}
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

// summaryEnabled returns whether LLM-based summarization is
// active. The field is interpreted as an opt-out: zero
// (unset) means enabled, matching the spec ("default true;
// false disables summarization").
func (a *reActAgent) summaryEnabled() bool {
	return !a.Cfg.SummaryDisabled
}

// applyWindow returns a windowed view of history that
// fits within the configured context budget; oldest
// whole turns are dropped otherwise. A single turn
// that alone exceeds the budget is left for the
// safety net.
//
// The second layer of context management: when the sliding window
// alone would drop turns, summarization (if !SummaryDisabled) preserves
// information from those turns before they are dropped.
//
// When forceCompress is true, the budget check is bypassed: the
// view is always compressed (via summarize or dropOldestTurns)
// before returning. Used by the reactive ErrContextLength path
// to retry without re-checking budget.
//
// Returns (view, trim). trim is the count of leading messages
// from history that the view excludes — the caller commits it
// as a.committedPrefix. The committedPrefix cache means early
// history isn't re-scanned on every step; it moves forward
// monotonically since budget is fixed mid-session. Reset and
// Resume clear it.
func (a *reActAgent) applyWindow(ctx context.Context, history []provider.Message, forceCompress bool) ([]provider.Message, int, error) {
	budget := a.contextBudget()
	candidate := history[a.committedPrefix:]

	view := candidate
	if a.summaryMsg != nil {
		view = append([]provider.Message{*a.summaryMsg}, candidate...)
	}
	if !forceCompress && a.estimateHistoryTokens(view) <= budget {
		return view, a.committedPrefix, nil
	}

	// Budget exceeded. Try LLM summarization first.
	if !a.Cfg.SummaryDisabled {
		if split := a.splitPoint(candidate); split > 0 {
			slog.Info("applyWindow: budget exceeded, attempting summary",
				"candidateTokens", a.estimateHistoryTokens(candidate), "budget", budget, "split", split)
			summary, err := a.summarizeHistory(ctx, candidate[:split])
			if err != nil {
				return nil, 0, fmt.Errorf("agent: summarize: %w", err)
			}
			reduced := append([]provider.Message{summary}, candidate[split:]...)
			reducedTokens := a.estimateHistoryTokens(reduced)
			if reducedTokens <= budget {
				slog.Info("applyWindow: summary accepted",
					"summaryTokens", reducedTokens, "split", split)
				a.summaryMsg = &summary
				a.committedPrefix += split
				return reduced, a.committedPrefix, nil
			}
			// Summary still too big — discard. Leave a.summaryMsg and
			// a.committedPrefix untouched: nothing was actually committed.
			slog.Warn("applyWindow: summary discarded (still over budget); falling back to drop",
				"summaryTokens", reducedTokens, "budget", budget)
		}
	}

	result := dropOldestTurns(candidate, budget, a.estimateHistoryTokens)
	slog.Info("applyWindow: dropping oldest turns",
		"dropped", len(candidate)-len(result), "remaining", len(result), "forceCompress", forceCompress)
	return result, len(history) - len(result), nil
}

// splitPoint returns the index into candidate that divides it
// at a turn boundary for summarization. Returns 0 if no valid
// split exists (single turn — summarization won't help).
func (a *reActAgent) splitPoint(candidate []provider.Message) int {
	ends := turnEnds(candidate)
	if len(ends) == 0 {
		return 0
	}
	target := len(candidate) * 2 / 3
	i, found := slices.BinarySearch(ends, target)
	if found {
		return ends[i]
	}
	if i > 0 {
		return ends[i-1]
	}
	return 0
}

// dropOldestTurns removes whole turns from the head
// until the estimate fits the budget. A turn is a
// user message + everything up to the next user
// message.
func dropOldestTurns(history []provider.Message, budget int, estimate func([]provider.Message) int) []provider.Message {
	ends := turnEnds(history)
	if len(ends) == 0 {
		return history
	}
	// Walk from the oldest turn to the newest (longest candidate to
	// shortest); the first fit is the longest prefix that fits.
	for _, end := range ends {
		if candidate := history[end:]; estimate(candidate) <= budget {
			return candidate
		}
	}
	// Nothing fits; return the shortest tail (last turn only).
	return history[ends[len(ends)-1]:]
}

// turnEnds returns the indices of user messages after the first
// — valid cut points for dropping whole turns from the head of
// history while preserving turn integrity. The first user message
// is not included because dropping everything before it would
// leave nothing, and dropping it without the assistant/tool
// messages that follow would break the turn.
func turnEnds(history []provider.Message) []int {
	var ends []int
	seen := 0
	for i, m := range history {
		if m.Role == provider.RoleUser {
			if seen == 1 {
				ends = append(ends, i)
			}
			seen = 1
		}
	}
	return ends
}

// serializeHistoryFragment formats the given messages as a
// plain-text string suitable for passing to an LLM as the
// summarization input. No JSON; just role-prefixed lines.
func serializeHistoryFragment(msgs []provider.Message) string {
	if len(msgs) == 0 {
		return ""
	}
	var b strings.Builder
	b.Grow(len(msgs) * 64) // heuristic: avoid early reallocation
	const sep = "\n"
	for _, m := range msgs {
		if m.Role == provider.RoleTool {
			b.WriteString("[tool]: ")
			b.WriteString(m.ToolCallID)
			b.WriteString(" → ")
			b.WriteString(m.Content)
			b.WriteString(sep)
		} else {
			b.WriteString(roleTag(m.Role))
			b.WriteString(": ")
			b.WriteString(m.Content)
			b.WriteString(sep)
		}
		for _, tc := range m.ToolCalls {
			b.WriteString("[tool]: ")
			b.WriteString(tc.ID)
			b.WriteByte('/')
			b.WriteString(tc.Name)
			b.WriteString(" → ")
			b.Write(tc.Arguments)
			b.WriteString(sep)
		}
	}
	return b.String()
}

// roleTag returns the human-readable tag for a message role.
func roleTag(role provider.Role) string {
	switch role {
	case provider.RoleUser:
		return "[user]"
	case provider.RoleAssistant:
		return "[assistant]"
	case provider.RoleTool:
		return "[tool]"
	case provider.RoleSystem:
		return "[system]"
	default:
		return "[unknown]"
	}
}
