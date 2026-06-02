package provider_test

import (
	"context"
	"errors"
	"testing"

	"chaosbot/internal/provider"
)

// Compile-time assertion: fakeProvider must satisfy provider.Provider.
var _ provider.Provider = (*fakeProvider)(nil)

// fakeProvider is a hand-written test double used by every package
// that depends on provider.Provider. Tests program its next response
// (or error) and inspect the request it received.
//
// The script style is intentionally minimal: no queue, no recorder
// beyond the last call. Extend as needed when a real test asks for
// more, but do not add features speculatively.
type fakeProvider struct {
	name     string
	nextResp *provider.Response
	nextErr  error
	calls    int
	lastReq  provider.Request
}

func (f *fakeProvider) Name() string { return f.name }

func (f *fakeProvider) Chat(_ context.Context, req provider.Request) (*provider.Response, error) {
	f.calls++
	f.lastReq = req
	if f.nextErr != nil {
		return nil, f.nextErr
	}
	return f.nextResp, nil
}

func TestRoleConstants(t *testing.T) {
	cases := []struct {
		got  provider.Role
		want string
	}{
		{provider.RoleSystem, "system"},
		{provider.RoleUser, "user"},
		{provider.RoleAssistant, "assistant"},
		{provider.RoleTool, "tool"},
	}
	for _, c := range cases {
		if string(c.got) != c.want {
			t.Errorf("Role %q != %q", c.got, c.want)
		}
	}
}

func TestFakeProvider_ImplementsInterface(t *testing.T) {
	var p provider.Provider = &fakeProvider{name: "fake"}
	if got := p.Name(); got != "fake" {
		t.Errorf("Name() = %q, want %q", got, "fake")
	}
}

func TestFakeProvider_ReturnsProgrammedResponse(t *testing.T) {
	p := &fakeProvider{
		name: "fake",
		nextResp: &provider.Response{
			Content: "hello",
			Usage:   provider.Usage{PromptTokens: 3, CompletionTokens: 4, TotalTokens: 7},
		},
	}
	got, err := p.Chat(context.Background(), provider.Request{Model: "m"})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if got.Content != "hello" {
		t.Errorf("Content = %q, want %q", got.Content, "hello")
	}
	if got.Usage.TotalTokens != 7 {
		t.Errorf("Usage.TotalTokens = %d, want 7", got.Usage.TotalTokens)
	}
	if p.calls != 1 {
		t.Errorf("calls = %d, want 1", p.calls)
	}
}

func TestFakeProvider_CapturesRequest(t *testing.T) {
	p := &fakeProvider{name: "fake"}
	req := provider.Request{
		System:   "you are a helper",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
		Model:    "m",
	}
	if _, err := p.Chat(context.Background(), req); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if p.lastReq.System != "you are a helper" {
		t.Errorf("captured System = %q", p.lastReq.System)
	}
	if len(p.lastReq.Messages) != 1 || p.lastReq.Messages[0].Content != "hi" {
		t.Errorf("captured Messages mismatch: %+v", p.lastReq.Messages)
	}
}

func TestFakeProvider_ReturnsProgrammedError(t *testing.T) {
	wantErr := errors.New("fake failure")
	p := &fakeProvider{name: "fake", nextErr: wantErr}
	_, err := p.Chat(context.Background(), provider.Request{})
	if !errors.Is(err, wantErr) {
		t.Errorf("err = %v, want %v", err, wantErr)
	}
}

func TestRequest_Validate(t *testing.T) {
	cases := []struct {
		name string
		req  provider.Request
		want error
	}{
		{"both empty", provider.Request{}, nil},
		{"only system message", provider.Request{Messages: []provider.Message{{Role: provider.RoleSystem, Content: "x"}}}, nil},
		{"only System field", provider.Request{System: "x"}, nil},
		{"System and leading user message", provider.Request{System: "x", Messages: []provider.Message{{Role: provider.RoleUser, Content: "y"}}}, nil},
		{"System and leading system message (conflict)", provider.Request{System: "x", Messages: []provider.Message{{Role: provider.RoleSystem, Content: "y"}}}, provider.ErrSystemConflict},
		{"System and leading tool message", provider.Request{System: "x", Messages: []provider.Message{{Role: provider.RoleTool, Content: "y"}}}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := c.req.Validate()
			if !errors.Is(got, c.want) {
				t.Errorf("Validate() = %v, want %v", got, c.want)
			}
		})
	}
}
