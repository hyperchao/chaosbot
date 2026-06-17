package openai_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"chaosbot/internal/provider"
	"chaosbot/internal/provider/openai"
)

func TestNew_ReturnsProviderWithName(t *testing.T) {
	p := openai.New(provider.Config{APIKey: "test-key", Name: "deepseek"})
	if p == nil {
		t.Fatal("New returned nil")
	}
	if got := p.Name(); got != "deepseek" {
		t.Errorf("Name() = %q, want %q (preserved case)", got, "deepseek")
	}
}

func TestNew_EmptyName_DefaultsToOpenAI(t *testing.T) {
	p := openai.New(provider.Config{APIKey: "test-key"})
	if got := p.Name(); got != "openai" {
		t.Errorf("Name() = %q, want %q", got, "openai")
	}
}

// TestClassifyErrors covers 09-1: HTTP status codes get mapped
// to the provider's error sentinels so callers can branch via
// errors.Is. We use an httptest server returning each status
// and verify the returned error satisfies errors.Is against
// the expected sentinel.
func TestClassifyErrors(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{
			name:   "429 rate limited",
			status: http.StatusTooManyRequests,
			body:   `{"error":{"message":"rate limit hit","type":"rate_limit_error"}}`,
			want:   provider.ErrRateLimited,
		},
		{
			name:   "401 unauthorized",
			status: http.StatusUnauthorized,
			body:   `{"error":{"message":"invalid api key","type":"invalid_request_error"}}`,
			want:   provider.ErrAuthFailed,
		},
		{
			name:   "403 forbidden",
			status: http.StatusForbidden,
			body:   `{"error":{"message":"forbidden","type":"invalid_request_error"}}`,
			want:   provider.ErrAuthFailed,
		},
		{
			name:   "400 bad request",
			status: http.StatusBadRequest,
			body:   `{"error":{"message":"bad param","type":"invalid_request_error"}}`,
			want:   provider.ErrBadRequest,
		},
		{
			name:   "500 server error",
			status: http.StatusInternalServerError,
			body:   `{"error":{"message":"server down"}}`,
			want:   provider.ErrServerError,
		},
		{
			name:   "502 bad gateway",
			status: http.StatusBadGateway,
			body:   `{"error":{"message":"bad gateway"}}`,
			want:   provider.ErrServerError,
		},
		{
			name:   "503 unavailable",
			status: http.StatusServiceUnavailable,
			body:   `{"error":{"message":"overloaded"}}`,
			want:   provider.ErrServerError,
		},
		{
			name:   "404 unknown status defaults to bad request",
			status: http.StatusNotFound,
			body:   `{"error":{"message":"not found"}}`,
			want:   provider.ErrBadRequest,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(c.status)
				fmt.Fprint(w, c.body)
			}))
			defer srv.Close()
			p := openai.New(provider.Config{
				APIKey:  "test-key",
				BaseURL: srv.URL,
				Timeout: 5 * time.Second,
			})
			_, err := p.Chat(context.Background(), provider.Request{Model: "m"})
			if err == nil {
				t.Fatal("want error")
			}
			if !errors.Is(err, c.want) {
				t.Errorf("err = %v, want errors.Is %v", err, c.want)
			}
		})
	}
}

// TestClassifyError_Network verifies that a connection
// refused (server not reachable) maps to ErrNetwork.
func TestClassifyError_Network(t *testing.T) {
	// Point at a port that's not listening.
	p := openai.New(provider.Config{
		APIKey:  "test-key",
		BaseURL: "http://127.0.0.1:1", // refused
		Timeout: 2 * time.Second,
	})
	_, err := p.Chat(context.Background(), provider.Request{Model: "m"})
	if err == nil {
		t.Fatal("want error")
	}
	if !errors.Is(err, provider.ErrNetwork) {
		t.Errorf("err = %v, want errors.Is ErrNetwork", err)
	}
}

// TestClassifyError_PreservesMessage verifies the wrapped
// error still contains useful details (the API's reason
// text), not just the sentinel.
func TestClassifyError_PreservesMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]string{
				"message": "you exceeded your token plan; upgrade or switch to pay-as-you-go",
				"type":    "rate_limit_error",
			},
		})
	}))
	defer srv.Close()
	p := openai.New(provider.Config{APIKey: "test-key", BaseURL: srv.URL})
	_, err := p.Chat(context.Background(), provider.Request{Model: "m"})
	if err == nil {
		t.Fatal("want error")
	}
	if !errors.Is(err, provider.ErrRateLimited) {
		t.Errorf("err = %v, want errors.Is ErrRateLimited", err)
	}
	if !strings.Contains(err.Error(), "token plan") {
		t.Errorf("err = %q, want contains 'token plan' (API message preserved)", err.Error())
	}
}

// TestRetry_SucceedsOnSecondAttempt verifies that a
// transient 429 is retried and a 200 on the second call
// returns the response.
func TestRetry_SucceedsOnSecondAttempt(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `{"error":{"message":"rate limit"}}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"x","choices":[{"message":{"role":"assistant","content":"hi"}}]}`)
	}))
	defer srv.Close()
	p := openai.New(provider.Config{
		APIKey:         "test-key",
		BaseURL:        srv.URL,
		MaxRetries:     3,
		RetryBaseDelay: 1 * time.Millisecond, // tests should be fast
	})
	resp, err := p.Chat(context.Background(), provider.Request{Model: "m"})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Content != "hi" {
		t.Errorf("Content = %q, want 'hi'", resp.Content)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2 (one fail + one success)", calls)
	}
}

// TestRetry_ExhaustedReturnsLastError verifies that when
// all attempts fail with 429, the final error is returned
// (still classified as ErrRateLimited).
func TestRetry_ExhaustedReturnsLastError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"error":{"message":"rate limit"}}`)
	}))
	defer srv.Close()
	p := openai.New(provider.Config{
		APIKey:         "test-key",
		BaseURL:        srv.URL,
		MaxRetries:     2, // total 3 attempts
		RetryBaseDelay: 1 * time.Millisecond,
	})
	_, err := p.Chat(context.Background(), provider.Request{Model: "m"})
	if err == nil {
		t.Fatal("want error after exhausting retries")
	}
	if !errors.Is(err, provider.ErrRateLimited) {
		t.Errorf("err = %v, want errors.Is ErrRateLimited", err)
	}
}

// TestRetry_NoRetryOnAuthFailed verifies that a 401
// returns immediately without retrying (permanent error).
func TestRetry_NoRetryOnAuthFailed(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":{"message":"bad key"}}`)
	}))
	defer srv.Close()
	p := openai.New(provider.Config{
		APIKey:         "test-key",
		BaseURL:        srv.URL,
		MaxRetries:     3,
		RetryBaseDelay: 1 * time.Millisecond,
	})
	_, err := p.Chat(context.Background(), provider.Request{Model: "m"})
	if err == nil {
		t.Fatal("want error")
	}
	if !errors.Is(err, provider.ErrAuthFailed) {
		t.Errorf("err = %v, want ErrAuthFailed", err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1 (no retry on auth failure)", calls)
	}
}

// TestRetry_NoRetryOnBadRequest verifies that a 400
// returns immediately (not retried).
func TestRetry_NoRetryOnBadRequest(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":{"message":"bad param"}}`)
	}))
	defer srv.Close()
	p := openai.New(provider.Config{
		APIKey:         "test-key",
		BaseURL:        srv.URL,
		MaxRetries:     3,
		RetryBaseDelay: 1 * time.Millisecond,
	})
	_, err := p.Chat(context.Background(), provider.Request{Model: "m"})
	if err == nil {
		t.Fatal("want error")
	}
	if !errors.Is(err, provider.ErrBadRequest) {
		t.Errorf("err = %v, want ErrBadRequest", err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1 (no retry on bad request)", calls)
	}
}

// TestRetry_DisabledWhenMaxRetriesZero verifies that
// MaxRetries=0 means a single attempt with no retry.
func TestRetry_DisabledWhenMaxRetriesZero(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"error":{"message":"rate limit"}}`)
	}))
	defer srv.Close()
	p := openai.New(provider.Config{
		APIKey:     "test-key",
		BaseURL:    srv.URL,
		MaxRetries: 0,
	})
	_, err := p.Chat(context.Background(), provider.Request{Model: "m"})
	if err == nil {
		t.Fatal("want error")
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1 (no retry when MaxRetries=0)", calls)
	}
}

// TestRetry_ContextCancelDuringBackoff verifies that a
// ctx.Cancel during the backoff sleep aborts the retry
// loop and returns ctx.Err.
func TestRetry_ContextCancelDuringBackoff(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"error":{"message":"rate limit"}}`)
	}))
	defer srv.Close()
	p := openai.New(provider.Config{
		APIKey:         "test-key",
		BaseURL:        srv.URL,
		MaxRetries:     5,
		RetryBaseDelay: 10 * time.Second, // long; we cancel before sleeping
	})
	ctx, cancel := context.WithCancel(context.Background())
	// Cancel after a short delay so the first call completes
	// and the backoff begins.
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	_, err := p.Chat(ctx, provider.Request{Model: "m"})
	elapsed := time.Since(start)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("elapsed = %v, want < 500ms (cancel should short-circuit backoff)", elapsed)
	}
}
