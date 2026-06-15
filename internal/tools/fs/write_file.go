package fs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// WriteFileTool writes content to a file, replacing any existing
// content. See docs/phases/phase-05-tools.md §05-2 for the
// contract: atomic via tmp+rename, parent directories created
// on demand, 0600 permissions, *os.PathError propagates verbatim
// (so the LLM sees "no such file or directory" / "permission
// denied" in tool-message form).
type WriteFileTool struct{}

// Name implements agent.Tool.
func (t *WriteFileTool) Name() string { return "write_file" }

// Description implements agent.Tool.
func (t *WriteFileTool) Description() string {
	return "Write content to a file, replacing any existing " +
		"content. Atomic via tmp+rename: a crash mid-write " +
		"leaves the previous file intact. Parent directories " +
		"are created as needed. Always overwrites (no append " +
		"mode); use edit_file for in-place patches."
}

// Parameters implements agent.Tool.
func (t *WriteFileTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["path", "content"],
		"properties": {
			"path":    {"type": "string", "description": "absolute or relative file path"},
			"content": {"type": "string", "description": "full file content (replaces any existing content)"}
		},
		"additionalProperties": false
	}`)
}

// Invoke implements agent.Tool.
func (t *WriteFileTool) Invoke(ctx context.Context, args json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var a writeFileArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("write_file: invalid args: %w", err)
	}
	if a.Path == "" {
		return "", errors.New("write_file: path is required")
	}
	if err := writeFileAtomic(a.Path, []byte(a.Content)); err != nil {
		return "", err
	}
	return fmt.Sprintf("wrote %d bytes to %s", len(a.Content), a.Path), nil
}

type writeFileArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// writeFileAtomic writes data to path via a temp file in the
// same directory, fsyncs, and renames over the destination.
// On a single filesystem os.Rename is atomic, so a concurrent
// reader either sees the old content or the new — never a
// half-written file.
//
// Steps:
//  1. MkdirAll on the parent directory (no error if it exists).
//  2. Create path + ".tmp" with 0600 (overwrite if it exists).
//  3. Write data, Sync, Close.
//  4. Rename path + ".tmp" → path (atomic on same FS).
//
// On any failure between (2) and (4) the temp file is
// removed; the original destination (if any) is left intact.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
