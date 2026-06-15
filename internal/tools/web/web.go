// Package web hosts the web_fetch tool. chaosbot's "web_fetch"
// tool performs a single HTTP GET, runs the body through the
// golang.org/x/net/html tokenizer to extract visible text, and
// returns the result. The body is capped at 1 MB on the wire
// (io.LimitReader); the rendered text is capped at 50 KB.
package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

const (
	maxInputBytes  = 1 * 1024 * 1024
	maxOutputBytes = 50 * 1024
	httpTimeoutSec = 30
	truncationMsg  = "\n... [truncated, output exceeded 50 KB] ...\n"
)

// WebFetchTool fetches a URL and returns its visible text. See
// docs/phases/phase-05-tools.md §05-5 for the full contract:
// HTTP GET only, body capped at 1 MB via io.LimitReader, HTML
// tag/text stripping via golang.org/x/net/html, 50 KB output
// cap with a marker on overflow. The stdlib *http.Client is
// reused so a single agent session does not exhaust ephemeral
// ports on Linux.
type WebFetchTool struct {
	client *http.Client
}

// NewWebFetchTool returns a tool with a default http.Client
// (30 s timeout, default transport). The client is reused
// across calls.
func NewWebFetchTool() *WebFetchTool {
	return &WebFetchTool{
		client: &http.Client{Timeout: httpTimeoutSec * 1_000_000_000},
	}
}

// Name implements agent.Tool.
func (t *WebFetchTool) Name() string { return "web_fetch" }

// Description implements agent.Tool.
func (t *WebFetchTool) Description() string {
	return "Fetch a URL and return its visible text. HTML " +
		"tags are stripped, <script>/<style> blocks removed, " +
		"and HTML entities decoded. Body is capped at 1 MB; " +
		"output at 50 KB. HTTP and HTTPS only; no auth, no " +
		"cookies, no JavaScript execution."
}

// Parameters implements agent.Tool.
func (t *WebFetchTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["url"],
		"properties": {
			"url": {"type": "string", "description": "http:// or https:// URL"}
		},
		"additionalProperties": false
	}`)
}

// Invoke implements agent.Tool.
func (t *WebFetchTool) Invoke(ctx context.Context, args json.RawMessage) (string, error) {
	var a webFetchArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("web_fetch: invalid args: %w", err)
	}
	if a.URL == "" {
		return "", errors.New("web_fetch: url is required")
	}
	u, err := url.Parse(a.URL)
	if err != nil {
		return "", fmt.Errorf("web_fetch: invalid url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("web_fetch: scheme %q not supported (use http or https)", u.Scheme)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	if err != nil {
		return "", err
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("web_fetch: http: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("web_fetch: HTTP %d %s", resp.StatusCode, resp.Status)
	}

	limited := io.LimitReader(resp.Body, maxInputBytes+1)
	text, err := htmlToText(limited)
	if err != nil {
		return "", fmt.Errorf("web_fetch: parse: %w", err)
	}

	if len(text) > maxOutputBytes {
		text = text[:maxOutputBytes] + truncationMsg
	}
	return text, nil
}

type webFetchArgs struct {
	URL string `json:"url"`
}

// htmlToText walks the HTML tokenizer and accumulates visible
// text. <script> and <style> blocks are skipped; tags are
// dropped. Whitespace between adjacent text nodes is collapsed
// to a single space; block-level boundaries get a newline.
func htmlToText(r io.Reader) (string, error) {
	z := html.NewTokenizer(r)
	var out strings.Builder
	skipDepth := 0
	prevWasText := false

	flushSpace := func() {
		if prevWasText {
			out.WriteByte(' ')
		}
		prevWasText = false
	}

	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			if err := z.Err(); err != nil && !errors.Is(err, io.EOF) {
				return "", err
			}
			break
		}
		switch tt {
		case html.StartTagToken:
			name, _ := z.TagName()
			if isSkippable(name) {
				skipDepth++
			}
			if isBlock(name) {
				flushSpace()
				out.WriteByte('\n')
			}
		case html.EndTagToken:
			name, _ := z.TagName()
			if isSkippable(name) {
				if skipDepth > 0 {
					skipDepth--
				}
			}
			if isBlock(name) {
				flushSpace()
				out.WriteByte('\n')
			}
		case html.SelfClosingTagToken:
			// ignore (img, br, etc.); block-level ends handled above
		case html.TextToken:
			if skipDepth == 0 {
				txt := strings.TrimSpace(string(z.Text()))
				if txt != "" {
					if prevWasText {
						out.WriteByte(' ')
					}
					out.WriteString(txt)
					prevWasText = true
				}
			}
		}
	}
	return strings.TrimSpace(out.String()), nil
}

// isSkippable reports whether the element's body should be
// excluded from the visible text. script and style are
// classic; noscript / noframes are content-inert too.
func isSkippable(name []byte) bool {
	switch atom.Lookup(name) {
	case atom.Script, atom.Style, atom.Noscript, atom.Noframes, atom.Head, atom.Template:
		return true
	}
	return false
}

// isBlock reports whether the element introduces a block-level
// break in the rendered text. Used to insert newlines so the
// LLM sees a readable layout.
func isBlock(name []byte) bool {
	switch atom.Lookup(name) {
	case atom.P, atom.Div, atom.Br, atom.Li, atom.H1, atom.H2, atom.H3,
		atom.H4, atom.H5, atom.H6, atom.Tr, atom.Blockquote, atom.Pre,
		atom.Section, atom.Article, atom.Header, atom.Footer, atom.Main,
		atom.Aside, atom.Nav, atom.Hr, atom.Table, atom.Ul, atom.Ol,
		atom.Hgroup, atom.Figure, atom.Figcaption, atom.Details, atom.Summary:
		return true
	}
	return false
}
