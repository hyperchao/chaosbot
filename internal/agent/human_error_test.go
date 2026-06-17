package agent_test

import (
	"errors"
	"fmt"
	"testing"

	"chaosbot/internal/agent"
	"chaosbot/internal/provider"
)

func TestHumanError_ContextLength(t *testing.T) {
	err := fmt.Errorf("wrapped: %w", provider.ErrContextLength)
	got := agent.HumanError(err)
	want := "context too long; type /reset to start a new session"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestHumanError_RateLimited(t *testing.T) {
	err := fmt.Errorf("wrapped: %w: api said slow down", provider.ErrRateLimited)
	got := agent.HumanError(err)
	want := "rate limited; please wait a moment and try again"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestHumanError_AuthFailed(t *testing.T) {
	err := fmt.Errorf("wrapped: %w", provider.ErrAuthFailed)
	got := agent.HumanError(err)
	want := "authentication failed; check CHAOSBOT_API_KEY or provider.api_key"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestHumanError_ServerError(t *testing.T) {
	err := fmt.Errorf("wrapped: %w", provider.ErrServerError)
	got := agent.HumanError(err)
	want := "provider server error; please try again later"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestHumanError_Network(t *testing.T) {
	// Simulate what the openai provider produces: the sentinel
	// is wrapped with a context string that's a separate
	// sub-error. errors.Is matches the sentinel; err.Error()
	// contains both messages.
	wrapped := fmt.Errorf("wrapped: %w", provider.ErrNetwork)
	got := agent.HumanError(wrapped)
	// We just want a useful message that mentions "network".
	if !contains(got, "network") {
		t.Errorf("got %q, want contains 'network'", got)
	}
}

func TestHumanError_BadRequestPreservesDetail(t *testing.T) {
	// Bad request messages often include the API's diagnostic
	// (e.g. "model 'foo' not found"); preserve it.
	inner := errors.New("model 'foo' not found")
	err := fmt.Errorf("wrapped: %w: %s", provider.ErrBadRequest, inner.Error())
	got := agent.HumanError(err)
	if !contains(got, "model 'foo' not found") {
		t.Errorf("got %q, want contains API detail", got)
	}
}

func TestHumanError_UnknownReturnsRawString(t *testing.T) {
	// Errors that don't match any sentinel pass through.
	raw := errors.New("some weird failure")
	got := agent.HumanError(raw)
	if got != "some weird failure" {
		t.Errorf("got %q, want raw string passthrough", got)
	}
}

func TestHumanError_Nil(t *testing.T) {
	if got := agent.HumanError(nil); got != "" {
		t.Errorf("got %q, want empty string for nil", got)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
