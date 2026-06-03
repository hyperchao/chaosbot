package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"chaosbot/internal/agent"
)

// Compile-time: fakeTool must satisfy agent.Tool.
var _ agent.Tool = (*fakeTool)(nil)

// fakeTool is a hand-written test double for agent.Tool. It records
// the call count and the last ctx/args it received, and lets each
// test program the Invoke result/error via invokeFunc.
type fakeTool struct {
	name       string
	desc       string
	params     json.RawMessage
	invokeFunc func(ctx context.Context, args json.RawMessage) (string, error)
	calls      int
	lastCtx    context.Context
	lastArgs   json.RawMessage
}

func (f *fakeTool) Name() string                { return f.name }
func (f *fakeTool) Description() string         { return f.desc }
func (f *fakeTool) Parameters() json.RawMessage { return f.params }

func (f *fakeTool) Invoke(ctx context.Context, args json.RawMessage) (string, error) {
	f.calls++
	f.lastCtx = ctx
	f.lastArgs = args
	if f.invokeFunc != nil {
		return f.invokeFunc(ctx, args)
	}
	return "fake-result", nil
}

func TestNewRegistry_Empty(t *testing.T) {
	r := agent.NewRegistry()
	if got := r.Names(); len(got) != 0 {
		t.Errorf("Names() = %v, want []", got)
	}
	if got := r.Specs(); len(got) != 0 {
		t.Errorf("Specs() = %v, want []", got)
	}
}

func TestRegister_Adds(t *testing.T) {
	r := agent.NewRegistry()
	r.Register(&fakeTool{name: "foo"})
	if got := r.Names(); len(got) != 1 || got[0] != "foo" {
		t.Errorf("Names() = %v, want [foo]", got)
	}
}

func TestRegister_Overwrites(t *testing.T) {
	r := agent.NewRegistry()
	r.Register(&fakeTool{name: "foo", desc: "first"})
	r.Register(&fakeTool{name: "foo", desc: "second"})
	if got := r.Names(); len(got) != 1 {
		t.Fatalf("Names() = %v, want 1 entry", got)
	}
	specs := r.Specs()
	if len(specs) != 1 || specs[0].Description != "second" {
		t.Errorf("Specs() = %+v, want the second tool to win", specs)
	}
}

func TestSpecs_MatchesToolFields(t *testing.T) {
	params := json.RawMessage(`{"type":"object","properties":{"x":{"type":"integer"}}}`)
	r := agent.NewRegistry()
	r.Register(&fakeTool{name: "add", desc: "add two numbers", params: params})
	specs := r.Specs()
	if len(specs) != 1 {
		t.Fatalf("Specs() returned %d entries, want 1", len(specs))
	}
	s := specs[0]
	if s.Name != "add" {
		t.Errorf("Name = %q, want %q", s.Name, "add")
	}
	if s.Description != "add two numbers" {
		t.Errorf("Description = %q", s.Description)
	}
	if string(s.Parameters) != string(params) {
		t.Errorf("Parameters = %s, want %s", s.Parameters, params)
	}
}

func TestNames_Sorted(t *testing.T) {
	r := agent.NewRegistry()
	r.Register(&fakeTool{name: "zebra"})
	r.Register(&fakeTool{name: "alpha"})
	r.Register(&fakeTool{name: "mango"})
	got := r.Names()
	want := []string{"alpha", "mango", "zebra"}
	if !slices.Equal(got, want) {
		t.Errorf("Names() = %v, want %v", got, want)
	}
}

func TestInvoke_DispatchesAndReturnsResult(t *testing.T) {
	tool := &fakeTool{
		name: "echo",
		invokeFunc: func(_ context.Context, _ json.RawMessage) (string, error) {
			return "echoed", nil
		},
	}
	r := agent.NewRegistry()
	r.Register(tool)
	got, err := r.Invoke(context.Background(), "echo", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if got != "echoed" {
		t.Errorf("Invoke = %q, want %q", got, "echoed")
	}
	if tool.calls != 1 {
		t.Errorf("calls = %d, want 1", tool.calls)
	}
}

func TestInvoke_PropagatesToolError(t *testing.T) {
	want := errors.New("tool failure")
	tool := &fakeTool{
		name: "boom",
		invokeFunc: func(_ context.Context, _ json.RawMessage) (string, error) {
			return "", want
		},
	}
	r := agent.NewRegistry()
	r.Register(tool)
	_, err := r.Invoke(context.Background(), "boom", nil)
	if !errors.Is(err, want) {
		t.Errorf("err = %v, want %v", err, want)
	}
}

func TestInvoke_NotFound_ReturnsErrToolNotFound(t *testing.T) {
	r := agent.NewRegistry()
	_, err := r.Invoke(context.Background(), "missing", nil)
	if !errors.Is(err, agent.ErrToolNotFound) {
		t.Errorf("err = %v, want ErrToolNotFound", err)
	}
	if !strings.Contains(err.Error(), `"missing"`) {
		t.Errorf("err = %q, want it to contain the requested name", err)
	}
}

func TestInvoke_RespectsContextCancellation(t *testing.T) {
	started := make(chan struct{})
	tool := &fakeTool{
		name: "blocking",
		invokeFunc: func(ctx context.Context, _ json.RawMessage) (string, error) {
			close(started)
			<-ctx.Done()
			return "", ctx.Err()
		},
	}
	r := agent.NewRegistry()
	r.Register(tool)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-started
		cancel()
	}()
	_, err := r.Invoke(ctx, "blocking", nil)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestInvoke_PassesRawArgsUnchanged(t *testing.T) {
	want := json.RawMessage(`{"path":"/tmp/x","n":42}`)
	tool := &fakeTool{name: "read"}
	r := agent.NewRegistry()
	r.Register(tool)
	_, _ = r.Invoke(context.Background(), "read", want)
	if string(tool.lastArgs) != string(want) {
		t.Errorf("lastArgs = %s, want %s", tool.lastArgs, want)
	}
}
