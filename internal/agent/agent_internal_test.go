package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	agentfake "chaosbot/internal/agent/fake"
	"chaosbot/internal/provider"
	providerfake "chaosbot/internal/provider/fake"
	"chaosbot/internal/session"
)

// newTestAgent wires a minimal reActAgent backed by
// providerfake.Provider and a registry seeded with the given
// tools (name -> response). Internal-test access means we
// build the concrete type directly without going through New
// (no-arg DI constructor) — the di library can't reach us
// here, but we don't need it.
func newTestAgent(t *testing.T, tools map[string]string) (*reActAgent, *providerfake.Provider) {
	t.Helper()
	reg := NewRegistry()
	for name, resp := range tools {
		name, resp := name, resp
		reg.Register(&agentfake.Tool{
			NameStr: name,
			InvokeFunc: func(_ context.Context, _ json.RawMessage) (string, error) {
				return resp, nil
			},
		})
	}
	fp := &providerfake.Provider{NameStr: "test"}
	a := &reActAgent{
		Provider: fp,
		Registry: reg,
		Cfg: Config{
			System: "you are a helper",
			Model:  "test-model",
		},
	}
	return a, fp
}

func TestStep_FinalAnswerNoTools(t *testing.T) {
	a, fp := newTestAgent(t, nil)
	fp.NextResp = &provider.Response{Content: "the answer"}

	history := []provider.Message{NewUserMessage("hi")}
	newHistory, final, err := a.step(context.Background(), history, false)
	if err != nil {
		t.Fatalf("step: %v", err)
	}
	if final != "the answer" {
		t.Errorf("final = %q, want %q", final, "the answer")
	}
	if len(newHistory) != 2 {
		t.Fatalf("len(newHistory) = %d, want 2 (user + assistant)", len(newHistory))
	}
	assistant := newHistory[1]
	if assistant.Role != provider.RoleAssistant {
		t.Errorf("newHistory[1].Role = %q, want assistant", assistant.Role)
	}
	if assistant.Content != "the answer" {
		t.Errorf("newHistory[1].Content = %q", assistant.Content)
	}
	if len(assistant.ToolCalls) != 0 {
		t.Errorf("newHistory[1].ToolCalls = %+v, want empty", assistant.ToolCalls)
	}
	if fp.Calls != 1 {
		t.Errorf("fp.Calls = %d, want 1", fp.Calls)
	}
}

func TestStep_ToolCallsAppendToolMessages(t *testing.T) {
	a, fp := newTestAgent(t, map[string]string{
		"echo": "echoed",
		"time": "12:00",
	})
	fp.NextResp = &provider.Response{
		Content: "",
		ToolCalls: []provider.ToolCall{
			{ID: "call-1", Name: "echo", Arguments: json.RawMessage(`{}`)},
			{ID: "call-2", Name: "time", Arguments: json.RawMessage(`{}`)},
		},
	}

	history := []provider.Message{NewUserMessage("hi")}
	newHistory, final, err := a.step(context.Background(), history, false)
	if err != nil {
		t.Fatalf("step: %v", err)
	}
	if final != "" {
		t.Errorf("final = %q, want empty (loop should continue)", final)
	}
	if len(newHistory) != 4 {
		t.Fatalf("len(newHistory) = %d, want 4", len(newHistory))
	}
	if newHistory[2].Role != provider.RoleTool {
		t.Errorf("newHistory[2].Role = %q, want tool", newHistory[2].Role)
	}
	if newHistory[2].ToolCallID != "call-1" || newHistory[2].Name != "echo" {
		t.Errorf("newHistory[2] = %+v, want call-1/echo", newHistory[2])
	}
	if newHistory[3].ToolCallID != "call-2" || newHistory[3].Name != "time" {
		t.Errorf("newHistory[3] = %+v, want call-2/time", newHistory[3])
	}
}

func TestStep_ToolErrorEmbeddedInMessage(t *testing.T) {
	wantErr := errors.New("boom")
	reg := NewRegistry()
	reg.Register(&agentfake.Tool{
		NameStr: "boom",
		InvokeFunc: func(_ context.Context, _ json.RawMessage) (string, error) {
			return "", wantErr
		},
	})
	a := &reActAgent{
		Provider: &providerfake.Provider{
			NameStr: "test",
			NextResp: &provider.Response{
				ToolCalls: []provider.ToolCall{{ID: "c", Name: "boom", Arguments: json.RawMessage(`{}`)}},
			},
		},
		Registry: reg,
		Cfg:      Config{Model: "m"},
	}
	history := []provider.Message{NewUserMessage("go")}
	newHistory, final, err := a.step(context.Background(), history, false)
	if err != nil {
		t.Fatalf("step: %v, tool errors should NOT bubble up as Go errors", err)
	}
	if final != "" {
		t.Errorf("final = %q, want empty (loop continues)", final)
	}
	if len(newHistory) != 3 {
		t.Fatalf("len(newHistory) = %d, want 3", len(newHistory))
	}
	tool := newHistory[2]
	if tool.Role != provider.RoleTool {
		t.Errorf("Role = %q, want tool", tool.Role)
	}
	if tool.Content != "boom" {
		t.Errorf("Content = %q, want %q (error string embedded)", tool.Content, "boom")
	}
}

func TestStep_ProviderErrorBubblesUp(t *testing.T) {
	wantErr := errors.New("provider down")
	a := &reActAgent{
		Provider: &providerfake.Provider{NameStr: "test", NextErr: wantErr},
		Registry: NewRegistry(),
		Cfg:      Config{Model: "m"},
	}
	_, _, err := a.step(context.Background(), []provider.Message{NewUserMessage("x")}, false)
	if !errors.Is(err, wantErr) {
		t.Errorf("err = %v, want wraps %v", err, wantErr)
	}
}

func TestStep_ValidateFailsBubblesUp(t *testing.T) {
	a := &reActAgent{
		Provider: &providerfake.Provider{NameStr: "test"},
		Registry: NewRegistry(),
		Cfg: Config{
			System: "you are a helper",
			Model:  "m",
		},
	}
	history := []provider.Message{
		{Role: provider.RoleSystem, Content: "another system msg"},
	}
	_, _, err := a.step(context.Background(), history, false)
	if !errors.Is(err, provider.ErrSystemConflict) {
		t.Errorf("err = %v, want wraps ErrSystemConflict", err)
	}
}

func TestStep_PassesSystemAndToolsToProvider(t *testing.T) {
	a, fp := newTestAgent(t, map[string]string{"echo": "ok"})
	fp.NextResp = &provider.Response{Content: "done"}

	_, _, err := a.step(context.Background(), []provider.Message{NewUserMessage("hi")}, false)
	if err != nil {
		t.Fatalf("step: %v", err)
	}
	if fp.LastReq.System != "you are a helper" {
		t.Errorf("LastReq.System = %q, want %q", fp.LastReq.System, "you are a helper")
	}
	if fp.LastReq.Model != "test-model" {
		t.Errorf("LastReq.Model = %q", fp.LastReq.Model)
	}
	if len(fp.LastReq.Tools) != 1 || fp.LastReq.Tools[0].Name != "echo" {
		t.Errorf("LastReq.Tools = %+v, want one 'echo'", fp.LastReq.Tools)
	}
	if len(fp.LastReq.Messages) != 1 || fp.LastReq.Messages[0].Content != "hi" {
		t.Errorf("LastReq.Messages = %+v, want [user 'hi']", fp.LastReq.Messages)
	}
}

// TestRun_ContextCanceled_DoesNotMutateHistory verifies that
// a Run that bails on a pre-canceled context leaves a.History
// untouched — the candidate user message is built on a
// local slice, never assigned back to a.History, so the
// next Run starts from the same point.
func TestRun_ContextCanceled_DoesNotMutateHistory(t *testing.T) {
	a, _ := newTestAgent(t, nil)
	// Seed a successful Run so a.History is non-empty.
	prevHistory := []provider.Message{NewUserMessage("prior"), NewAssistantMessage("ok", nil)}
	a.History = append([]provider.Message(nil), prevHistory...)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := a.Run(ctx, "this should not stick")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if len(a.History) != len(prevHistory) {
		t.Errorf("len(a.History) = %d, want %d (canceled Run must not append)", len(a.History), len(prevHistory))
	}
	for i := range prevHistory {
		if a.History[i].Content != prevHistory[i].Content {
			t.Errorf("a.History[%d] mutated: got %q, want %q", i, a.History[i].Content, prevHistory[i].Content)
		}
	}
}

// TestRun_MaxSteps_DoesNotMutateHistory verifies that
// exhausting MaxSteps without a final answer leaves
// a.History untouched. The provider keeps emitting tool
// calls, the loop runs out of budget, and the agent must
// NOT commit the user message.
func TestRun_MaxSteps_DoesNotMutateHistory(t *testing.T) {
	a, fp := newTestAgent(t, map[string]string{"echo": "x"})
	toolCall := providerfake.Call{Resp: &provider.Response{
		ToolCalls: []provider.ToolCall{{ID: "1", Name: "echo", Arguments: json.RawMessage(`{}`)}},
	}}
	fp.Script = []providerfake.Call{toolCall, toolCall, toolCall}
	a.Cfg.MaxSteps = 3

	prevLen := len(a.History)
	_, err := a.Run(context.Background(), "hi")
	if !errors.Is(err, ErrMaxSteps) {
		t.Errorf("err = %v, want wraps ErrMaxSteps", err)
	}
	if len(a.History) != prevLen {
		t.Errorf("len(a.History) = %d, want %d (failed Run must not append)", len(a.History), prevLen)
	}
}

// TestRun_ProviderError_DoesNotMutateHistory verifies that
// a provider Chat error in the very first step leaves
// a.History untouched.
func TestRun_ProviderError_DoesNotMutateHistory(t *testing.T) {
	a, fp := newTestAgent(t, nil)
	fp.NextErr = errors.New("llm down")

	prevLen := len(a.History)
	_, err := a.Run(context.Background(), "hi")
	if err == nil {
		t.Fatal("want error when provider fails")
	}
	if len(a.History) != prevLen {
		t.Errorf("len(a.History) = %d, want %d (failed Run must not append)", len(a.History), prevLen)
	}
}

// TestEstimateHistoryTokens asserts the per-message
// sum plus actual tool call argument estimation.
func TestEstimateHistoryTokens(t *testing.T) {
	a, _ := newTestAgent(t, nil)

	t.Run("content_only", func(t *testing.T) {
		hist := []provider.Message{
			{Role: provider.RoleUser, Content: "hello world"},
			{Role: provider.RoleAssistant, Content: "ok"},
		}
		got := a.estimateHistoryTokens(hist)
		// "hello world" ≈ 4 tokens, "ok" ≈ 1 token
		if got < 3 || got > 10 {
			t.Errorf("estimate = %d, want roughly 5", got)
		}
	})

	t.Run("empty_args", func(t *testing.T) {
		hist := []provider.Message{
			{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{Name: "x"}}},
		}
		got := a.estimateHistoryTokens(hist)
		if got != 0 {
			t.Errorf("estimate = %d, want 0 (empty args)", got)
		}
	})

	t.Run("nonempty_args", func(t *testing.T) {
		args := json.RawMessage(`{"path":"/foo/bar","content":"hello"}`)
		hist := []provider.Message{
			{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{Name: "x", Arguments: args}}},
		}
		got := a.estimateHistoryTokens(hist)
		if got < 3 || got > 15 {
			t.Errorf("estimate = %d, want roughly 8", got)
		}
	})
}

// TestApplyWindow_NoOpWhenUnderBudget verifies history at
// or below the budget is returned unchanged.
func TestApplyWindow_NoOpWhenUnderBudget(t *testing.T) {
	a, _ := newTestAgent(t, nil)
	a.Cfg.MaxContextTokens = 1000 // generous
	a.Cfg.SafetyMarginFraction = 0.10
	hist := []provider.Message{
		NewUserMessage("hi"),
		NewAssistantMessage("ok", nil),
	}
	out, _, err := a.applyWindow(context.Background(), hist, false)
	if err != nil {
		t.Fatalf("applyWindow: %v", err)
	}
	if len(out) != len(hist) {
		t.Errorf("len = %d, want %d (no-op)", len(out), len(hist))
	}
}

// TestApplyWindow_DropsOldestTurn verifies basic
// windowing: with 3 turns and a 2-turn budget, the
// oldest turn is dropped.
func TestApplyWindow_DropsOldestTurn(t *testing.T) {
	a, _ := newTestAgent(t, nil)
	a.Cfg.MaxContextTokens = 50
	a.Cfg.SafetyMarginFraction = 0.0
	a.Cfg.SummaryDisabled = true // exercise pure dropping, not summary
	big := strings.Repeat("a", 100)
	hist := []provider.Message{
		NewUserMessage(big),
		NewAssistantMessage("a1", nil),
		NewUserMessage(big),
		NewAssistantMessage("a2", nil),
		NewUserMessage(big),
		NewAssistantMessage("a3", nil),
	}
	out, _, err := a.applyWindow(context.Background(), hist, false)
	if err != nil {
		t.Fatalf("applyWindow: %v", err)
	}
	if len(out) >= len(hist) {
		t.Errorf("len = %d, want < %d", len(out), len(hist))
	}
	if out[0].Content != big {
		t.Errorf("out[0] is not the second user prompt")
	}
}

// TestApplyWindow_NoOpWhenMaxContextTokensIsZero verifies
// that the zero value for MaxContextTokens (unset
// config) gets the default budget and does no windowing
// on small histories.
func TestApplyWindow_NoOpWhenMaxContextTokensIsZero(t *testing.T) {
	a, _ := newTestAgent(t, nil)
	a.Cfg.MaxContextTokens = 0 // unset
	hist := []provider.Message{NewUserMessage("hi"), NewAssistantMessage("ok", nil)}
	out, _, err := a.applyWindow(context.Background(), hist, false)
	if err != nil {
		t.Fatalf("applyWindow: %v", err)
	}
	if len(out) != len(hist) {
		t.Errorf("len = %d, want %d (no-op on tiny history)", len(out), len(hist))
	}
}

func TestContextBudget_Defaults(t *testing.T) {
	a, _ := newTestAgent(t, nil)
	a.Cfg.MaxContextTokens = 0 // unset → default 128k
	a.Cfg.SafetyMarginFraction = 0
	got := a.contextBudget()
	want := int(128_000 * 0.90) // default frac = 0.10
	if got != want {
		t.Errorf("contextBudget() = %d, want %d", got, want)
	}
}

func TestContextBudget_NegativeMaxClampsToDefault(t *testing.T) {
	a, _ := newTestAgent(t, nil)
	a.Cfg.MaxContextTokens = -100
	a.Cfg.SafetyMarginFraction = 0
	got := a.contextBudget()
	want := int(128_000 * 0.90)
	if got != want {
		t.Errorf("contextBudget() = %d, want %d (negative max → default)", got, want)
	}
}

func TestContextBudget_FracBelowZeroClampsToZero(t *testing.T) {
	a, _ := newTestAgent(t, nil)
	a.Cfg.MaxContextTokens = 1000
	a.Cfg.SafetyMarginFraction = -0.5
	got := a.contextBudget()
	want := 1000 // frac clamped to 0, budget = max
	if got != want {
		t.Errorf("contextBudget() = %d, want %d (negative frac → 0)", got, want)
	}
}

func TestContextBudget_FracAboveOneClampsToDefault(t *testing.T) {
	a, _ := newTestAgent(t, nil)
	a.Cfg.MaxContextTokens = 1000
	a.Cfg.SafetyMarginFraction = 1.5
	got := a.contextBudget()
	want := int(1000 * 0.90) // frac clamped to default 0.10
	if got != want {
		t.Errorf("contextBudget() = %d, want %d (frac>1 → default)", got, want)
	}
}

// TestContextBudget_SubtractsMaxTokens verifies the output
// reservation: with MaxTokens set, the input-side budget
// is reduced by that amount. This matters because output
// also occupies the context window (KV cache holds the
// full sequence on every mainstream LLM), so without
// this subtraction we'd happily send a near-max input and
// trip the provider's combined input+output limit on
// the response.
func TestContextBudget_SubtractsMaxTokens(t *testing.T) {
	a, _ := newTestAgent(t, nil)
	a.Cfg.MaxContextTokens = 10_000
	a.Cfg.SafetyMarginFraction = -1 // negative → clamped to 0, margin disabled
	a.Cfg.MaxTokens = 2_000
	got := a.contextBudget()
	want := 8_000 // 10_000 − 2_000
	if got != want {
		t.Errorf("contextBudget() = %d, want %d (max − MaxTokens)", got, want)
	}
}

// TestContextBudget_MaxTokensZeroNotSubtracted verifies
// that MaxTokens ≤ 0 ("use provider default") does NOT
// subtract anything — we don't know the provider's
// default output size, so guessing would either
// over-reserve (wasting context) or under-reserve
// (re-introducing the bug). Users who need exact budgeting
// should set MaxTokens explicitly.
func TestContextBudget_MaxTokensZeroNotSubtracted(t *testing.T) {
	cases := []struct {
		name      string
		maxTokens int
	}{
		{"unset (0)", 0},
		{"negative (provider-default sentinel)", -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, _ := newTestAgent(t, nil)
			a.Cfg.MaxContextTokens = 10_000
			a.Cfg.SafetyMarginFraction = -1 // disable margin to make the math obvious
			a.Cfg.MaxTokens = tc.maxTokens
			got := a.contextBudget()
			want := 10_000
			if got != want {
				t.Errorf("contextBudget() = %d, want %d (MaxTokens not subtracted)", got, want)
			}
		})
	}
}

// TestContextBudget_MaxTokensLargerThanBudgetClampsToZero
// verifies that if MaxTokens alone exceeds the margin-
// adjusted budget, the final budget clamps to 0 instead
// of going negative — applyWindow will then drop
// everything it can but still leave the smallest single
// turn for the safety net (existing behavior).
func TestContextBudget_MaxTokensLargerThanBudgetClampsToZero(t *testing.T) {
	a, _ := newTestAgent(t, nil)
	a.Cfg.MaxContextTokens = 1_000
	a.Cfg.SafetyMarginFraction = 0
	a.Cfg.MaxTokens = 5_000
	got := a.contextBudget()
	if got != 0 {
		t.Errorf("contextBudget() = %d, want 0 (clamped)", got)
	}
}

func TestSummarizeHistory_Basic(t *testing.T) {
	a, fp := newTestAgent(t, nil)
	fp.NextResp = &provider.Response{Content: "summarized early turns"}
	hist := []provider.Message{
		NewUserMessage("hello"),
		NewAssistantMessage("world", nil),
	}
	msg, err := a.summarizeHistory(context.Background(), hist)
	if err != nil {
		t.Fatalf("summarizeHistory: %v", err)
	}
	if msg.Role != provider.RoleUser {
		t.Errorf("msg.Role = %q, want user", msg.Role)
	}
	if msg.Content != "summarized early turns" {
		t.Errorf("msg.Content = %q, want %q", msg.Content, "summarized early turns")
	}
}

func TestSummarizeHistory_ProviderError(t *testing.T) {
	a, fp := newTestAgent(t, nil)
	fp.NextErr = errors.New("provider down")
	_, err := a.summarizeHistory(context.Background(), []provider.Message{NewUserMessage("x")})
	if err == nil {
		t.Fatal("want error when provider fails")
	}
}

func TestReset_ClearsSummaryFields(t *testing.T) {
	a, _ := newTestAgent(t, nil)
	a.History = []provider.Message{NewUserMessage("old")}
	a.summaryMsg = &provider.Message{Role: provider.RoleUser, Content: "summary"}
	a.committedPrefix = 3
	a.Reset()
	if a.summaryMsg != nil {
		t.Errorf("summaryMsg = %v, want nil", a.summaryMsg)
	}
	if a.committedPrefix != 0 {
		t.Errorf("committedPrefix = %d, want 0", a.committedPrefix)
	}
}

func TestResume_ClearsSummaryFields(t *testing.T) {
	fs, err := session.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	id := "session-with-summary"
	fs.Append(context.Background(), id, []provider.Message{NewUserMessage("old")})
	a := &reActAgent{
		Provider:   providerfake.New("test"),
		Registry:   NewRegistry(),
		Cfg:        Config{},
		Store:      fs,
		summaryMsg: &provider.Message{Role: provider.RoleUser, Content: "old summary"},
	}
	err = a.Resume(context.Background(), id)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if a.summaryMsg != nil {
		t.Errorf("summaryMsg = %v, want nil after Resume", a.summaryMsg)
	}
}

func TestSerializeHistoryFragment(t *testing.T) {
	msgs := []provider.Message{
		{Role: provider.RoleUser, Content: "hello"},
		{Role: provider.RoleAssistant, Content: "hi there", ToolCalls: []provider.ToolCall{
			{ID: "call-1", Name: "echo", Arguments: json.RawMessage(`{}`)},
		}},
		{Role: provider.RoleTool, Content: "tool result", ToolCallID: "call-1"},
	}
	got := serializeHistoryFragment(msgs)
	if !strings.Contains(got, "[user]: hello") {
		t.Errorf("missing user line: %s", got)
	}
	if !strings.Contains(got, "[assistant]: hi there") {
		t.Errorf("missing assistant line: %s", got)
	}
	if !strings.Contains(got, "[tool]: call-1/echo → {}") {
		t.Errorf("missing tool-call line: %s", got)
	}
	if !strings.Contains(got, "[tool]: call-1 → tool result") {
		t.Errorf("missing tool-result line: %s", got)
	}
}

func TestSerializeHistoryFragment_Empty(t *testing.T) {
	if serializeHistoryFragment(nil) != "" {
		t.Errorf("nil slice → want empty string")
	}
	if serializeHistoryFragment([]provider.Message{}) != "" {
		t.Errorf("empty slice → want empty string")
	}
}

func TestRoleTag(t *testing.T) {
	tests := []struct {
		role provider.Role
		want string
	}{
		{provider.RoleUser, "[user]"},
		{provider.RoleAssistant, "[assistant]"},
		{provider.RoleTool, "[tool]"},
		{provider.RoleSystem, "[system]"},
		{provider.Role("invalid"), "[unknown]"},
		{provider.Role(""), "[unknown]"},
		{provider.Role("ROLE_TOOL_RESULT"), "[unknown]"},
	}
	for _, tt := range tests {
		got := roleTag(tt.role)
		if got != tt.want {
			t.Errorf("roleTag(%q) = %q, want %q", tt.role, got, tt.want)
		}
	}
}
