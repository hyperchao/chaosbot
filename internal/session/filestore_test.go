package session_test

import (
	"bytes"
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
	if err := fs.Append(ctx, "s1", 0, msgs); err != nil {
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
	if err := fs.Append(ctx, "s1", 0, []provider.Message{
		mustMessage(t, provider.RoleUser, "a"),
		mustMessage(t, provider.RoleAssistant, "b"),
	}); err != nil {
		t.Fatalf("Append 1: %v", err)
	}
	if err := fs.Append(ctx, "s1", 2, []provider.Message{
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
	if err == nil {
		t.Fatal("want error for missing session")
	}
	if !os.IsNotExist(err) {
		t.Errorf("err = %v, want os.ErrNotExist", err)
	}
}

func TestFileStore_AppendEmptyNoOp(t *testing.T) {
	fs, dir := newStore(t)
	if err := fs.Append(context.Background(), "s1", 0, nil); err != nil {
		t.Fatalf("Append nil: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "s1.jsonl")); !os.IsNotExist(err) {
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
		if err := fs.Append(ctx, id, 0, []provider.Message{
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
	if err := fs.Append(ctx, "s1", 0, []provider.Message{
		mustMessage(t, provider.RoleUser, "hi"),
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := fs.Delete(ctx, "s1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "s1.jsonl")); !os.IsNotExist(err) {
		t.Errorf("file should be gone, stat err = %v", err)
	}
	_, err := fs.Load(ctx, "s1")
	if !os.IsNotExist(err) {
		t.Errorf("Load after Delete: err = %v, want os.ErrNotExist", err)
	}
}

func TestFileStore_DeleteNotExistIsIdempotent(t *testing.T) {
	fs, _ := newStore(t)
	if err := fs.Delete(context.Background(), "nope"); err != nil {
		t.Errorf("Delete on non-existent: err = %v, want nil", err)
	}
}

func TestFileStore_LoadCorruptLine_Skipped(t *testing.T) {
	fs, dir := newStore(t)
	path := filepath.Join(dir, "s1.jsonl")
	content := `{"role":"user","content":"hi","l":0}
not valid json
{"role":"assistant","content":"ok","l":1}
`
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, err := fs.Load(context.Background(), "s1")
	if err != nil {
		t.Fatalf("corrupt line should be skipped silently; got err = %v", err)
	}
	if len(got) != 2 || got[0].Content != "hi" || got[1].Content != "ok" {
		t.Errorf("got = %+v, want [hi, ok]", got)
	}
}

func TestFileStore_AppendLargeOutput(t *testing.T) {
	fs, _ := newStore(t)
	big := strings.Repeat("a", 1_500_000)
	msgs := []provider.Message{
		{Role: provider.RoleUser, Content: big},
	}
	if err := fs.Append(context.Background(), "s1", 0, msgs); err != nil {
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
	if len(id) != 13 {
		t.Errorf("len(id) = %d, want 13", len(id))
	}
	if id[8] != '-' {
		t.Errorf("id[8] = %q, want '-'", id[8])
	}
}

// TestFileStore_IncrementalAppendEquivalence demonstrates the
// contract that the cli relies on: appending messages in
// chunks produces the same end state as a single append.
func TestFileStore_IncrementalAppendEquivalence(t *testing.T) {
	fs, _ := newStore(t)
	ctx := context.Background()
	full := []provider.Message{
		{Role: provider.RoleUser, Content: "a"},
		{Role: provider.RoleAssistant, Content: "b"},
		{Role: provider.RoleUser, Content: "c"},
		{Role: provider.RoleAssistant, Content: "d"},
	}
	// Simulate the cli pattern: Append(history[offset:]) per turn.
	if err := fs.Append(ctx, "s1", 0, full[:1]); err != nil {
		t.Fatalf("Append 1: %v", err)
	}
	// First "turn" added a user msg and a tool/assistant
	// response (assistant). The cli appends what it has.
	if err := fs.Append(ctx, "s1", 1, full[1:2]); err != nil {
		t.Fatalf("Append 2: %v", err)
	}
	if err := fs.Append(ctx, "s1", 2, full[2:]); err != nil {
		t.Fatalf("Append 3: %v", err)
	}
	got, err := fs.Load(ctx, "s1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != len(full) {
		t.Errorf("len = %d, want %d", len(got), len(full))
	}
	for i, m := range got {
		if m.Content != full[i].Content {
			t.Errorf("msg[%d].Content = %q, want %q", i, m.Content, full[i].Content)
		}
	}
}

// TestFileStore_RoundTripToolCall verifies that messages
// with ToolCalls (which contain a []byte Arguments field)
// round-trip correctly through JSONL.
func TestFileStore_RoundTripToolCall(t *testing.T) {
	fs, _ := newStore(t)
	ctx := context.Background()
	original := []provider.Message{
		{
			Role:    provider.RoleAssistant,
			Content: "",
			ToolCalls: []provider.ToolCall{
				{ID: "c1", Name: "read_file", Arguments: []byte(`{"path":"/foo"}`)},
			},
		},
		{
			Role:       provider.RoleTool,
			ToolCallID: "c1",
			Name:       "read_file",
			Content:    "file contents here",
		},
	}
	if err := fs.Append(ctx, "s1", 0, original); err != nil {
		t.Fatalf("Append: %v", err)
	}
	got, err := fs.Load(ctx, "s1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if len(got[0].ToolCalls) != 1 {
		t.Fatalf("len(ToolCalls) = %d, want 1", len(got[0].ToolCalls))
	}
	tc := got[0].ToolCalls[0]
	if tc.ID != "c1" || tc.Name != "read_file" {
		t.Errorf("tc = %+v, want id=c1 name=read_file", tc)
	}
	if !bytes.Equal(tc.Arguments, []byte(`{"path":"/foo"}`)) {
		t.Errorf("Arguments = %q, want %q", tc.Arguments, `{"path":"/foo"}`)
	}
	if got[1].ToolCallID != "c1" || got[1].Content != "file contents here" {
		t.Errorf("tool msg = %+v, want tool_call_id=c1", got[1])
	}
}

// TestSaveSummary_LoadSummary_Roundtrip verifies a saved
// summary can be loaded back with the same fields.
func TestSaveSummary_LoadSummary_Roundtrip(t *testing.T) {
	fs, _ := newStore(t)
	id := "sess-1"
	want := session.SummaryInfo{Content: "summary text", Cursor: 5, Tokens: 3}
	if err := fs.SaveSummary(context.Background(), id, want); err != nil {
		t.Fatalf("SaveSummary: %v", err)
	}
	got, err := fs.LoadSummary(context.Background(), id)
	if err != nil {
		t.Fatalf("LoadSummary: %v", err)
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// TestLoadSummary_NotExist verifies LoadSummary returns
// os.ErrNotExist when no summary has been saved.
func TestLoadSummary_NotExist(t *testing.T) {
	fs, _ := newStore(t)
	_, err := fs.LoadSummary(context.Background(), "nope")
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("err = %v, want os.ErrNotExist", err)
	}
}

// TestSaveSummary_Overwrites verifies a second save replaces
// the first; we don't accumulate orphan summary files.
func TestSaveSummary_Overwrites(t *testing.T) {
	fs, dir := newStore(t)
	id := "sess-overwrite"
	if err := fs.SaveSummary(context.Background(), id, session.SummaryInfo{Content: "first", Cursor: 1, Tokens: 1}); err != nil {
		t.Fatalf("SaveSummary first: %v", err)
	}
	if err := fs.SaveSummary(context.Background(), id, session.SummaryInfo{Content: "second", Cursor: 2, Tokens: 2}); err != nil {
		t.Fatalf("SaveSummary second: %v", err)
	}
	got, err := fs.LoadSummary(context.Background(), id)
	if err != nil {
		t.Fatalf("LoadSummary: %v", err)
	}
	if got.Content != "second" || got.Cursor != 2 {
		t.Errorf("got %+v, want {second, 2}", got)
	}
	matches, err := filepath.Glob(filepath.Join(dir, id+".summary*"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(matches) != 1 {
		t.Errorf("summary file count = %d, want 1 (no orphans)", len(matches))
	}
}

// TestDelete_RemovesSummary verifies Delete clears both the
// history file and the summary sidecar.
func TestDelete_RemovesSummary(t *testing.T) {
	fs, dir := newStore(t)
	id := "sess-del"
	if err := fs.Append(context.Background(), id, 0, []provider.Message{mustMessage(t, provider.RoleUser, "hi")}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := fs.SaveSummary(context.Background(), id, session.SummaryInfo{Content: "s", Cursor: 1, Tokens: 1}); err != nil {
		t.Fatalf("SaveSummary: %v", err)
	}
	if err := fs.Delete(context.Background(), id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, id+".jsonl")); !os.IsNotExist(err) {
		t.Errorf("jsonl still present after Delete")
	}
	if _, err := os.Stat(filepath.Join(dir, id+".summary.json")); !os.IsNotExist(err) {
		t.Errorf("summary still present after Delete")
	}
}

func TestSaveSummary_WriteFails_ReadOnlyDir(t *testing.T) {
	fs, dir := newStore(t)
	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	defer os.Chmod(dir, 0700)
	err := fs.SaveSummary(context.Background(), "will-fail", session.SummaryInfo{Content: "s", Cursor: 1, Tokens: 1})
	if err == nil {
		t.Fatal("SaveSummary on read-only dir: got nil, want error")
	}
}

func TestAppend_WriteFails_ReadOnlyDir(t *testing.T) {
	fs, dir := newStore(t)
	if err := fs.Append(context.Background(), "existing", 0, []provider.Message{mustMessage(t, provider.RoleUser, "existing")}); err != nil {
		t.Fatalf("Append setup: %v", err)
	}
	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	defer os.Chmod(dir, 0700)
	err := fs.Append(context.Background(), "new", 0, []provider.Message{mustMessage(t, provider.RoleUser, "new")})
	if err == nil {
		t.Fatal("Append on read-only dir: got nil, want error")
	}
}

func TestDelete_FileNotExist(t *testing.T) {
	fs, _ := newStore(t)
	err := fs.Delete(context.Background(), "never-existed")
	if err != nil {
		t.Errorf("Delete non-existent: %v, want nil", err)
	}
}

// TestList_WorksWithoutSummary verifies sessions with no
// sidecar are still listed; List does not require summary files.
func TestList_WorksWithoutSummary(t *testing.T) {
	fs, _ := newStore(t)
	if err := fs.Append(context.Background(), "no-sum", 0, []provider.Message{mustMessage(t, provider.RoleUser, "hi")}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := fs.Append(context.Background(), "with-sum", 0, []provider.Message{mustMessage(t, provider.RoleUser, "hi")}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := fs.SaveSummary(context.Background(), "with-sum", session.SummaryInfo{Content: "s", Cursor: 1, Tokens: 1}); err != nil {
		t.Fatalf("SaveSummary: %v", err)
	}
	ids, err := fs.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := map[string]bool{"no-sum": true, "with-sum": true}
	if len(ids) != 2 {
		t.Errorf("len(ids) = %d, want 2 (ids = %v)", len(ids), ids)
	}
	for _, id := range ids {
		if !want[id] {
			t.Errorf("unexpected id %q", id)
		}
	}
}

// TestFileStore_PartialFirstMessage_RetryRecovers simulates the
// real failure mode: Append fails mid-flush, leaving a partial
// JSON line at the end of the file. The next Append retries with
// the same line_ids. With the leading-newline format
// ("\n{json}" per line) the partial is separated from the
// retry's first line by a "\n", so the retry's first message
// is recovered as its own line and dedup fills the rest.
//
// Without the leading-newline format the retry's first message
// would glue to the partial, fail JSON parse, and be silently
// dropped — leaving a zero-value at idx 0 in the deduped slice.
func TestFileStore_PartialFirstMessage_RetryRecovers(t *testing.T) {
	fs, dir := newStore(t)
	ctx := context.Background()
	id := "partial-first"

	msgs := []provider.Message{
		mustMessage(t, provider.RoleUser, "m1"),
		mustMessage(t, provider.RoleAssistant, "m2"),
		mustMessage(t, provider.RoleUser, "m3"),
	}

	// Simulate a partial write of the FIRST message of a fresh
	// file: file contains the start of m1's JSON, no trailing
	// newline. The leading-"\n" Append would normally put "\n"
	// before the content; the absence of that "\n" is exactly
	// what a mid-flush failure leaves on disk.
	path := filepath.Join(dir, id+".jsonl")
	partial := []byte(`{"l":0,"role":"user","content":"m1`)
	if err := os.WriteFile(path, partial, 0600); err != nil {
		t.Fatalf("WriteFile partial: %v", err)
	}

	// Retry: Append all 3 messages with the same offset.
	if err := fs.Append(ctx, id, 0, msgs); err != nil {
		t.Fatalf("Append retry: %v", err)
	}

	// All 3 messages must be recoverable. The partial m1 is
	// skipped on read; retry's m1 lands at idx 0, m2 at 1, m3
	// at 2. Without the leading-"\n" fix, retry's m1 would be
	// glued to the partial and dropped → len(got) == 2.
	got, err := fs.Load(ctx, id)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != len(msgs) {
		t.Fatalf("len = %d, want %d (retry's first message lost to partial-glue)", len(got), len(msgs))
	}
	for i, want := range []string{"m1", "m2", "m3"} {
		if got[i].Content != want {
			t.Errorf("got[%d] = %q, want %q", i, got[i].Content, want)
		}
	}
}

// TestFileStore_PartialMidBatch_RetryDedups covers the case
// where Append writes some messages cleanly, then fails mid-flush
// on a later message, then retries. The dedup-by-line_id path
// must produce the same final state as a single successful
// Append of the full batch.
func TestFileStore_PartialMidBatch_RetryDedups(t *testing.T) {
	fs, dir := newStore(t)
	ctx := context.Background()
	id := "partial-mid"

	msgs := []provider.Message{
		mustMessage(t, provider.RoleUser, "m1"),
		mustMessage(t, provider.RoleAssistant, "m2"),
		mustMessage(t, provider.RoleUser, "m3"),
	}

	// Manually craft the "first attempt left some complete lines
	// plus a partial" state. After a hypothetical successful
	// Append(msgs) the file would be "\n{m1}\n{m2}\n{m3}\n".
	// A failure at m3 leaves "\n{m1}\n{m2}\n{partial-m3".
	attempt1 := "\n{\"l\":0,\"role\":\"user\",\"content\":\"m1\"}\n{\"l\":1,\"role\":\"assistant\",\"content\":\"m2\"}\n{\"l\":2,\"role\":\"user\",\"content\":\"m3"
	path := filepath.Join(dir, id+".jsonl")
	if err := os.WriteFile(path, []byte(attempt1), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Retry the full batch.
	if err := fs.Append(ctx, id, 0, msgs); err != nil {
		t.Fatalf("Append retry: %v", err)
	}

	got, err := fs.Load(ctx, id)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	for i, want := range []string{"m1", "m2", "m3"} {
		if got[i].Content != want {
			t.Errorf("got[%d] = %q, want %q", i, got[i].Content, want)
		}
	}
}

// TestLoadFrom_OffsetZero verifies offset 0 returns the full
// history (equivalent to Load).
func TestLoadFrom_OffsetZero(t *testing.T) {
	fs, _ := newStore(t)
	ctx := context.Background()
	msgs := []provider.Message{
		mustMessage(t, provider.RoleUser, "a"),
		mustMessage(t, provider.RoleAssistant, "b"),
		mustMessage(t, provider.RoleUser, "c"),
	}
	if err := fs.Append(ctx, "s", 0, msgs); err != nil {
		t.Fatalf("Append: %v", err)
	}
	got, err := fs.LoadFrom(ctx, "s", 0)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if len(got) != len(msgs) {
		t.Fatalf("len = %d, want %d", len(got), len(msgs))
	}
	for i, m := range got {
		if m.Content != msgs[i].Content {
			t.Errorf("[%d] = %q, want %q", i, m.Content, msgs[i].Content)
		}
	}
}

// TestLoadFrom_OffsetMid verifies a non-zero offset returns
// only the tail without materializing the prefix in memory.
func TestLoadFrom_OffsetMid(t *testing.T) {
	fs, _ := newStore(t)
	ctx := context.Background()
	msgs := []provider.Message{
		mustMessage(t, provider.RoleUser, "a"),
		mustMessage(t, provider.RoleAssistant, "b"),
		mustMessage(t, provider.RoleUser, "c"),
		mustMessage(t, provider.RoleAssistant, "d"),
		mustMessage(t, provider.RoleUser, "e"),
	}
	if err := fs.Append(ctx, "s", 0, msgs); err != nil {
		t.Fatalf("Append: %v", err)
	}
	got, err := fs.LoadFrom(ctx, "s", 3)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Content != "d" || got[1].Content != "e" {
		t.Errorf("got = %+v, want [d, e]", got)
	}
}

// TestLoadFrom_OffsetBeyondEnd verifies offset > line count
// returns a wrapped ErrStaleCursor so Resume can fall back.
func TestLoadFrom_OffsetBeyondEnd(t *testing.T) {
	fs, _ := newStore(t)
	ctx := context.Background()
	msgs := []provider.Message{
		mustMessage(t, provider.RoleUser, "a"),
		mustMessage(t, provider.RoleAssistant, "b"),
	}
	if err := fs.Append(ctx, "s", 0, msgs); err != nil {
		t.Fatalf("Append: %v", err)
	}
	_, err := fs.LoadFrom(ctx, "s", 99)
	if err == nil {
		t.Fatal("want error when offset > line count")
	}
	if !errors.Is(err, session.ErrStaleCursor) {
		t.Errorf("err = %v, want wraps ErrStaleCursor", err)
	}
}

// TestLoadFrom_NotExist verifies LoadFrom returns
// os.ErrNotExist when the session file doesn't exist.
func TestLoadFrom_NotExist(t *testing.T) {
	fs, _ := newStore(t)
	_, err := fs.LoadFrom(context.Background(), "nope", 0)
	if err == nil {
		t.Fatal("want error for missing session")
	}
	if !os.IsNotExist(err) {
		t.Errorf("err = %v, want os.ErrNotExist", err)
	}
}

// TestLoadFrom_NegativeOffset verifies a defensive check for
// negative offsets (programming error, not user input).
func TestLoadFrom_NegativeOffset(t *testing.T) {
	fs, _ := newStore(t)
	_, err := fs.LoadFrom(context.Background(), "s", -1)
	if err == nil {
		t.Fatal("want error for negative offset")
	}
}

// TestLoadFrom_LargeLineAtOffset verifies a single message
// exceeding the default scanner buffer round-trips correctly
// even when loaded from a non-zero offset.
func TestLoadFrom_LargeLineAtOffset(t *testing.T) {
	fs, _ := newStore(t)
	ctx := context.Background()
	// Small prefix (2 lines), then one huge line.
	if err := fs.Append(ctx, "s", 0, []provider.Message{
		mustMessage(t, provider.RoleUser, "prefix1"),
		mustMessage(t, provider.RoleAssistant, "prefix2"),
	}); err != nil {
		t.Fatalf("Append prefix: %v", err)
	}
	big := strings.Repeat("x", 1_500_000)
	if err := fs.Append(ctx, "s", 2, []provider.Message{
		{Role: provider.RoleUser, Content: big},
	}); err != nil {
		t.Fatalf("Append big: %v", err)
	}
	got, err := fs.LoadFrom(ctx, "s", 2)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Content != big {
		t.Errorf("large message mismatch, len = %d, want %d", len(got[0].Content), len(big))
	}
}
