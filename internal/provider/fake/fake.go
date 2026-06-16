// Package fake provides a hand-written test double of
// provider.Provider for use by tests in any package.
//
// Lives in a subpackage (rather than provider_test.go) so the
// double can be imported by tests outside the provider package
// without duplicating the AGENTS.md-canonical fakeProvider.
package fake

import (
	"context"

	"chaosbot/internal/provider"
)

// Compile-time: Provider must satisfy provider.Provider.
var _ provider.Provider = (*Provider)(nil)

// Call is one scripted response. If Err is non-nil it is
// returned and Resp is ignored.
type Call struct {
	Resp *provider.Response
	Err  error
}

// Provider is a programmable test double. Tests program the
// behavior per call (Script for multi-step loops, NextResp /
// NextErr for single-shot), then inspect LastReq / AllReqs /
// Calls afterwards.
type Provider struct {
	NameStr  string
	NextResp *provider.Response
	NextErr  error
	Script   []Call
	Calls    int
	LastReq  provider.Request
	AllReqs  []provider.Request
}

// New returns a Provider with the given name and no programmed
// response or error (calling Chat without setting one will
// panic on nil deref — tests should program both paths).
func New(name string) *Provider {
	return &Provider{NameStr: name}
}

// Name implements provider.Provider.
func (f *Provider) Name() string { return f.NameStr }

// EstimateTokens implements provider.Provider.
func (f *Provider) EstimateTokens(content string) int { return provider.EstimateTokensDefault(content) }

// Chat implements provider.Provider.
func (f *Provider) Chat(_ context.Context, req provider.Request) (*provider.Response, error) {
	f.Calls++
	f.LastReq = req
	f.AllReqs = append(f.AllReqs, req)
	if len(f.Script) > 0 {
		call := f.Script[0]
		f.Script = f.Script[1:]
		if call.Err != nil {
			return nil, call.Err
		}
		return call.Resp, nil
	}
	if f.NextErr != nil {
		return nil, f.NextErr
	}
	return f.NextResp, nil
}
