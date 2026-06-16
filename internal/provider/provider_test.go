package provider_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"chaosbot/internal/provider"
	"chaosbot/internal/provider/fake"
)

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
	var p provider.Provider = &fake.Provider{NameStr: "fake"}
	if got := p.Name(); got != "fake" {
		t.Errorf("Name() = %q, want %q", got, "fake")
	}
}

func TestFakeProvider_ReturnsProgrammedResponse(t *testing.T) {
	p := &fake.Provider{
		NameStr: "fake",
		NextResp: &provider.Response{
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
	if p.Calls != 1 {
		t.Errorf("Calls = %d, want 1", p.Calls)
	}
}

func TestFakeProvider_CapturesRequest(t *testing.T) {
	p := &fake.Provider{NameStr: "fake"}
	req := provider.Request{
		System:   "you are a helper",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
		Model:    "m",
	}
	if _, err := p.Chat(context.Background(), req); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if p.LastReq.System != "you are a helper" {
		t.Errorf("captured System = %q", p.LastReq.System)
	}
	if len(p.LastReq.Messages) != 1 || p.LastReq.Messages[0].Content != "hi" {
		t.Errorf("captured Messages mismatch: %+v", p.LastReq.Messages)
	}
}

func TestFakeProvider_ReturnsProgrammedError(t *testing.T) {
	wantErr := errors.New("fake failure")
	p := &fake.Provider{NameStr: "fake", NextErr: wantErr}
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

func TestEstimateTokensDefault_Empty(t *testing.T) {
	if got := provider.EstimateTokensDefault(""); got != 0 {
		t.Errorf("EstimateTokensDefault(\"\") = %d, want 0", got)
	}
}

func TestEstimateTokensDefault_Latin(t *testing.T) {
	// 30 ASCII chars → heuristic returns 30/3 = 10 tokens.
	// Real GPT tokenizers typically return 6-8; the
	// heuristic is intentionally conservative (higher
	// count → windowing triggers earlier → never misses
	// the cap).
	got := provider.EstimateTokensDefault("hello world this is a test!!")
	if got < 5 || got > 15 {
		t.Errorf("Latin 30 chars: got %d, want roughly 10", got)
	}
}

func TestEstimateTokensDefault_CJK(t *testing.T) {
	// 30 CJK chars → heuristic returns 30/1 = 30 tokens.
	// Real CJK tokenizers typically return 20-30; heuristic
	// is conservative but in the right ballpark.
	cjk := "你好世界你好世界你好世界你好世界你好世界"
	got := provider.EstimateTokensDefault(cjk)
	if got < 20 || got > 40 {
		t.Errorf("CJK 30 chars: got %d, want roughly 30", got)
	}
}

func TestEstimateTokensDefault_NonZeroMinimum(t *testing.T) {
	// A 1-char non-empty string should still return at
	// least 1, not 0.
	if got := provider.EstimateTokensDefault("a"); got == 0 {
		t.Error("1-char ASCII returned 0; minimum should be 1")
	}
}

func TestErrContextLength_Sentinel(t *testing.T) {
	wrapped := fmt.Errorf("openai: %w: 400", provider.ErrContextLength)
	if !errors.Is(wrapped, provider.ErrContextLength) {
		t.Error("wrapped error should be matchable via errors.Is")
	}
}
