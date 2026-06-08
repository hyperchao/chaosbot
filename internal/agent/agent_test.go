package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/hyperchao/di"

	"chaosbot/internal/agent"
	"chaosbot/internal/agent/fake"
	"chaosbot/internal/provider"
	providerfake "chaosbot/internal/provider/fake"
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
