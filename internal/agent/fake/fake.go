// Package fake provides a hand-written test double of agent.Tool
// for use by tests in any package.
//
// Lives in a subpackage (rather than agent_test.go) so the
// double can be imported by tests outside the agent package
// without duplicating the AGENTS.md-canonical fakeTool.
//
// This package deliberately does NOT import chaosbot/internal/agent
// (no compile-time `var _ agent.Tool = (*Tool)(nil)` check). The
// parent package's internal test files import this subpackage,
// which would form an agent -> agent/fake -> agent cycle. The
// contract that *Tool satisfies agent.Tool is enforced at every
// call site in the agent package's tests (Register takes
// agent.Tool; the compiler checks it).
package fake

import (
	"context"
	"encoding/json"
)

// Tool is a programmable test double. Tests set InvokeFunc to
// control the result/error, or leave it nil to get the default
// "fake-result" string back. Calls / LastCtx / LastArgs record
// the most recent Invoke call.
type Tool struct {
	NameStr    string
	Desc       string
	Params     json.RawMessage
	InvokeFunc func(ctx context.Context, args json.RawMessage) (string, error)
	Calls      int
	LastCtx    context.Context
	LastArgs   json.RawMessage
}

// New returns a Tool with the given name and no programmed
// behavior (Invoke returns "fake-result", nil).
func New(name string) *Tool {
	return &Tool{NameStr: name}
}

// Name implements agent.Tool.
func (f *Tool) Name() string { return f.NameStr }

// Description implements agent.Tool.
func (f *Tool) Description() string { return f.Desc }

// Parameters implements agent.Tool.
func (f *Tool) Parameters() json.RawMessage { return f.Params }

// Invoke implements agent.Tool. Records the call and delegates
// to InvokeFunc if set, else returns ("fake-result", nil).
func (f *Tool) Invoke(ctx context.Context, args json.RawMessage) (string, error) {
	f.Calls++
	f.LastCtx = ctx
	f.LastArgs = args
	if f.InvokeFunc != nil {
		return f.InvokeFunc(ctx, args)
	}
	return "fake-result", nil
}
