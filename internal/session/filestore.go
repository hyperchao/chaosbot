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

// FileStore persists sessions as NDJSON files in a directory.
// One file per session: <dir>/<id>.ndjson.
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
	return filepath.Join(fs.dir, id+".ndjson")
}

// Append appends messages to the session file. Atomic at the OS
// level via O_APPEND. Uses bufio.Writer to batch small writes
// into one syscall.
func (fs *FileStore) Append(ctx context.Context, id string, messages []provider.Message) error {
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
	for _, m := range messages {
		line, err := json.Marshal(m)
		if err != nil {
			return fmt.Errorf("session: marshal: %w", err)
		}
		if _, err := w.Write(line); err != nil {
			return fmt.Errorf("session: write: %w", err)
		}
		if err := w.WriteByte('\n'); err != nil {
			return fmt.Errorf("session: write: %w", err)
		}
	}
	if err := w.Flush(); err != nil {
		return fmt.Errorf("session: flush: %w", err)
	}
	return f.Sync()
}

// Load returns the full history for the given ID. Streaming
// decode via bufio.Reader to handle arbitrarily long lines
// (large tool outputs can exceed any fixed buffer).
func (fs *FileStore) Load(ctx context.Context, id string) ([]provider.Message, error) {
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
				var m provider.Message
				if uerr := json.Unmarshal(trimmed, &m); uerr != nil {
					return messages, fmt.Errorf("session: corrupt line: %w", uerr)
				}
				messages = append(messages, m)
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return messages, fmt.Errorf("session: read: %w", err)
		}
	}
	return messages, nil
}

// List returns all session IDs, newest first (by file mtime).
func (fs *FileStore) List(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	matches, err := filepath.Glob(filepath.Join(fs.dir, "*.ndjson"))
	if err != nil {
		return nil, fmt.Errorf("session: glob: %w", err)
	}
	type entry struct {
		id   string
		mtime time.Time
	}
	entries := make([]entry, 0, len(matches))
	for _, m := range matches {
		fi, err := os.Stat(m)
		if err != nil {
			continue
		}
		id := strings.TrimSuffix(filepath.Base(m), ".ndjson")
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

// Delete removes the session file. Idempotent.
func (fs *FileStore) Delete(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	err := os.Remove(fs.path(id))
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("session: delete: %w", err)
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
