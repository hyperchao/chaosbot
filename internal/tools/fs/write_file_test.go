package fs_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"chaosbot/internal/tools/fs"
)

func TestWriteFile_CreatesNew(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "subdir", "new.txt")

	tw := &fs.WriteFileTool{}
	reply, err := tw.Invoke(context.Background(), mustJSON(t, map[string]string{
		"path":    path,
		"content": "hello world",
	}))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if !strings.Contains(reply, "11 bytes") {
		t.Errorf("reply = %q, want contains '11 bytes'", reply)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "hello world" {
		t.Errorf("file content = %q, want %q", got, "hello world")
	}
}

func TestWriteFile_Overwrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("OLD"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	tw := &fs.WriteFileTool{}
	if _, err := tw.Invoke(context.Background(), mustJSON(t, map[string]string{
		"path":    path,
		"content": "NEW",
	})); err != nil {
		t.Fatalf("Invoke: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "NEW" {
		t.Errorf("file content = %q, want %q", got, "NEW")
	}
}

func TestWriteFile_Atomic_NoTempLeftover(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")

	tw := &fs.WriteFileTool{}
	if _, err := tw.Invoke(context.Background(), mustJSON(t, map[string]string{
		"path":    path,
		"content": "ok",
	})); err != nil {
		t.Fatalf("Invoke: %v", err)
	}

	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("temp file should be gone, stat err = %v", err)
	}
}

func TestWriteFile_Atomic_OriginalIntactOnFailure(t *testing.T) {
	// Failure mode: the parent directory is a file, not a dir.
	// MkdirAll on its parent fails; the original file (if any)
	// is never touched.
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	path := filepath.Join(blocker, "child", "f.txt")

	tw := &fs.WriteFileTool{}
	_, err := tw.Invoke(context.Background(), mustJSON(t, map[string]string{
		"path":    path,
		"content": "x",
	}))
	if err == nil {
		t.Fatal("want error when parent is a file, got nil")
	}

	// blocker is untouched.
	got, err := os.ReadFile(blocker)
	if err != nil {
		t.Fatalf("ReadFile blocker: %v", err)
	}
	if string(got) != "not a directory" {
		t.Errorf("blocker = %q, want %q", got, "not a directory")
	}
}

func TestWriteFile_Permissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions not enforced on Windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")

	tw := &fs.WriteFileTool{}
	if _, err := tw.Invoke(context.Background(), mustJSON(t, map[string]string{
		"path":    path,
		"content": "x",
	})); err != nil {
		t.Fatalf("Invoke: %v", err)
	}

	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := st.Mode().Perm(); perm != 0o600 {
		t.Errorf("perm = %o, want 0o600", perm)
	}
}

func TestWriteFile_InvalidArgs(t *testing.T) {
	tw := &fs.WriteFileTool{}
	_, err := tw.Invoke(context.Background(), json.RawMessage(`{"content": "no path"}`))
	if err == nil {
		t.Fatal("want error for missing path")
	}
	if !strings.Contains(err.Error(), "path is required") {
		t.Errorf("err = %v, want mentions 'path is required'", err)
	}
}

func TestWriteFile_BadJSON(t *testing.T) {
	tw := &fs.WriteFileTool{}
	_, err := tw.Invoke(context.Background(), json.RawMessage(`not json`))
	if err == nil {
		t.Fatal("want error for malformed JSON args")
	}
	if !strings.Contains(err.Error(), "invalid args") {
		t.Errorf("err = %v, want mentions 'invalid args'", err)
	}
}

func TestWriteFile_ContextCanceled(t *testing.T) {
	tw := &fs.WriteFileTool{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := tw.Invoke(ctx, mustJSON(t, map[string]string{
		"path":    filepath.Join(t.TempDir(), "f.txt"),
		"content": "x",
	}))
	if err != context.Canceled {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func mustJSON(t *testing.T, m map[string]string) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}
