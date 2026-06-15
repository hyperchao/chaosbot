// Package shell hosts the shell tool. chaosbot's "shell" tool
// runs commands via /bin/sh -c, captures merged stdout+stderr
// up to a hard cap, enforces a per-call timeout, and reports
// the exit code as part of the tool result so the LLM can
// distinguish success from failure.
package shell

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"time"
)

const (
	defaultTimeoutSec = 30
	maxTimeoutSec     = 600
	maxOutputBytes    = 100 * 1024
	truncationMarker  = "\n... [truncated, output exceeded 100 KB] ...\n"
)

// ShellTool runs a shell command. See docs/phases/phase-05-tools.md
// §05-4 for the full contract: /bin/sh -c, merged stdout+stderr
// (≤ 100 KB, with a marker on overflow), default 30 s timeout
// (max 600 s), exit code surfaced in the reply. ctx cancellation
// aborts the child with os.Process.Kill.
type ShellTool struct{}

// Name implements agent.Tool.
func (t *ShellTool) Name() string { return "shell" }

// Description implements agent.Tool.
func (t *ShellTool) Description() string {
	return "Run a shell command via /bin/sh -c. stdout and " +
		"stderr are merged and returned together with the " +
		"exit code. Output is capped at 100 KB; longer output " +
		"is truncated with a marker. Default timeout 30 s, " +
		"max 600 s. ctx cancellation kills the child."
}

// Parameters implements agent.Tool.
func (t *ShellTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["command"],
		"properties": {
			"command":     {"type": "string", "description": "shell command line (runs via /bin/sh -c)"},
			"timeout_sec": {"type": "integer", "minimum": 1, "maximum": 600, "default": 30}
		},
		"additionalProperties": false
	}`)
}

// Invoke implements agent.Tool.
func (t *ShellTool) Invoke(ctx context.Context, args json.RawMessage) (string, error) {
	var a shellArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("shell: invalid args: %w", err)
	}
	if a.Command == "" {
		return "", errors.New("shell: command is required")
	}

	timeout := time.Duration(a.TimeoutSec) * time.Second
	if a.TimeoutSec == 0 {
		timeout = defaultTimeoutSec * time.Second
	} else if a.TimeoutSec < 0 || a.TimeoutSec > maxTimeoutSec {
		return "", fmt.Errorf("shell: timeout_sec %d out of range (1..%d)", a.TimeoutSec, maxTimeoutSec)
	}

	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "/bin/sh", "-c", a.Command)
	cap := &cappedWriter{limit: maxOutputBytes}
	cmd.Stdout = cap
	cmd.Stderr = cap

	err := cmd.Run()
	output := cap.Bytes()
	truncated := cap.Truncated()

	if cmdCtx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("shell: command killed after %d s timeout: %s", int(timeout.Seconds()), trim(output, 200))
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}

	reply := formatShellReply(output, truncated, err)
	return reply, nil
}

// shellArgs mirrors the JSON Schema in Parameters. TimeoutSec
// defaults to 0 → caller fills in defaultTimeoutSec.
type shellArgs struct {
	Command    string `json:"command"`
	TimeoutSec int    `json:"timeout_sec,omitempty"`
}

// formatShellReply renders the tool result string the LLM
// sees. It always includes the exit code (or "(signal)" for
// killed processes) so the LLM can distinguish success from
// failure. Output is included verbatim with the truncation
// marker already appended by cappedWriter.
func formatShellReply(output string, truncated bool, runErr error) string {
	exitLine := "exit 0"
	if runErr != nil {
		if ee, ok := runErr.(*exec.ExitError); ok {
			exitLine = fmt.Sprintf("exit %d", ee.ExitCode())
		} else {
			exitLine = fmt.Sprintf("error: %v", runErr)
		}
	}
	marker := ""
	if truncated {
		marker = truncationMarker
	}
	return fmt.Sprintf("%s%s\n--- %s", output, marker, exitLine)
}

// cappedWriter is an io.Writer that captures up to `limit`
// bytes, then stops recording (further writes are silently
// dropped — the child process keeps running, we just don't
// allocate more memory for it). Truncated reports whether any
// write was dropped.
type cappedWriter struct {
	limit     int
	buf       bytes.Buffer
	truncated bool
}

func (w *cappedWriter) Write(p []byte) (int, error) {
	remaining := w.limit - w.buf.Len()
	if remaining <= 0 {
		w.truncated = true
		return len(p), nil
	}
	if len(p) > remaining {
		w.buf.Write(p[:remaining])
		w.truncated = true
		return len(p), nil
	}
	w.buf.Write(p)
	return len(p), nil
}

func (w *cappedWriter) Bytes() string   { return w.buf.String() }
func (w *cappedWriter) Truncated() bool { return w.truncated }

// trim returns the first n bytes of s followed by "..." if s
// was longer. Used to embed a snippet of a timed-out command's
// output in the error message without dumping 100 KB.
func trim(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
