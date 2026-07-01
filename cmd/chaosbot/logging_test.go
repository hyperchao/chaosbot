package main

import (
	"bytes"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseLogLevel(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    slog.Level
		wantErr bool
	}{
		{"debug", "debug", slog.LevelDebug, false},
		{"info", "info", slog.LevelInfo, false},
		{"warn", "warn", slog.LevelWarn, false},
		{"error", "error", slog.LevelError, false},
		{"empty", "", 0, true},
		{"none is not parseable here", "none", 0, true},
		{"bogus", "trace", 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseLogLevel(c.in)
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, c.wantErr)
			}
			if !c.wantErr && got != c.want {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestSetupLogging_NoneDisablesAllOutput(t *testing.T) {
	// Even if a log file is requested, "none" should not touch
	// the filesystem and nothing should be written anywhere.
	// (We don't assert Enabled=false because the level filter
	// is irrelevant when the writer is io.Discard.)
	dir := t.TempDir()
	bad := filepath.Join(dir, "no-such-dir", "agent.log")
	if err := setupLogging(bad, "none"); err != nil {
		t.Fatalf("setupLogging(none, bad path): want no error, got %v", err)
	}
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })

	// Actual log calls must produce no output on stderr.
	r, w, _ := os.Pipe()
	oldStderr := os.Stderr
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = oldStderr })
	slog.Error("should not appear")
	w.Close()
	var buf bytes.Buffer
	io.Copy(&buf, r)
	if buf.Len() != 0 {
		t.Errorf("level=none wrote to stderr: %q", buf.String())
	}
}

func TestSetupLogging_WritesToFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.log")
	if err := setupLogging(path, "info"); err != nil {
		t.Fatalf("setupLogging: %v", err)
	}
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })

	slog.Info("hello", "k", "v")

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	if !bytes.Contains(data, []byte("hello")) {
		t.Errorf("log file missing 'hello': %s", data)
	}
	if !bytes.Contains(data, []byte("k=v")) {
		t.Errorf("log file missing attr 'k=v': %s", data)
	}
}

func TestSetupLogging_FileModeDoesNotWriteToStderr(t *testing.T) {
	// When --log-file is set, stderr must stay clean so the
	// REPL prompt and tool output are not interleaved with
	// log lines. We redirect stderr to a pipe and assert the
	// log call writes only to the file.
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.log")
	if err := setupLogging(path, "debug"); err != nil {
		t.Fatalf("setupLogging: %v", err)
	}
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })

	r, w, _ := os.Pipe()
	oldStderr := os.Stderr
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = oldStderr })
	slog.Debug("noisy", "k", "v")
	w.Close()
	var buf bytes.Buffer
	io.Copy(&buf, r)
	if buf.Len() != 0 {
		t.Errorf("file-mode wrote to stderr: %q", buf.String())
	}
	data, _ := os.ReadFile(path)
	if !bytes.Contains(data, []byte("noisy")) {
		t.Errorf("file-mode missing log in file: %s", data)
	}
}

func TestSetupLogging_RejectsInvalidLevel(t *testing.T) {
	if err := setupLogging("", "verbose"); err == nil {
		t.Fatal("setupLogging with invalid level: want error, got nil")
	}
}

func TestSetupLogging_RejectsUnwritableFile(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "no-such-dir", "agent.log")
	if err := setupLogging(bad, "info"); err == nil {
		t.Fatal("setupLogging with bad path: want error, got nil")
	} else if !strings.Contains(err.Error(), "open log file") {
		t.Errorf("err = %v, want it to mention 'open log file'", err)
	}
}
