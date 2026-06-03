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

// Provider is a programmable test double. Tests set NextResp /
// NextErr before calling Chat, then inspect LastReq / Calls
// afterwards. Intentionally minimal: no response queue, no
// request recorder beyond the last call. Extend when a real
// test asks for more, but do not add features speculatively.
type Provider struct {
	NameStr  string
	NextResp *provider.Response
	NextErr  error
	Calls    int
	LastReq  provider.Request
}

// New returns a Provider with the given name and no programmed
// response or error (calling Chat without setting one will
// panic on nil deref — tests should program both paths).
func New(name string) *Provider {
	return &Provider{NameStr: name}
}

// Name implements provider.Provider.
func (f *Provider) Name() string { return f.NameStr }

// Chat implements provider.Provider.
func (f *Provider) Chat(_ context.Context, req provider.Request) (*provider.Response, error) {
	f.Calls++
	f.LastReq = req
	if f.NextErr != nil {
		return nil, f.NextErr
	}
	return f.NextResp, nil
}
