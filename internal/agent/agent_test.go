package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/hyperchao/di"

	"chaosbot/internal/agent"
	"chaosbot/internal/agent/fake"
	"chaosbot/internal/provider"
	providerfake "chaosbot/internal/provider/fake"
	"chaosbot/internal/session"
)

// buildAgent wires a test agent via the di library. Per
// AGENTS.md: "For tests, build a fresh di.New() and register
// hand-written fakes." The agent package's no-arg New + the
// di:"type" tags on reActAgent's fields do the rest.
//
// The di library exposes Register/Get as package-level generics
// (di.RegisterDI[T](d, f), di.GetDI[T](d)); the per-container
// *DI value is passed explicitly so tests don't share state.
func buildAgent(t *testing.T, fp provider.Provider, reg *agent.Registry, cfg agent.Config) agent.Agent {
	t.Helper()
	c := di.New()
	di.RegisterDI(c, func() provider.Provider { return fp })
	di.RegisterDI(c, func() *agent.Registry { return reg })
	di.RegisterDI(c, func() agent.Config { return cfg })
	di.RegisterDI(c, func() session.Store { return session.NoopStore{} })
	di.RegisterDI(c, agent.New)
	return di.GetDI[agent.Agent](c)
}

// buildAgentWithStore is buildAgent + an explicit session.Store.
// Used by 06-3 tests that need to verify persistence.
func buildAgentWithStore(t *testing.T, fp provider.Provider, reg *agent.Registry, cfg agent.Config, store session.Store) agent.Agent {
	t.Helper()
	c := di.New()
	di.RegisterDI(c, func() provider.Provider { return fp })
	di.RegisterDI(c, func() *agent.Registry { return reg })
	di.RegisterDI(c, func() agent.Config { return cfg })
	di.RegisterDI(c, func() session.Store { return store })
	di.RegisterDI(c, agent.New)
	return di.GetDI[agent.Agent](c)
}

func TestRun_FinalAnswerFirstStep(t *testing.T) {
	fp := &providerfake.Provider{
		NameStr: "test",
		Script: []providerfake.Call{
			{Resp: &provider.Response{Content: "the answer"}},
		},
	}
	a := buildAgent(t, fp, agent.NewRegistry(), agent.Config{
		System:   "you are a helper",
		Model:    "test-model",
		MaxSteps: 5,
	})
	got, err := a.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got != "the answer" {
		t.Errorf("Run = %q, want %q", got, "the answer")
	}
	if fp.Calls != 1 {
		t.Errorf("Calls = %d, want 1", fp.Calls)
	}
	if len(fp.AllReqs) != 1 {
		t.Fatalf("AllReqs = %d, want 1", len(fp.AllReqs))
	}
	if fp.AllReqs[0].System != "you are a helper" {
		t.Errorf("AllReqs[0].System = %q, want %q", fp.AllReqs[0].System, "you are a helper")
	}
	if fp.AllReqs[0].Model != "test-model" {
		t.Errorf("AllReqs[0].Model = %q", fp.AllReqs[0].Model)
	}
}

func TestRun_TwoStepReActLoop(t *testing.T) {
	fp := &providerfake.Provider{
		NameStr: "test",
		Script: []providerfake.Call{
			{Resp: &provider.Response{
				Content: "",
				ToolCalls: []provider.ToolCall{
					{ID: "call-1", Name: "echo", Arguments: json.RawMessage(`{}`)},
				},
			}},
			{Resp: &provider.Response{Content: "final"}},
		},
	}
	reg := agent.NewRegistry()
	reg.Register(&fake.Tool{
		NameStr: "echo",
		InvokeFunc: func(_ context.Context, _ json.RawMessage) (string, error) {
			return "echoed", nil
		},
	})
	a := buildAgent(t, fp, reg, agent.Config{MaxSteps: 5})

	got, err := a.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got != "final" {
		t.Errorf("Run = %q, want %q", got, "final")
	}
	if fp.Calls != 2 {
		t.Errorf("Calls = %d, want 2", fp.Calls)
	}
	if len(fp.AllReqs) != 2 {
		t.Fatalf("AllReqs = %d, want 2", len(fp.AllReqs))
	}
	if len(fp.AllReqs[0].Messages) != 1 || fp.AllReqs[0].Messages[0].Content != "hi" {
		t.Errorf("AllReqs[0] messages = %+v, want [user 'hi']", fp.AllReqs[0].Messages)
	}
	if len(fp.AllReqs[1].Messages) != 3 {
		t.Errorf("AllReqs[1] messages len = %d, want 3", len(fp.AllReqs[1].Messages))
	}
	if fp.AllReqs[1].Messages[1].Role != provider.RoleAssistant {
		t.Errorf("AllReqs[1][1].Role = %q, want assistant", fp.AllReqs[1].Messages[1].Role)
	}
	if fp.AllReqs[1].Messages[2].Role != provider.RoleTool {
		t.Errorf("AllReqs[1][2].Role = %q, want tool", fp.AllReqs[1].Messages[2].Role)
	}
	if fp.AllReqs[1].Messages[2].Content != "echoed" {
		t.Errorf("AllReqs[1][2].Content = %q, want %q", fp.AllReqs[1].Messages[2].Content, "echoed")
	}
}

func TestRun_MaxStepsReached(t *testing.T) {
	toolCall := providerfake.Call{Resp: &provider.Response{
		ToolCalls: []provider.ToolCall{{ID: "1", Name: "echo", Arguments: json.RawMessage(`{}`)}},
	}}
	fp := &providerfake.Provider{
		NameStr: "test",
		Script:  []providerfake.Call{toolCall, toolCall, toolCall},
	}
	reg := agent.NewRegistry()
	reg.Register(&fake.Tool{
		NameStr: "echo",
		InvokeFunc: func(_ context.Context, _ json.RawMessage) (string, error) {
			return "x", nil
		},
	})
	a := buildAgent(t, fp, reg, agent.Config{MaxSteps: 3})

	_, err := a.Run(context.Background(), "hi")
	if !errors.Is(err, agent.ErrMaxSteps) {
		t.Errorf("err = %v, want wraps ErrMaxSteps", err)
	}
	if fp.Calls != 3 {
		t.Errorf("Calls = %d, want 3", fp.Calls)
	}
}

func TestRun_ContextCanceled(t *testing.T) {
	fp := &providerfake.Provider{
		NameStr: "test",
		Script: []providerfake.Call{
			{Resp: &provider.Response{Content: "should not reach"}},
		},
	}
	a := buildAgent(t, fp, agent.NewRegistry(), agent.Config{MaxSteps: 5})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := a.Run(ctx, "hi")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if fp.Calls != 0 {
		t.Errorf("Calls = %d, want 0 (pre-canceled ctx should bail before first Chat)", fp.Calls)
	}
}

// TestRun_MultiTurn_AccumulatesHistory verifies that consecutive
// Run calls feed the same agent instance, so the second turn's
// provider request contains the first turn's user + assistant
// messages plus the new user message.
func TestRun_MultiTurn_AccumulatesHistory(t *testing.T) {
	fp := providerfake.New("multi")
	fp.Script = []providerfake.Call{
		{Resp: &provider.Response{Content: "answer-1"}},
		{Resp: &provider.Response{Content: "answer-2"}},
	}
	a := buildAgent(t, fp, agent.NewRegistry(), agent.Config{MaxSteps: 1})

	reply1, err := a.Run(context.Background(), "q1")
	if err != nil {
		t.Fatalf("Run turn 1: %v", err)
	}
	if reply1 != "answer-1" {
		t.Errorf("turn 1 reply = %q, want %q", reply1, "answer-1")
	}

	reply2, err := a.Run(context.Background(), "q2")
	if err != nil {
		t.Fatalf("Run turn 2: %v", err)
	}
	if reply2 != "answer-2" {
		t.Errorf("turn 2 reply = %q, want %q", reply2, "answer-2")
	}

	if len(fp.AllReqs) != 2 {
		t.Fatalf("AllReqs len = %d, want 2", len(fp.AllReqs))
	}
	// Turn 1: agent sends just the user message.
	if got := len(fp.AllReqs[0].Messages); got != 1 {
		t.Errorf("turn 1 req.Messages len = %d, want 1", got)
	}
	// Turn 2: agent sends q1 + a1 + q2 (3 messages).
	if got := len(fp.AllReqs[1].Messages); got != 3 {
		t.Errorf("turn 2 req.Messages len = %d, want 3 (q1 + answer-1 + q2)", got)
	}
}

// TestRun_Reset_ClearsHistory verifies that Reset drops the
// in-memory history so the next Run starts a fresh
// conversation.
func TestRun_Reset_ClearsHistory(t *testing.T) {
	fp := providerfake.New("reset")
	fp.Script = []providerfake.Call{
		{Resp: &provider.Response{Content: "answer-1"}},
		{Resp: &provider.Response{Content: "answer-2"}},
	}
	a := buildAgent(t, fp, agent.NewRegistry(), agent.Config{MaxSteps: 1})

	if _, err := a.Run(context.Background(), "q1"); err != nil {
		t.Fatalf("Run turn 1: %v", err)
	}

	a.Reset()

	if _, err := a.Run(context.Background(), "q2"); err != nil {
		t.Fatalf("Run turn 2 after reset: %v", err)
	}

	if len(fp.AllReqs) != 2 {
		t.Fatalf("AllReqs len = %d, want 2", len(fp.AllReqs))
	}
	// After reset, turn 2 sees only the new user message.
	if got := len(fp.AllReqs[1].Messages); got != 1 {
		t.Errorf("turn 2 req.Messages len = %d, want 1 (post-reset)", got)
	}
}

// TestAgent_SessionID_BeforeRun verifies the agent starts
// with an empty session id (no successful Run yet).
func TestAgent_SessionID_BeforeRun(t *testing.T) {
	fp := providerfake.New("h0")
	a := buildAgent(t, fp, agent.NewRegistry(), agent.Config{})
	if id := a.SessionID(); id != "" {
		t.Errorf("SessionID = %q, want empty before first Run", id)
	}
}

// TestAgent_Run_AutoSavesToStore verifies that a successful
// Run generates a session id and appends the new turn to
// the store.
func TestAgent_Run_AutoSavesToStore(t *testing.T) {
	fp := providerfake.New("s0")
	fp.Script = []providerfake.Call{
		{Resp: &provider.Response{Content: "reply-1"}},
		{Resp: &provider.Response{Content: "reply-2"}},
	}
	fs, err := session.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	a := buildAgentWithStore(t, fp, agent.NewRegistry(), agent.Config{MaxSteps: 1}, fs)
	if _, err := a.Run(context.Background(), "q1"); err != nil {
		t.Fatalf("Run 1: %v", err)
	}
	id := a.SessionID()
	if id == "" {
		t.Fatal("SessionID empty after Run; want generated id")
	}
	hist, err := fs.Load(context.Background(), id)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(hist) != 2 {
		t.Fatalf("len(history) = %d, want 2", len(hist))
	}
	if hist[0].Content != "q1" || hist[1].Content != "reply-1" {
		t.Errorf("messages = %+v, want [q1, reply-1]", hist)
	}
	// Second Run should append, not overwrite.
	if _, err := a.Run(context.Background(), "q2"); err != nil {
		t.Fatalf("Run 2: %v", err)
	}
	hist, _ = fs.Load(context.Background(), id)
	if len(hist) != 4 {
		t.Errorf("len(history) after Run 2 = %d, want 4", len(hist))
	}
	if hist[2].Content != "q2" || hist[3].Content != "reply-2" {
		t.Errorf("second turn = %+v, want [q2, reply-2]", hist[2:4])
	}
}

// TestAgent_Run_NoOpStore_GeneratesSessionID verifies that
// with a NoopStore the agent still generates a session id
// (for display in REPL) but doesn't actually persist.
func TestAgent_Run_NoOpStore_GeneratesSessionID(t *testing.T) {
	fp := providerfake.New("ns")
	fp.Script = []providerfake.Call{
		{Resp: &provider.Response{Content: "ok"}},
	}
	a := buildAgent(t, fp, agent.NewRegistry(), agent.Config{MaxSteps: 1})
	reply, err := a.Run(context.Background(), "q")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if reply != "ok" {
		t.Errorf("reply = %q, want 'ok'", reply)
	}
	if a.SessionID() == "" {
		t.Error("SessionID should be set after Run, even with NoopStore")
	}
}

// TestAgent_Reset_DeletesSession verifies that Reset
// removes the on-disk session and clears the in-memory
// state, so the next Run starts a fresh session.
func TestAgent_Reset_DeletesSession(t *testing.T) {
	fp := providerfake.New("r0")
	fp.Script = []providerfake.Call{
		{Resp: &provider.Response{Content: "ok"}},
		{Resp: &provider.Response{Content: "ok"}},
	}
	fs, _ := session.NewFileStore(t.TempDir())
	a := buildAgentWithStore(t, fp, agent.NewRegistry(), agent.Config{MaxSteps: 1}, fs)
	if _, err := a.Run(context.Background(), "q1"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	id := a.SessionID()
	if id == "" {
		t.Fatal("SessionID empty after Run")
	}
	a.Reset()
	if a.SessionID() != "" {
		t.Errorf("SessionID after Reset = %q, want empty", a.SessionID())
	}
	// Session file should be gone.
	if _, err := fs.Load(context.Background(), id); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Load after Reset: err = %v, want os.ErrNotExist", err)
	}
	// Next Run creates a new session with a different id.
	if _, err := a.Run(context.Background(), "q2"); err != nil {
		t.Fatalf("Run 2: %v", err)
	}
	newID := a.SessionID()
	if newID == "" || newID == id {
		t.Errorf("new SessionID = %q, want different non-empty", newID)
	}
}

// TestAgent_Resume_LoadsAndContinues verifies that Resume
// loads a saved session, sets it as the in-memory history,
// and the next Run appends to the same id.
func TestAgent_Resume_LoadsAndContinues(t *testing.T) {
	fp := providerfake.New("res")
	fp.Script = []providerfake.Call{
		{Resp: &provider.Response{Content: "new-reply"}},
	}
	fs, _ := session.NewFileStore(t.TempDir())
	// Pre-populate a session.
	ctx := context.Background()
	prev := []provider.Message{
		{Role: provider.RoleUser, Content: "earlier"},
		{Role: provider.RoleAssistant, Content: "earlier-reply"},
	}
	if err := fs.Append(ctx, "sess-001", prev); err != nil {
		t.Fatalf("Append: %v", err)
	}
	a := buildAgentWithStore(t, fp, agent.NewRegistry(), agent.Config{MaxSteps: 1}, fs)
	if err := a.Resume(ctx, "sess-001"); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if a.SessionID() != "sess-001" {
		t.Errorf("SessionID after Resume = %q, want sess-001", a.SessionID())
	}
	if _, err := a.Run(ctx, "new question"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Provider should have seen the loaded + new messages.
	if len(fp.AllReqs) != 1 {
		t.Fatalf("AllReqs len = %d, want 1", len(fp.AllReqs))
	}
	msgs := fp.AllReqs[0].Messages
	if len(msgs) != 3 {
		t.Fatalf("msgs len = %d, want 3 (2 loaded + 1 new)", len(msgs))
	}
	if msgs[2].Content != "new question" {
		t.Errorf("new msg = %q, want 'new question'", msgs[2].Content)
	}
	// And the file should now have 4 messages (saved the new turn).
	hist, _ := fs.Load(ctx, "sess-001")
	if len(hist) != 4 {
		t.Errorf("file history len = %d, want 4", len(hist))
	}
}

// TestAgent_Resume_NotFound verifies Resume returns a
// clear error for missing sessions.
func TestAgent_Resume_NotFound(t *testing.T) {
	fp := providerfake.New("rnf")
	fs, _ := session.NewFileStore(t.TempDir())
	a := buildAgentWithStore(t, fp, agent.NewRegistry(), agent.Config{}, fs)
	err := a.Resume(context.Background(), "does-not-exist")
	if err == nil {
		t.Fatal("want error for missing session")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("err = %v, want wraps os.ErrNotExist", err)
	}
}

// TestAgent_Resume_NoStore verifies Resume without a
// Store returns a clear error.
func TestAgent_Resume_NoStore(t *testing.T) {
	fp := providerfake.New("rns")
	a := buildAgent(t, fp, agent.NewRegistry(), agent.Config{})
	err := a.Resume(context.Background(), "any-id")
	if err == nil {
		t.Fatal("want error when Store is nil")
	}
}

// TestAgent_Run_WindowingDoesNotBreakSessionOffset verifies
// that the sliding window (which drops old turns from the
// LLM view) does not affect what gets saved. The store
// must always have the cumulative full history, so
// sessionOffset stays in sync with len(a.History).
func TestAgent_Run_WindowingDoesNotBreakSessionOffset(t *testing.T) {
	fp := providerfake.New("w0")
	// 5 turns, each producing a reply.
	script := make([]providerfake.Call, 5)
	for i := range script {
		script[i] = providerfake.Call{Resp: &provider.Response{Content: "ok"}}
	}
	fp.Script = script
	fs, _ := session.NewFileStore(t.TempDir())
	// Tiny budget: windowing will drop old turns on every
	// step, so the LLM view is much smaller than the
	// cumulative history.
	a := buildAgentWithStore(t, fp, agent.NewRegistry(), agent.Config{
		MaxSteps:         1,
		MaxContextTokens: 10, // very small; forces aggressive dropping
	}, fs)
	for i := 0; i < 5; i++ {
		if _, err := a.Run(context.Background(), "q"); err != nil {
			t.Fatalf("Run %d: %v", i, err)
		}
	}
	id := a.SessionID()
	hist, err := fs.Load(context.Background(), id)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// 5 turns × 2 messages each = 10 cumulative.
	if len(hist) != 10 {
		t.Errorf("len(history) = %d, want 10 (5 turns × 2 messages)", len(hist))
	}
}

// TestResume_RestoresSummary verifies a persisted summary is
// visible to the LLM on the next Run: the provider sees
// [summaryMsg] + history[summaryCursor:] on its first Chat call.
func TestResume_RestoresSummary(t *testing.T) {
	fp := providerfake.New("res-sum")
	fp.Script = []providerfake.Call{
		{Resp: &provider.Response{Content: "ok"}},
	}
	fs, _ := session.NewFileStore(t.TempDir())
	ctx := context.Background()
	// 6 messages of history; summary covers first 2.
	hist := []provider.Message{
		{Role: provider.RoleUser, Content: "first"},
		{Role: provider.RoleAssistant, Content: "first-reply"},
		{Role: provider.RoleUser, Content: "second"},
		{Role: provider.RoleAssistant, Content: "second-reply"},
		{Role: provider.RoleUser, Content: "third"},
		{Role: provider.RoleAssistant, Content: "third-reply"},
	}
	if err := fs.Append(ctx, "sess-sum", hist); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := fs.SaveSummary(ctx, "sess-sum", session.SummaryInfo{
		Content: "EARLY SUMMARY",
		Cursor:  2,
		Tokens:  3,
	}); err != nil {
		t.Fatalf("SaveSummary: %v", err)
	}
	a := buildAgentWithStore(t, fp, agent.NewRegistry(), agent.Config{MaxSteps: 1}, fs)
	if err := a.Resume(ctx, "sess-sum"); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if _, err := a.Run(ctx, "new"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(fp.AllReqs) != 1 {
		t.Fatalf("AllReqs len = %d, want 1", len(fp.AllReqs))
	}
	msgs := fp.AllReqs[0].Messages
	// Cumulative history = 6 (loaded) + 1 (new user prompt) = 7.
	// summary covers first 2 → view = [summary, history[2:] of cumulative]
	//                   = [summary, second, second-reply, third, third-reply, new]
	//                   = 6 messages.
	if len(msgs) != 6 {
		t.Fatalf("msgs len = %d, want 6 (summary + history[cursor:] + new user msg)", len(msgs))
	}
	if msgs[0].Content != "EARLY SUMMARY" {
		t.Errorf("msgs[0] = %q, want EARLY SUMMARY", msgs[0].Content)
	}
	if msgs[1].Content != "second" || msgs[2].Content != "second-reply" {
		t.Errorf("msgs[1..] = %+v, want second/second-reply/...", msgs)
	}
}

// TestResume_StaleSummary_Discarded verifies a summary whose
// Cursor > len(history) (stale, e.g. session was truncated
// externally) is silently dropped — not surfaced to the LLM.
func TestResume_StaleSummary_Discarded(t *testing.T) {
	fp := providerfake.New("res-stale")
	fp.Script = []providerfake.Call{
		{Resp: &provider.Response{Content: "ok"}},
	}
	fs, _ := session.NewFileStore(t.TempDir())
	ctx := context.Background()
	hist := []provider.Message{
		{Role: provider.RoleUser, Content: "only"},
		{Role: provider.RoleAssistant, Content: "reply"},
	}
	if err := fs.Append(ctx, "stale", hist); err != nil {
		t.Fatalf("Append: %v", err)
	}
	// Cursor=10 > len(history)=2: stale.
	if err := fs.SaveSummary(ctx, "stale", session.SummaryInfo{
		Content: "STALE", Cursor: 10, Tokens: 1,
	}); err != nil {
		t.Fatalf("SaveSummary: %v", err)
	}
	a := buildAgentWithStore(t, fp, agent.NewRegistry(), agent.Config{MaxSteps: 1}, fs)
	if err := a.Resume(ctx, "stale"); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if _, err := a.Run(ctx, "next"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(fp.AllReqs) != 1 {
		t.Fatalf("AllReqs len = %d, want 1", len(fp.AllReqs))
	}
	msgs := fp.AllReqs[0].Messages
	for _, m := range msgs {
		if m.Content == "STALE" {
			t.Errorf("stale summary leaked into LLM view: %+v", msgs)
		}
	}
}

// TestResume_NoSummary_StillWorks verifies Resume is fine
// when no summary sidecar exists.
func TestResume_NoSummary_StillWorks(t *testing.T) {
	fp := providerfake.New("res-nosum")
	fp.Script = []providerfake.Call{
		{Resp: &provider.Response{Content: "ok"}},
	}
	fs, _ := session.NewFileStore(t.TempDir())
	ctx := context.Background()
	if err := fs.Append(ctx, "plain", []provider.Message{
		{Role: provider.RoleUser, Content: "hi"},
		{Role: provider.RoleAssistant, Content: "reply"},
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	a := buildAgentWithStore(t, fp, agent.NewRegistry(), agent.Config{MaxSteps: 1}, fs)
	if err := a.Resume(ctx, "plain"); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if _, err := a.Run(ctx, "next"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	msgs := fp.AllReqs[0].Messages
	if len(msgs) != 3 {
		t.Errorf("msgs len = %d, want 3 (no summary, 2 loaded + 1 user)", len(msgs))
	}
}

// TestSaveOnSuccess_PersistsSummary verifies that when the
// agent's summaryMsg is set during a step, saveOnSuccess
// writes it to the sidecar.
func TestSaveOnSuccess_PersistsSummary(t *testing.T) {
	fp := providerfake.New("save-sum")
	// 1st call: ErrContextLength (estimation wrong → reactive path).
	// 2nd call: summarizeHistory → "REDUCED SUMMARY".
	// 3rd call: retried step → "ok".
	fp.Script = []providerfake.Call{
		{Err: provider.ErrContextLength},
		{Resp: &provider.Response{Content: "REDUCED SUMMARY"}},
		{Resp: &provider.Response{Content: "ok"}},
	}
	fs, _ := session.NewFileStore(t.TempDir())
	ctx := context.Background()
	a := buildAgentWithStore(t, fp, agent.NewRegistry(), agent.Config{
		MaxSteps: 5,
	}, fs)
	if _, err := a.Run(ctx, "hi"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	id := a.SessionID()
	if id == "" {
		t.Fatal("sessionID empty after Run")
	}
	got, err := fs.LoadSummary(ctx, id)
	if err != nil {
		t.Fatalf("LoadSummary: %v", err)
	}
	if got.Content != "REDUCED SUMMARY" {
		t.Errorf("got.Content = %q, want REDUCED SUMMARY", got.Content)
	}
	// Cursor is 0 for reactive summarization (summary IS the
	// whole history now); non-zero for proactive (summary covers
	// a prefix). We just verify the file was written.
}

// TestReset_ClearsSummaryCursor verifies Reset zeros the
// summaryCursor (and the sidecar is deleted by Delete).
func TestReset_ClearsSummaryCursor(t *testing.T) {
	fp := providerfake.New("reset")
	fp.Script = []providerfake.Call{
		{Resp: &provider.Response{Content: "ok"}},
	}
	fs, _ := session.NewFileStore(t.TempDir())
	ctx := context.Background()
	hist := []provider.Message{
		{Role: provider.RoleUser, Content: "old"},
		{Role: provider.RoleAssistant, Content: "old-reply"},
	}
	if err := fs.Append(ctx, "s", hist); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := fs.SaveSummary(ctx, "s", session.SummaryInfo{Content: "SUM", Cursor: 1, Tokens: 1}); err != nil {
		t.Fatalf("SaveSummary: %v", err)
	}
	a := buildAgentWithStore(t, fp, agent.NewRegistry(), agent.Config{MaxSteps: 1}, fs)
	if err := a.Resume(ctx, "s"); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	a.Reset()
	// After Reset, the sidecar should be gone (Delete called).
	if _, err := fs.LoadSummary(ctx, "s"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("after Reset, LoadSummary err = %v, want os.ErrNotExist", err)
	}
}
