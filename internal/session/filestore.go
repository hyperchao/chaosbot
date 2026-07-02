package session

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"chaosbot/internal/provider"
)

// FileStore persists sessions as JSONL files in a directory.
// One file per session: <dir>/<id>.jsonl.
// Each line is a JSON-encoded provider.Message.
type FileStore struct {
	dir string
}

// NewFileStore creates a FileStore rooted at dir. The directory
// is created if it doesn't exist. Returns an error if dir cannot
// be created.
func NewFileStore(dir string) (*FileStore, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("session: create dir: %w", err)
	}
	return &FileStore{dir: dir}, nil
}

// path returns the on-disk path for a session ID.
func (fs *FileStore) path(id string) string {
	return filepath.Join(fs.dir, id+".jsonl")
}

// summaryPath returns the sidecar path for a session's summary.
func (fs *FileStore) summaryPath(id string) string {
	return filepath.Join(fs.dir, id+".summary.json")
}

// Append appends messages wrapped in storedLine with sequential
// line_ids starting from offset. Each line is JSON-marshaled
// and written independently (line_id in the envelope makes
// partial-write recovery possible: corrupt JSON is skipped on
// read; duplicates with the same line_id are deduplicated).
// Single fsync after all writes. On error, the caller does NOT
// advance its cursor — the next retry assigns the same line_ids
// to the same messages and dedup-on-read handles the overlap.
//
// On-disk layout: each line is written as "\n{json}" (newline
// before the content, not after). The leading newline of every
// line doubles as a partial-write separator: if a flush fails
// mid-message the partial bytes are followed by the next
// attempt's leading "\n", so the retry's first message is read
// as its own line instead of being glued to the partial. Empty
// leading lines are skipped by loadFromOffset.
func (fs *FileStore) Append(ctx context.Context, id string, offset int, messages []provider.Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(messages) == 0 {
		return nil
	}
	f, err := os.OpenFile(fs.path(id), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("session: open %s: %w", id, err)
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	for i, m := range messages {
		sl := storedLine{Message: m, LineID: offset + i}
		line, err := json.Marshal(sl)
		if err != nil {
			return fmt.Errorf("session: marshal msg %d: %w", i, err)
		}
		// Write the leading newline FIRST so a partial write at
		// the end of this loop iteration is followed by the next
		// attempt's leading "\n", which separates the two on read.
		if err := w.WriteByte('\n'); err != nil {
			return fmt.Errorf("session: write newline msg %d: %w", i, err)
		}
		if _, err := w.Write(line); err != nil {
			return fmt.Errorf("session: write msg %d: %w", i, err)
		}
	}
	if err := w.Flush(); err != nil {
		return fmt.Errorf("session: flush: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("session: sync: %w", err)
	}
	return nil
}

// SummaryInfo persists the last computed summary alongside
// the history it covers. Cursor is the count of leading
// history messages the summary covers (history[Cursor:] is
// verbatim).
type SummaryInfo struct {
	Content string `json:"content"`
	Cursor  int    `json:"cursor"`
	Tokens  int    `json:"tokens"`
}

// SaveSummary atomically overwrites the summary sidecar for
// the session. Atomic via tmp file + rename. Returns an
// error on disk failure.
func (fs *FileStore) SaveSummary(ctx context.Context, id string, info SummaryInfo) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := json.Marshal(info)
	if err != nil {
		return fmt.Errorf("session: marshal summary: %w", err)
	}
	final := fs.summaryPath(id)
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("session: write summary tmp: %w", err)
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("session: rename summary: %w", err)
	}
	return nil
}

// LoadSummary reads the summary sidecar. Returns
// (SummaryInfo, nil) on success, (_, os.ErrNotExist) when no
// summary has ever been saved, (_, wrapped-error) on
// read/decode failure.
func (fs *FileStore) LoadSummary(ctx context.Context, id string) (SummaryInfo, error) {
	if err := ctx.Err(); err != nil {
		return SummaryInfo{}, err
	}
	data, err := os.ReadFile(fs.summaryPath(id))
	if err != nil {
		return SummaryInfo{}, err
	}
	var info SummaryInfo
	if uerr := json.Unmarshal(data, &info); uerr != nil {
		return SummaryInfo{}, fmt.Errorf("session: corrupt summary: %w", uerr)
	}
	return info, nil
}

// Load returns the full history for the given ID. Streaming
// decode via bufio.Reader to handle arbitrarily long lines
// (large tool outputs can exceed any fixed buffer).
func (fs *FileStore) Load(ctx context.Context, id string) ([]provider.Message, error) {
	return fs.loadFromOffset(ctx, id, 0)
}

// LoadFrom returns history[offset:] without materializing the
// skipped prefix in memory. Returns a wrapped ErrStaleCursor
// when offset exceeds the line count (caller should fall back
// to Load). offset == 0 is equivalent to Load.
func (fs *FileStore) LoadFrom(ctx context.Context, id string, offset int) ([]provider.Message, error) {
	if offset < 0 {
		return nil, fmt.Errorf("session: LoadFrom: negative offset %d", offset)
	}
	return fs.loadFromOffset(ctx, id, offset)
}

// loadFromOffset reads lines from the session file. Each line
// is unmarshaled as storedLine (backward-compatible with old
// format: missing "l" field decodes as LineID == 0).
//
// Corrupt JSON lines (from partial writes) are silently
// skipped. Duplicate line_ids (same line_id appearing more
// than once, from retried Appends) are deduplicated: the
// last occurrence wins.
//
// offset is a line_id cursor: messages with LineID <= offset
// are skipped. offset == 0 loads everything (including old
// format messages with LineID == 0 — those are never skipped
// when offset == 0). Returns wrapped ErrStaleCursor when
// offset > 0 and every line has LineID <= offset (the
// cursor has advanced past the end of the file).
func (fs *FileStore) loadFromOffset(ctx context.Context, id string, offset int) ([]provider.Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := os.Open(fs.path(id))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := bufio.NewReader(f)
	var messages []provider.Message
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			trimmed := bytes.TrimRight(line, "\n")
			if len(trimmed) > 0 {
				var sl storedLine
				if uerr := json.Unmarshal(trimmed, &sl); uerr != nil {
					continue
				}
				if sl.LineID < offset {
					continue
				}
				idx := sl.LineID - offset
				if idx == len(messages) {
					messages = append(messages, sl.Message)
				} else {
					messages[idx] = sl.Message
				}
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return messages, fmt.Errorf("session: read: %w", err)
		}
	}
	if offset > 0 && len(messages) == 0 {
		return nil, fmt.Errorf("session: LoadFrom offset %d: %w", offset, ErrStaleCursor)
	}
	return messages, nil
}

// List returns all session IDs, newest first (by file mtime).
func (fs *FileStore) List(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	matches, err := filepath.Glob(filepath.Join(fs.dir, "*.jsonl"))
	if err != nil {
		return nil, fmt.Errorf("session: glob: %w", err)
	}
	type entry struct {
		id    string
		mtime time.Time
	}
	entries := make([]entry, 0, len(matches))
	for _, m := range matches {
		fi, err := os.Stat(m)
		if err != nil {
			continue
		}
		id := strings.TrimSuffix(filepath.Base(m), ".jsonl")
		entries = append(entries, entry{id: id, mtime: fi.ModTime()})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].mtime.After(entries[j].mtime)
	})
	ids := make([]string, len(entries))
	for i, e := range entries {
		ids[i] = e.id
	}
	return ids, nil
}

// Delete removes the session file and its summary sidecar.
// Idempotent: missing files are not an error.
func (fs *FileStore) Delete(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Remove(fs.path(id)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("session: delete: %w", err)
	}
	if err := os.Remove(fs.summaryPath(id)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("session: delete summary: %w", err)
	}
	return nil
}

// NewID returns a session ID of the form "<date>-<random4>".
// Example: "20260616-a3f7". Not cryptographically secure;
// collisions within the same day are possible but acceptable
// for single-user MVP.
func NewID() (string, error) {
	date := time.Now().UTC().Format("20060102")
	n, err := rand.Int(rand.Reader, big.NewInt(0x10000))
	if err != nil {
		return "", fmt.Errorf("session: random: %w", err)
	}
	return fmt.Sprintf("%s-%04x", date, n), nil
}
