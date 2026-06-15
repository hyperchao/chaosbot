package shell_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"chaosbot/internal/tools/shell"
)

func TestShell_RunsEcho(t *testing.T) {
	ts := &shell.ShellTool{}
	reply, err := ts.Invoke(context.Background(), mustJSON(t, map[string]any{
		"command": "echo hello world",
	}))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if !strings.Contains(reply, "hello world") {
		t.Errorf("reply = %q, want contains 'hello world'", reply)
	}
	if !strings.Contains(reply, "exit 0") {
		t.Errorf("reply = %q, want contains 'exit 0'", reply)
	}
}

func TestShell_NonZeroExit_SurfacesInReply(t *testing.T) {
	ts := &shell.ShellTool{}
	reply, err := ts.Invoke(context.Background(), mustJSON(t, map[string]any{
		"command": "sh -c 'echo oops; exit 7'",
	}))
	if err != nil {
		t.Fatalf("Invoke (non-zero exit should not error): %v", err)
	}
	if !strings.Contains(reply, "oops") {
		t.Errorf("reply = %q, want contains 'oops'", reply)
	}
	if !strings.Contains(reply, "exit 7") {
		t.Errorf("reply = %q, want contains 'exit 7'", reply)
	}
}

func TestShell_StderrMerged(t *testing.T) {
	ts := &shell.ShellTool{}
	reply, err := ts.Invoke(context.Background(), mustJSON(t, map[string]any{
		"command": "sh -c 'echo on_stdout; echo on_stderr >&2'",
	}))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if !strings.Contains(reply, "on_stdout") {
		t.Errorf("reply = %q, want contains 'on_stdout'", reply)
	}
	if !strings.Contains(reply, "on_stderr") {
		t.Errorf("reply = %q, want contains 'on_stderr' (stderr should be merged)", reply)
	}
}

func TestShell_Timeout_KillsChild(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timeout test in -short mode")
	}
	ts := &shell.ShellTool{}
	start := time.Now()
	_, err := ts.Invoke(context.Background(), mustJSON(t, map[string]any{
		"command":     "sleep 5",
		"timeout_sec": 1,
	}))
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("want error for timed-out command")
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Errorf("err = %v, want mentions 'timeout'", err)
	}
	if elapsed > 3*time.Second {
		t.Errorf("elapsed = %v, want < 3s (child should be killed around 1s)", elapsed)
	}
}

func TestShell_TruncatesAt100KB(t *testing.T) {
	ts := &shell.ShellTool{}
	// head -c 200000 exits cleanly after producing 200 KB.
	// The cap should fire and the reply should be capped at
	// 100 KB + marker + exit line.
	reply, err := ts.Invoke(context.Background(), mustJSON(t, map[string]any{
		"command":     "head -c 200000 /dev/zero | tr '\\0' 'y'",
		"timeout_sec": 5,
	}))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if !strings.Contains(reply, "truncated") {
		t.Errorf("reply = %q, want contains 'truncated' marker", reply)
	}
	if len(reply) > 110*1024 {
		t.Logf("reply length = %d", len(reply))
		t.Errorf("reply length = %d, want < 110 KB (cap is 100 KB + marker)", len(reply))
	}
}

func TestShell_InvalidTimeout(t *testing.T) {
	ts := &shell.ShellTool{}
	_, err := ts.Invoke(context.Background(), mustJSON(t, map[string]any{
		"command":     "echo x",
		"timeout_sec": 99999,
	}))
	if err == nil {
		t.Fatal("want error for timeout_sec > 600")
	}
	if !strings.Contains(err.Error(), "out of range") {
		t.Errorf("err = %v, want mentions 'out of range'", err)
	}
}

func TestShell_NegativeTimeout(t *testing.T) {
	ts := &shell.ShellTool{}
	_, err := ts.Invoke(context.Background(), mustJSON(t, map[string]any{
		"command":     "echo x",
		"timeout_sec": -5,
	}))
	if err == nil {
		t.Fatal("want error for negative timeout_sec")
	}
}

func TestShell_EmptyCommand(t *testing.T) {
	ts := &shell.ShellTool{}
	_, err := ts.Invoke(context.Background(), mustJSON(t, map[string]string{
		"command": "",
	}))
	if err == nil {
		t.Fatal("want error for empty command")
	}
	if !strings.Contains(err.Error(), "command is required") {
		t.Errorf("err = %v, want mentions 'command is required'", err)
	}
}

func TestShell_ContextCanceled(t *testing.T) {
	ts := &shell.ShellTool{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := ts.Invoke(ctx, mustJSON(t, map[string]string{
		"command": "echo x",
	}))
	if err == nil {
		t.Fatal("want error for pre-canceled ctx")
	}
}

func TestShell_CommandNotFound(t *testing.T) {
	ts := &shell.ShellTool{}
	reply, err := ts.Invoke(context.Background(), mustJSON(t, map[string]string{
		"command": "this-binary-does-not-exist-xyz",
	}))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	// /bin/sh exits 127 with "not found" on stderr when the
	// inner command is missing — we surface exit code, not Go
	// error, so the LLM sees it.
	if !strings.Contains(reply, "exit 127") && !strings.Contains(reply, "not found") {
		t.Errorf("reply = %q, want contains 'exit 127' or 'not found'", reply)
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
