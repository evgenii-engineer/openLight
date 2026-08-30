// Package vectorstore defines the vector database contract used by the
// memory subsystem and ships a no-op implementation for degraded and
// disabled modes.
//
// The interface exists so no Qdrant type ever leaks into the rest of
// openLight: the concrete client lives in the qdrant sub-package and is
// the only file that imports it. Swapping backends means writing one
// more adapter, not touching retrieval, ingestion, or the agent.
package vectorstore

import (
	"context"
	"errors"
)

// ErrUnavailable marks a transient backend failure. Callers treat it as
// "retry later", never as "drop the data".
var ErrUnavailable = errors.New("vectorstore: unavailable")

// Point is one indexed vector plus the payload returned with a search
// hit. Payloads stay small — ids and short provenance strings — because
// the authoritative chunk text lives in SQLite.
type Point struct {
	ID      string
	Vector  []float32
	Payload map[string]any
}

// Filter narrows a search. An empty filter matches everything.
type Filter struct {
	// Types restricts results to these source types.
	Types []string
}

// Hit is one search result.
type Hit struct {
	ID      string
	Score   float32
	Payload map[string]any
}

// Store is the vector database contract.
type Store interface {
	// EnsureCollection creates the collection with the given vector
	// width if it does not exist. Safe to call repeatedly.
	EnsureCollection(ctx context.Context, dimensions int) error

	// Upsert writes points, replacing any with the same id.
	Upsert(ctx context.Context, points []Point) error

	// Search returns the nearest points to the query vector.
	Search(ctx context.Context, vector []float32, limit int, filter Filter) ([]Hit, error)

	// Delete removes points by id.
	Delete(ctx context.Context, ids []string) error

	// DeleteCollection drops the whole collection. Used by
	// `memory reindex --all` before a full rebuild.
	DeleteCollection(ctx context.Context) error

	// Count reports how many points the collection holds.
	Count(ctx context.Context) (int64, error)

	// Health reports whether the backend is reachable.
	Health(ctx context.Context) error

	// Close releases the connection.
	Close() error
}

// Noop is the store used when the vector backend is disabled or could
// not be constructed. Reads return nothing and writes report
// ErrUnavailable, so the ingestion queue keeps the work pending instead
// of marking it done — the data is never silently lost.
type Noop struct{}

func (Noop) EnsureCollection(context.Context, int) error { return ErrUnavailable }
func (Noop) Upsert(context.Context, []Point) error       { return ErrUnavailable }
func (Noop) Search(context.Context, []float32, int, Filter) ([]Hit, error) {
	return nil, nil
}
func (Noop) Delete(context.Context, []string) error { return nil }
func (Noop) DeleteCollection(context.Context) error { return nil }
func (Noop) Count(context.Context) (int64, error)   { return 0, nil }
func (Noop) Health(context.Context) error           { return ErrUnavailable }
func (Noop) Close() error                           { return nil }
