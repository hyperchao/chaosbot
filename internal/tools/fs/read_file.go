// Package fs hosts the filesystem tools (read / write / edit).
// 05-1 lands read_file; 05-2 adds write_file; 05-3 adds edit_file.
package fs

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

const (
	readFileMaxLines    = 2000
	readFileMaxBytes    = 256 * 1024
	readFileBinarySniff = 512
)

// ReadFileTool reads a text file. See docs/phases/phase-05-tools.md
// §05-1 for the full contract: 1-indexed inclusive [start_line,
// end_line] window, 2000-line / 256 KB output caps with truncation
// markers, NUL-byte sniff (first 512 B) rejects binary, and
// *os.PathError propagates verbatim.
type ReadFileTool struct{}

// Name implements agent.Tool.
func (t *ReadFileTool) Name() string { return "read_file" }

// Description implements agent.Tool.
func (t *ReadFileTool) Description() string {
	return "Read a text file. Returns lines in `cat -n` style " +
		"(`<n>\\t<line>`) so edit_file can pick a unique anchor. " +
		"Binary files (NUL byte in first 512 B) are rejected; " +
		"use shell + xxd for binary inspection. Capped at 2000 " +
		"lines / 256 KB; truncation markers are appended."
}

// Parameters implements agent.Tool.
func (t *ReadFileTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["path"],
		"properties": {
			"path":       {"type": "string", "description": "absolute or relative file path"},
			"start_line": {"type": "integer", "minimum": 1, "description": "1-indexed start line (inclusive, default 1)"},
			"end_line":   {"type": "integer", "minimum": 1, "description": "1-indexed end line (inclusive, default start+1999 or EOF)"}
		},
		"additionalProperties": false
	}`)
}

// Invoke implements agent.Tool.
func (t *ReadFileTool) Invoke(ctx context.Context, args json.RawMessage) (string, error) {
	var a readFileArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("read_file: invalid args: %w", err)
	}
	if a.Path == "" {
		return "", errors.New("read_file: path is required")
	}
	start, end, err := resolveWindow(a.StartLine, a.EndLine)
	if err != nil {
		return "", err
	}

	f, err := os.Open(a.Path)
	if err != nil {
		return "", err // *os.PathError propagates verbatim
	}
	defer f.Close()

	if err := ctx.Err(); err != nil {
		return "", err
	}

	if err := sniffBinary(f); err != nil {
		return "", err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", err
	}

	return renderWindow(f, start, end)
}

// readFileArgs mirrors the JSON Schema in Parameters. StartLine /
// EndLine are *int so we can distinguish "field omitted" (use
// default) from "field present but zero / negative" (validation
// error).
type readFileArgs struct {
	Path      string `json:"path"`
	StartLine *int   `json:"start_line,omitempty"`
	EndLine   *int   `json:"end_line,omitempty"`
}

// resolveWindow applies the defaults and validation. Returns the
// concrete 1-indexed inclusive [start, end] to render. Errors here
// are validation failures; *os.PathError is not in this path.
//
// Defaults (per phase-05 spec):
//
//	StartLine == nil -> 1
//	EndLine   == nil -> start + readFileMaxLines - 1
//
// Validation:
//
//	*StartLine < 1 || *EndLine < 1 -> error
//	*StartLine > *EndLine          -> error
func resolveWindow(startArg, endArg *int) (start, end int, err error) {
	if startArg == nil {
		start = 1
	} else {
		if *startArg < 1 {
			return 0, 0, fmt.Errorf("read_file: start_line must be >= 1, got %d", *startArg)
		}
		start = *startArg
	}
	if endArg == nil {
		end = start + readFileMaxLines - 1
	} else {
		if *endArg < 1 {
			return 0, 0, fmt.Errorf("read_file: end_line must be >= 1, got %d", *endArg)
		}
		end = *endArg
	}
	if start > end {
		return 0, 0, fmt.Errorf("read_file: start_line (%d) > end_line (%d)", start, end)
	}
	return start, end, nil
}

// sniffBinary reads up to readFileBinarySniff bytes from the start of
// f and returns an error if any byte is 0. The seek position after
// this call is the byte just past the last byte read.
func sniffBinary(f *os.File) error {
	var buf [readFileBinarySniff]byte
	n, err := io.ReadFull(f, buf[:])
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return err
	}
	for _, b := range buf[:n] {
		if b == 0 {
			return fmt.Errorf("read_file: %s: appears to be binary; use shell + xxd for inspection", f.Name())
		}
	}
	return nil
}

// renderWindow streams f line-by-line, slices to the requested
// 1-indexed inclusive window, prefixes each line with `<n>\t`, and
// applies the 2000-line / 256 KB caps with truncation markers.
// startLine and endLine are both 1-indexed and must be >= 1
// (validated at the Invoke boundary).
func renderWindow(f *os.File, startLine, endLine int) (string, error) {
	scanner := bufio.NewScanner(f)
	// max token (one line) capped at readFileMaxBytes. Rationale:
	// if one line is already larger than the entire output budget,
	// the cat -n prefix would force an immediate output-cap
	// truncation that strips the line's content, leaving the LLM
	// with "1\t" + 256 KB of prefix bytes + a "truncated" marker
	// and no useful payload. Better to fail fast with
	// bufio.ErrTooLong so the LLM falls back to `shell + head` /
	// `shell + sed -n` for those files.
	scanner.Buffer(make([]byte, 64*1024), readFileMaxBytes)

	var out bytes.Buffer
	out.Grow(readFileMaxBytes)
	totalLines := 0
	windowLines := 0

	for scanner.Scan() {
		totalLines++
		if totalLines < startLine {
			continue
		}
		if totalLines > endLine {
			break
		}
		if windowLines == readFileMaxLines {
			// line cap reached mid-stream; count remaining
			remaining := 1
			for scanner.Scan() {
				remaining++
			}
			fmt.Fprintf(&out, "... [truncated: 2000-line cap reached, file has %d more lines] ...\n", remaining)
			break
		}
		windowLines++
		fmt.Fprintf(&out, "%d\t%s\n", totalLines, scanner.Text())
		if out.Len() >= readFileMaxBytes {
			// byte cap reached; truncate and append marker
			truncated := out.Bytes()[:readFileMaxBytes]
			out.Reset()
			out.Write(truncated)
			fmt.Fprintf(&out, "... [truncated: 256 KB byte cap reached] ...\n")
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return out.String(), nil
}
