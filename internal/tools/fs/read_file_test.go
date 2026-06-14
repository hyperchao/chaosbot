package fs_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"chaosbot/internal/agent"
	fstools "chaosbot/internal/tools/fs"
)

// Compile-time assertion: ReadFileTool must implement agent.Tool.
var _ agent.Tool = (*fstools.ReadFileTool)(nil)

func TestReadFile_Whole(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(path, []byte("hello\nworld\nfoo\n"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	tool := &fstools.ReadFileTool{}
	got, err := tool.Invoke(context.Background(), mustArgs(t, map[string]any{"path": path}))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	want := "1\thello\n2\tworld\n3\tfoo\n"
	if got != want {
		t.Errorf("Invoke = %q, want %q", got, want)
	}
}

func TestReadFile_LineRange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "five.txt")
	if err := os.WriteFile(path, []byte("a\nb\nc\nd\ne\n"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	tool := &fstools.ReadFileTool{}
	got, err := tool.Invoke(context.Background(), mustArgs(t, map[string]any{
		"path":       path,
		"start_line": 2,
		"end_line":   3,
	}))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	want := "2\tb\n3\tc\n"
	if got != want {
		t.Errorf("Invoke = %q, want %q", got, want)
	}
}

func TestReadFile_Binary_Rejects(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bin.dat")
	// NUL byte inside the first 512 bytes — within sniff window.
	if err := os.WriteFile(path, []byte("abc\x00def"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	tool := &fstools.ReadFileTool{}
	_, err := tool.Invoke(context.Background(), mustArgs(t, map[string]any{"path": path}))
	if err == nil {
		t.Fatal("Invoke: want error for binary, got nil")
	}
	if !strings.Contains(err.Error(), "binary") {
		t.Errorf("Invoke: error %q, want it to mention 'binary'", err)
	}
}

func TestReadFile_NegativeLine_Rejects(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "any.txt")
	if err := os.WriteFile(path, []byte("a\nb\nc\n"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	tool := &fstools.ReadFileTool{}
	_, err := tool.Invoke(context.Background(), mustArgs(t, map[string]any{
		"path":       path,
		"start_line": -1,
		"end_line":   5,
	}))
	if err == nil {
		t.Fatal("Invoke: want error for negative start_line, got nil")
	}
	if !strings.Contains(err.Error(), "start_line") {
		t.Errorf("Invoke: error %q, want it to mention 'start_line'", err)
	}
}

func TestReadFile_StartGreaterEnd_Rejects(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "any.txt")
	if err := os.WriteFile(path, []byte("a\nb\nc\nd\ne\n"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	tool := &fstools.ReadFileTool{}
	_, err := tool.Invoke(context.Background(), mustArgs(t, map[string]any{
		"path":       path,
		"start_line": 3,
		"end_line":   2,
	}))
	if err == nil {
		t.Fatal("Invoke: want error for start_line > end_line, got nil")
	}
	if !strings.Contains(err.Error(), "start_line") || !strings.Contains(err.Error(), "end_line") {
		t.Errorf("Invoke: error %q, want it to mention both line numbers", err)
	}
}

func TestReadFile_NotFound_Propagates(t *testing.T) {
	tool := &fstools.ReadFileTool{}
	_, err := tool.Invoke(context.Background(), mustArgs(t, map[string]any{
		"path": filepath.Join(t.TempDir(), "no-such-file.txt"),
	}))
	if err == nil {
		t.Fatal("Invoke: want error for missing file, got nil")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Invoke: error %v, want errors.Is(err, os.ErrNotExist)", err)
	}
}

func TestReadFile_LineCap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "long.txt")
	var b strings.Builder
	for i := 1; i <= 2500; i++ {
		b.WriteString("line\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	tool := &fstools.ReadFileTool{}
	got, err := tool.Invoke(context.Background(), mustArgs(t, map[string]any{
		"path":     path,
		"end_line": 999999,
	}))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}

	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	// Expect: 2000 numbered lines + 1 marker line = 2001
	if len(lines) != 2001 {
		t.Fatalf("Invoke: got %d lines, want 2001 (2000 + marker)", len(lines))
	}
	if !strings.Contains(lines[2000], "2000-line cap reached") {
		t.Errorf("last line = %q, want it to mention '2000-line cap reached'", lines[2000])
	}
	if !strings.Contains(lines[2000], "500 more") {
		t.Errorf("last line = %q, want it to mention '500 more'", lines[2000])
	}
	// first line should be "1\tline"
	if lines[0] != "1\tline" {
		t.Errorf("lines[0] = %q, want %q", lines[0], "1\tline")
	}
	// 2000th line should be "2000\tline"
	if lines[1999] != "2000\tline" {
		t.Errorf("lines[1999] = %q, want %q", lines[1999], "2000\tline")
	}
}

func TestReadFile_LongLine_FailsFast(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "huge.txt")
	// Single line of 300 KB — exceeds the 256 KB scanner max, so
	// the tool fails fast with bufio.ErrTooLong rather than
	// producing a truncated "1\t" + cap-marker payload with no
	// useful content.
	huge := strings.Repeat("a", 300*1024)
	if err := os.WriteFile(path, []byte(huge), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	tool := &fstools.ReadFileTool{}
	_, err := tool.Invoke(context.Background(), mustArgs(t, map[string]any{"path": path}))
	if err == nil {
		t.Fatal("Invoke: want error for line > 256 KB, got nil")
	}
	if !errors.Is(err, bufio.ErrTooLong) {
		t.Errorf("Invoke: error %v, want errors.Is(err, bufio.ErrTooLong)", err)
	}
}

func mustArgs(t *testing.T, m map[string]any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	return b
}
