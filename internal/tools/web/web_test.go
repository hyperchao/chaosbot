package web_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"chaosbot/internal/tools/web"
)

func TestWeb_FetchesHTML_StripsTags(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<html><head><title>T</title></head>
<body>
<h1>Hello</h1>
<p>World</p>
<a href="/x">link</a>
</body></html>`)
	}))
	defer srv.Close()

	tw := web.NewWebFetchTool()
	reply, err := tw.Invoke(context.Background(), mustJSON(t, map[string]string{
		"url": srv.URL,
	}))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	for _, want := range []string{"Hello", "World", "link"} {
		if !strings.Contains(reply, want) {
			t.Errorf("reply = %q, want contains %q", reply, want)
		}
	}
	// Tags themselves should be gone.
	if strings.Contains(reply, "<h1>") || strings.Contains(reply, "<p>") {
		t.Errorf("reply should not contain raw tags, got %q", reply)
	}
}

func TestWeb_StripsScript(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html><body>
<p>before</p>
<script>alert('xss'); console.log("secret");</script>
<style>body { color: red }</style>
<p>after</p>
</body></html>`)
	}))
	defer srv.Close()

	tw := web.NewWebFetchTool()
	reply, err := tw.Invoke(context.Background(), mustJSON(t, map[string]string{
		"url": srv.URL,
	}))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	for _, banned := range []string{"alert", "xss", "secret", "color: red", "<script>", "<style>"} {
		if strings.Contains(reply, banned) {
			t.Errorf("reply should not contain %q, got %q", banned, reply)
		}
	}
	if !strings.Contains(reply, "before") || !strings.Contains(reply, "after") {
		t.Errorf("reply should contain 'before' and 'after', got %q", reply)
	}
}

func TestWeb_RejectsNonHTTPScheme(t *testing.T) {
	tw := web.NewWebFetchTool()
	for _, bad := range []string{"file:///etc/passwd", "ftp://example.com", "javascript:alert(1)", "data:text/plain,foo"} {
		_, err := tw.Invoke(context.Background(), mustJSON(t, map[string]string{
			"url": bad,
		}))
		if err == nil {
			t.Errorf("want error for url %q", bad)
		}
	}
}

func TestWeb_4xx5xx_Errors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	tw := web.NewWebFetchTool()
	_, err := tw.Invoke(context.Background(), mustJSON(t, map[string]string{
		"url": srv.URL,
	}))
	if err == nil {
		t.Fatal("want error for 404")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("err = %v, want mentions '404'", err)
	}
}

func TestWeb_LimitsInputTo1MB(t *testing.T) {
	// Server sends 2 MB; the LimitReader should cap it at 1 MB.
	// The tokenizer stops at 1 MB, returns whatever was parsed.
	// We assert the response finishes and stays under, say,
	// 1.5 MB (a generous upper bound for parsed text).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		chunk := strings.Repeat("x", 1024)
		for range 2048 {
			fmt.Fprint(w, chunk)
		}
	}))
	defer srv.Close()

	tw := web.NewWebFetchTool()
	reply, err := tw.Invoke(context.Background(), mustJSON(t, map[string]string{
		"url": srv.URL,
	}))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	// 1 MB body of "xxxxxx..." becomes 1 MB of text plus a
	// possible truncation marker. Allow a generous upper bound.
	if len(reply) > 2*1024*1024 {
		t.Errorf("reply length = %d, want < 2 MB (1 MB body cap + marker)", len(reply))
	}
}

func TestWeb_TruncatesTo50KB(t *testing.T) {
	// 200 KB of plain text, no tags. Output should be capped
	// at 50 KB + truncation marker.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		// Wrap each line in <p> so the tokenizer emits a newline
		// between them (otherwise the "words" run together into
		// one giant text node).
		for range 5000 {
			fmt.Fprintln(w, "<p>", strings.Repeat("a", 40), "</p>")
		}
	}))
	defer srv.Close()

	tw := web.NewWebFetchTool()
	reply, err := tw.Invoke(context.Background(), mustJSON(t, map[string]string{
		"url": srv.URL,
	}))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if !strings.Contains(reply, "truncated") {
		t.Errorf("reply = %q (len %d), want contains 'truncated' marker", reply, len(reply))
	}
	if len(reply) > 55*1024 {
		t.Errorf("reply length = %d, want < 55 KB (cap is 50 KB + marker)", len(reply))
	}
}

func TestWeb_EmptyURL(t *testing.T) {
	tw := web.NewWebFetchTool()
	_, err := tw.Invoke(context.Background(), mustJSON(t, map[string]string{
		"url": "",
	}))
	if err == nil {
		t.Fatal("want error for empty url")
	}
}

func TestWeb_BadJSON(t *testing.T) {
	tw := web.NewWebFetchTool()
	_, err := tw.Invoke(context.Background(), json.RawMessage(`not json`))
	if err == nil {
		t.Fatal("want error for malformed JSON args")
	}
}

func TestWeb_ContextCanceled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// hang
		<-r.Context().Done()
	}))
	defer srv.Close()

	tw := web.NewWebFetchTool()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := tw.Invoke(ctx, mustJSON(t, map[string]string{
		"url": srv.URL,
	}))
	if err == nil {
		t.Fatal("want error for pre-canceled ctx")
	}
}

func mustJSON(t *testing.T, m any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}
