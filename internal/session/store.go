// Package session provides the persistence boundary for chaosbot
// conversations. It is a leaf package: imports nothing from
// chaosbot. Concrete implementations (FileStore, future backends)
// implement the Store interface.
package session

import (
	"context"

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

	// List returns all session IDs, newest first.
	List(ctx context.Context) ([]string, error)

	// Delete removes the session file. Idempotent:
	// deleting a non-existent session is not an error.
	Delete(ctx context.Context, id string) error
}
