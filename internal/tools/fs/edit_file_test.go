package fs_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"chaosbot/internal/tools/fs"
)

func TestEditFile_Unique_Replaces(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("alpha\nbeta\ngamma\n"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	te := &fs.EditFileTool{}
	reply, err := te.Invoke(context.Background(), mustJSON(t, map[string]string{
		"path":     path,
		"old_text": "beta",
		"new_text": "BETA",
	}))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if !strings.Contains(reply, "replaced") {
		t.Errorf("reply = %q, want contains 'replaced'", reply)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "alpha\nBETA\ngamma\n" {
		t.Errorf("file = %q, want %q", got, "alpha\\nBETA\\ngamma\\n")
	}
}

func TestEditFile_MultiLineAnchor(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	original := "func foo() {\n\treturn 1\n}\n\nfunc bar() {\n\treturn 2\n}\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	te := &fs.EditFileTool{}
	_, err := te.Invoke(context.Background(), mustJSON(t, map[string]string{
		"path":     path,
		"old_text": "func foo() {\n\treturn 1\n}",
		"new_text": "func foo() {\n\treturn 42\n}",
	}))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}

	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), "return 42") {
		t.Errorf("file = %q, want contains 'return 42'", got)
	}
	if strings.Contains(string(got), "return 1\n") {
		t.Errorf("file still contains old 'return 1', got %q", got)
	}
}

func TestEditFile_NotFound_Errors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	te := &fs.EditFileTool{}
	_, err := te.Invoke(context.Background(), mustJSON(t, map[string]string{
		"path":     path,
		"old_text": "world",
		"new_text": "earth",
	}))
	if err == nil {
		t.Fatal("want error for missing anchor")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("err = %v, want mentions 'not found'", err)
	}

	// File is unchanged.
	got, _ := os.ReadFile(path)
	if string(got) != "hello" {
		t.Errorf("file = %q, want %q", got, "hello")
	}
}

func TestEditFile_NonUnique_Errors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("foo\nfoo\nfoo\n"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	te := &fs.EditFileTool{}
	_, err := te.Invoke(context.Background(), mustJSON(t, map[string]string{
		"path":     path,
		"old_text": "foo",
		"new_text": "bar",
	}))
	if err == nil {
		t.Fatal("want error for non-unique anchor")
	}
	if !strings.Contains(err.Error(), "found 3 times") {
		t.Errorf("err = %v, want mentions 'found 3 times'", err)
	}
	// File unchanged.
	got, _ := os.ReadFile(path)
	if string(got) != "foo\nfoo\nfoo\n" {
		t.Errorf("file = %q, want unchanged", got)
	}
}

func TestEditFile_EmptyOldText_Errors(t *testing.T) {
	te := &fs.EditFileTool{}
	_, err := te.Invoke(context.Background(), mustJSON(t, map[string]string{
		"path":     filepath.Join(t.TempDir(), "f.txt"),
		"old_text": "",
		"new_text": "x",
	}))
	if err == nil {
		t.Fatal("want error for empty old_text")
	}
	if !strings.Contains(err.Error(), "empty anchor") {
		t.Errorf("err = %v, want mentions 'empty anchor'", err)
	}
}

func TestEditFile_EmptyNewText_Deletes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("alpha\nbeta\ngamma\n"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	te := &fs.EditFileTool{}
	if _, err := te.Invoke(context.Background(), mustJSON(t, map[string]string{
		"path":     path,
		"old_text": "beta\n",
		"new_text": "",
	})); err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "alpha\ngamma\n" {
		t.Errorf("file = %q, want %q", got, "alpha\\ngamma\\n")
	}
}

func TestEditFile_InvalidArgs(t *testing.T) {
	te := &fs.EditFileTool{}
	_, err := te.Invoke(context.Background(), json.RawMessage(`{"old_text":"x","new_text":"y"}`))
	if err == nil {
		t.Fatal("want error for missing path")
	}
	if !strings.Contains(err.Error(), "path is required") {
		t.Errorf("err = %v, want mentions 'path is required'", err)
	}
}

func TestEditFile_ContextCanceled(t *testing.T) {
	te := &fs.EditFileTool{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := te.Invoke(ctx, mustJSON(t, map[string]string{
		"path":     filepath.Join(t.TempDir(), "f.txt"),
		"old_text": "x",
		"new_text": "y",
	}))
	if err != context.Canceled {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}
