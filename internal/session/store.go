// Package session provides the persistence boundary for chaosbot
// conversations. It is a leaf package: imports nothing from
// chaosbot. Concrete implementations (FileStore, future backends)
// implement the Store interface.
package session

import (
	"context"
	"os"

	"chaosbot/internal/provider"
)

// Store is the persistence boundary. Implementations must be
// safe for sequential use (the agent loop is single-threaded).
type Store interface {
	// Append appends new messages to the session file.
	// The caller is responsible for passing only new messages
	// (not the full history). The store simply appends them.
	Append(ctx context.Context, id string, messages []provider.Message) error

	// Load returns the full history for the given ID.
	// Returns os.ErrNotExist if the session doesn't exist.
	Load(ctx context.Context, id string) ([]provider.Message, error)

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

func (NoopStore) Append(_ context.Context, _ string, _ []provider.Message) error {
	return nil
}

func (NoopStore) Load(_ context.Context, _ string) ([]provider.Message, error) {
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
