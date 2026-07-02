// Package session provides the persistence boundary for chaosbot
// conversations. It is a leaf package: imports nothing from
// chaosbot. Concrete implementations (FileStore, future backends)
// implement the Store interface.
package session

import (
	"context"
	"errors"
	"os"

	"chaosbot/internal/provider"
)

// storedLine is the on-disk representation of one message.
// Embeds provider.Message so line_id is an additional JSON
// field in each line.
type storedLine struct {
	provider.Message
	LineID int `json:"l"`
}

// ErrStaleCursor is returned by LoadFrom when the requested
// offset exceeds the number of messages in the session file.
// Callers (typically Resume) should treat the persisted cursor
// as stale and fall back to a full Load.
var ErrStaleCursor = errors.New("session: cursor beyond end of history")

// Store is the persistence boundary. Implementations must be
// safe for sequential use (the agent loop is single-threaded).
type Store interface {
	// Append appends new messages to the session file.
	// offset is the line_id of the first message; messages are
	// stored with sequential line_ids starting from there. On
	// error, the caller should NOT advance its cursor so the
	// next retry assigns the same line_ids (duplicates are
	// deduplicated on read).
	Append(ctx context.Context, id string, offset int, messages []provider.Message) error

	// Load returns the full history for the given ID.
	// Returns os.ErrNotExist if the session doesn't exist.
	Load(ctx context.Context, id string) ([]provider.Message, error)

	// LoadFrom returns history[offset:] for the given ID.
	// offset is the count of leading messages to skip (used
	// by Resume to avoid loading the already-summarized
	// prefix into memory). Returns os.ErrNotExist if the
	// session doesn't exist, or a wrapped ErrStaleCursor if
	// offset exceeds the line count (caller should fall back
	// to Load). offset == 0 returns the full history.
	LoadFrom(ctx context.Context, id string, offset int) ([]provider.Message, error)

	// SaveSummary persists the last computed summary. Atomic
	// overwrite of a sidecar; absence is a valid state meaning
	// "no summary yet".
	SaveSummary(ctx context.Context, id string, info SummaryInfo) error

	// LoadSummary reads the persisted summary. Returns
	// os.ErrNotExist when no summary has ever been saved.
	LoadSummary(ctx context.Context, id string) (SummaryInfo, error)

	// List returns all session IDs, newest first.
	List(ctx context.Context) ([]string, error)

	// Delete removes the session file. Idempotent:
	// deleting a non-existent session is not an error.
	Delete(ctx context.Context, id string) error
}

// NoopStore discards every operation. Used as a stand-in
// when no session persistence is configured (no API key,
// no sessions dir) so the agent can run in pure in-memory
// mode without DI panics. Load returns os.ErrNotExist so
// Resume cleanly reports a missing session.
type NoopStore struct{}

func (NoopStore) Append(_ context.Context, _ string, _ int, _ []provider.Message) error {
	return nil
}

func (NoopStore) Load(_ context.Context, _ string) ([]provider.Message, error) {
	return nil, os.ErrNotExist
}

func (NoopStore) LoadFrom(_ context.Context, _ string, _ int) ([]provider.Message, error) {
	return nil, os.ErrNotExist
}

func (NoopStore) SaveSummary(_ context.Context, _ string, _ SummaryInfo) error {
	return nil
}

func (NoopStore) LoadSummary(_ context.Context, _ string) (SummaryInfo, error) {
	return SummaryInfo{}, os.ErrNotExist
}

func (NoopStore) List(_ context.Context) ([]string, error) {
	return nil, nil
}

func (NoopStore) Delete(_ context.Context, _ string) error {
	return nil
}
