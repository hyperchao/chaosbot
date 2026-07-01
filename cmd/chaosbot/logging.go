package main

import (
	"fmt"
	"io"
	"log/slog"
	"os"
)

// Log level names. Maps directly to slog.Level for the
// debug/info/warn/error cases; "none" is a special pseudo-level
// that disables all output (handler is wired to io.Discard).
const (
	levelNone  = "none"
	levelDebug = "debug"
	levelInfo  = "info"
	levelWarn  = "warn"
	levelError = "error"
)

// setupLogging configures the global slog default with a
// TextHandler.
//
//	--log-level none   : io.Discard (no output anywhere; file
//	                     is NOT opened even if --log-file is set)
//	--log-level <lvl>  : stderr, filtered at <lvl>
//	--log-level <lvl> + --log-file <path> : file only, filtered
//	                     at <lvl>. We intentionally do NOT also
//	                     write to stderr: debug-level output
//	                     would otherwise interleave with REPL
//	                     prompts and tool output on the terminal.
//
// Returns an error for invalid level names or file open
// failures. Callers should fail fast — bad logging config is
// a configuration bug, not a runtime condition.
func setupLogging(logFile, levelName string) error {
	if levelName == levelNone {
		// The level filter is irrelevant here: even with the
		// default LevelInfo, formatted records land in
		// io.Discard. nil HandlerOptions picks that default.
		slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
		return nil
	}

	level, err := parseLogLevel(levelName)
	if err != nil {
		return err
	}

	var w io.Writer = os.Stderr
	if logFile != "" {
		f, ferr := os.OpenFile(logFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if ferr != nil {
			return fmt.Errorf("open log file %s: %w", logFile, ferr)
		}
		w = f
	}

	slog.SetDefault(slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: level})))
	return nil
}

// parseLogLevel maps a CLI level name to a slog.Level.
// "none" is not handled here — setupLogging treats it as a
// special case (io.Discard) before calling this.
func parseLogLevel(name string) (slog.Level, error) {
	switch name {
	case levelDebug:
		return slog.LevelDebug, nil
	case levelInfo:
		return slog.LevelInfo, nil
	case levelWarn:
		return slog.LevelWarn, nil
	case levelError:
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("invalid log level %q (want %s|%s|%s|%s|%s)", name, levelNone, levelDebug, levelInfo, levelWarn, levelError)
	}
}
