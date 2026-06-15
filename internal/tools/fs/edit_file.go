package fs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

// EditFileTool replaces one occurrence of old_text with new_text
// in a file. See docs/phases/phase-05-tools.md §05-3 for the
// contract: old_text must appear exactly once (zero or many
// matches both fail; the LLM should re-read or grow the anchor
// until it is unique). The replacement is atomic via the same
// tmp+rename path as write_file, so a mid-edit crash leaves the
// original file intact. The whole file is read into memory and
// rewritten — fine for text files the agent is expected to edit;
// for binary or multi-megabyte files use shell + sed/python.
type EditFileTool struct{}

// Name implements agent.Tool.
func (t *EditFileTool) Name() string { return "edit_file" }

// Description implements agent.Tool.
func (t *EditFileTool) Description() string {
	return "Replace one occurrence of old_text with new_text " +
		"in a file. The old_text anchor must appear exactly " +
		"once — re-read the file with read_file first and grow " +
		"the anchor (include surrounding lines) until it is " +
		"unique. Zero or many matches both fail without " +
		"modifying the file."
}

// Parameters implements agent.Tool.
func (t *EditFileTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["path", "old_text", "new_text"],
		"properties": {
			"path":     {"type": "string", "description": "absolute or relative file path"},
			"old_text": {"type": "string", "description": "exact text to find (must occur exactly once)"},
			"new_text": {"type": "string", "description": "replacement text (can be empty to delete the anchor)"}
		},
		"additionalProperties": false
	}`)
}

// Invoke implements agent.Tool.
func (t *EditFileTool) Invoke(ctx context.Context, args json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var a editFileArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("edit_file: invalid args: %w", err)
	}
	if a.Path == "" {
		return "", errors.New("edit_file: path is required")
	}
	if a.OldText == "" {
		return "", errors.New("edit_file: old_text is required (empty anchor matches infinitely)")
	}

	content, err := os.ReadFile(a.Path)
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	count := strings.Count(string(content), a.OldText)
	if count == 0 {
		return "", fmt.Errorf("edit_file: old_text not found in %s", a.Path)
	}
	if count > 1 {
		offsets := findAllOffsets(string(content), a.OldText, 5)
		return "", fmt.Errorf("edit_file: old_text found %d times in %s (need exactly 1); first offsets: %v",
			count, a.Path, offsets)
	}

	// Exactly one match — perform the replacement.
	replaced := strings.Replace(string(content), a.OldText, a.NewText, 1)
	if err := writeFileAtomic(a.Path, []byte(replaced)); err != nil {
		return "", err
	}
	return fmt.Sprintf("replaced %d bytes with %d bytes in %s",
		len(a.OldText), len(a.NewText), a.Path), nil
}

type editFileArgs struct {
	Path    string `json:"path"`
	OldText string `json:"old_text"`
	NewText string `json:"new_text"`
}

// findAllOffsets returns byte offsets of needle in s, capped at
// limit; the rest are reported only by count. Used to give the
// LLM a useful error when old_text is ambiguous.
func findAllOffsets(s, needle string, limit int) []int {
	var out []int
	for i := 0; i <= len(s)-len(needle); {
		j := strings.Index(s[i:], needle)
		if j < 0 {
			break
		}
		out = append(out, i+j)
		if len(out) >= limit {
			break
		}
		i += j + len(needle)
	}
	return out
}
