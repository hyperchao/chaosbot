package agent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	agentfake "chaosbot/internal/agent/fake"
	"chaosbot/internal/provider"
	providerfake "chaosbot/internal/provider/fake"
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
	newHistory, final, err := a.step(context.Background(), history)
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
	newHistory, final, err := a.step(context.Background(), history)
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
	newHistory, final, err := a.step(context.Background(), history)
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
	_, _, err := a.step(context.Background(), []provider.Message{NewUserMessage("x")})
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
	_, _, err := a.step(context.Background(), history)
	if !errors.Is(err, provider.ErrSystemConflict) {
		t.Errorf("err = %v, want wraps ErrSystemConflict", err)
	}
}

func TestStep_PassesSystemAndToolsToProvider(t *testing.T) {
	a, fp := newTestAgent(t, map[string]string{"echo": "ok"})
	fp.NextResp = &provider.Response{Content: "done"}

	_, _, err := a.step(context.Background(), []provider.Message{NewUserMessage("hi")})
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
