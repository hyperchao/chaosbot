package session_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"chaosbot/internal/provider"
	"chaosbot/internal/session"
)

// newStore creates a FileStore in a temp dir. Returns the
// store and the temp dir path (for tests that want to
// inspect the filesystem).
func newStore(t *testing.T) (*session.FileStore, string) {
	t.Helper()
	dir := t.TempDir()
	fs, err := session.NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	return fs, dir
}

func mustMessage(t *testing.T, role provider.Role, content string) provider.Message {
	t.Helper()
	return provider.Message{Role: role, Content: content}
}

func TestFileStore_AppendLoadRoundtrip(t *testing.T) {
	fs, _ := newStore(t)
	ctx := context.Background()
	msgs := []provider.Message{
		mustMessage(t, provider.RoleUser, "hi"),
		mustMessage(t, provider.RoleAssistant, "hello"),
		mustMessage(t, provider.RoleUser, "how are you?"),
	}
	if err := fs.Append(ctx, "s1", msgs); err != nil {
		t.Fatalf("Append: %v", err)
	}
	got, err := fs.Load(ctx, "s1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != len(msgs) {
		t.Fatalf("len = %d, want %d", len(got), len(msgs))
	}
	for i, m := range got {
		if m.Role != msgs[i].Role || m.Content != msgs[i].Content {
			t.Errorf("msg[%d] = %+v, want %+v", i, m, msgs[i])
		}
	}
}

func TestFileStore_AppendIncrements(t *testing.T) {
	fs, _ := newStore(t)
	ctx := context.Background()
	if err := fs.Append(ctx, "s1", []provider.Message{
		mustMessage(t, provider.RoleUser, "a"),
		mustMessage(t, provider.RoleAssistant, "b"),
	}); err != nil {
		t.Fatalf("Append 1: %v", err)
	}
	if err := fs.Append(ctx, "s1", []provider.Message{
		mustMessage(t, provider.RoleUser, "c"),
	}); err != nil {
		t.Fatalf("Append 2: %v", err)
	}
	got, err := fs.Load(ctx, "s1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("len = %d, want 3", len(got))
	}
	if got[2].Content != "c" {
		t.Errorf("got[2].Content = %q, want %q", got[2].Content, "c")
	}
}

func TestFileStore_LoadNotExist(t *testing.T) {
	fs, _ := newStore(t)
	_, err := fs.Load(context.Background(), "nope")
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("err = %v, want os.ErrNotExist", err)
	}
}

func TestFileStore_AppendEmptyNoOp(t *testing.T) {
	fs, dir := newStore(t)
	if err := fs.Append(context.Background(), "s1", nil); err != nil {
		t.Fatalf("Append nil: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "s1.ndjson")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("empty Append should not create file, stat err = %v", err)
	}
}

func TestFileStore_ListEmpty(t *testing.T) {
	fs, _ := newStore(t)
	ids, err := fs.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("len(ids) = %d, want 0", len(ids))
	}
}

func TestFileStore_ListMultiple(t *testing.T) {
	fs, _ := newStore(t)
	ctx := context.Background()
	for _, id := range []string{"a", "b", "c"} {
		if err := fs.Append(ctx, id, []provider.Message{
			mustMessage(t, provider.RoleUser, id),
		}); err != nil {
			t.Fatalf("Append %s: %v", id, err)
		}
	}
	ids, err := fs.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(ids) != 3 {
		t.Errorf("len = %d, want 3", len(ids))
	}
	// Newest first: c, b, a (file mtime order)
	for i, want := range []string{"c", "b", "a"} {
		if ids[i] != want {
			t.Errorf("ids[%d] = %q, want %q", i, ids[i], want)
		}
	}
}

func TestFileStore_Delete(t *testing.T) {
	fs, dir := newStore(t)
	ctx := context.Background()
	if err := fs.Append(ctx, "s1", []provider.Message{
		mustMessage(t, provider.RoleUser, "hi"),
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := fs.Delete(ctx, "s1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "s1.ndjson")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("file should be gone, stat err = %v", err)
	}
	_, err := fs.Load(ctx, "s1")
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Load after Delete: err = %v, want os.ErrNotExist", err)
	}
}

func TestFileStore_DeleteNotExistIsIdempotent(t *testing.T) {
	fs, _ := newStore(t)
	if err := fs.Delete(context.Background(), "nope"); err != nil {
		t.Errorf("Delete on non-existent: err = %v, want nil", err)
	}
}

func TestFileStore_LoadCorruptLine(t *testing.T) {
	fs, dir := newStore(t)
	// Write a valid line, then garbage, then another valid line.
	path := filepath.Join(dir, "s1.ndjson")
	content := `{"role":"user","content":"hi"}
not valid json
{"role":"assistant","content":"ok"}
`
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, err := fs.Load(context.Background(), "s1")
	if err == nil {
		t.Fatal("want error on corrupt line")
	}
	if !strings.Contains(err.Error(), "corrupt") {
		t.Errorf("err = %v, want contains 'corrupt'", err)
	}
	// First valid message should be returned before the error.
	if len(got) != 1 || got[0].Content != "hi" {
		t.Errorf("got = %+v, want one 'hi' message", got)
	}
}

func TestFileStore_AppendLargeOutput(t *testing.T) {
	fs, _ := newStore(t)
	// Single message with 1.5 MB content (exceeds default 64KB scanner buffer).
	big := strings.Repeat("a", 1_500_000)
	msgs := []provider.Message{
		{Role: provider.RoleUser, Content: big},
	}
	if err := fs.Append(context.Background(), "s1", msgs); err != nil {
		t.Fatalf("Append: %v", err)
	}
	got, err := fs.Load(context.Background(), "s1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 1 || got[0].Content != big {
		t.Errorf("large message roundtrip failed, len(got) = %d", len(got))
	}
}

func TestFileStore_NewIDFormat(t *testing.T) {
	id, err := session.NewID()
	if err != nil {
		t.Fatalf("NewID: %v", err)
	}
	// Format: YYYYMMDD-XXXX (8 digits, dash, 4 hex chars)
	if len(id) != 13 {
		t.Errorf("len(id) = %d, want 13", len(id))
	}
	if id[8] != '-' {
		t.Errorf("id[8] = %q, want '-'", id[8])
	}
}
