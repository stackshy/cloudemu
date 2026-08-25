// Package snapshot defines the optional per-driver snapshotting contract a mock
// implements so persist can capture and restore its state identity-preservingly.
//
// It is a leaf package — it imports only context and encoding/json — so any mock
// can implement Snapshottable without risking an import cycle back into persist,
// exactly like internal/memstore. persist type-asserts each driver to this
// interface and falls back to its bespoke path for mocks that do not implement
// it yet.
package snapshot

import (
	"context"
	"encoding/json"
)

// Snapshottable is implemented by a service mock that can serialize its entire
// in-memory state to JSON and restore it under the same identities (resource
// IDs, cross-reference keys), so a snapshot/restore round-trip is transparent to
// clients — unlike a driver-level replay that mints fresh IDs.
//
// includeAssets selects whether large object bodies (e.g. S3 object bytes) are
// captured; false yields a metadata-only snapshot. Restore is called on a
// freshly built (empty) mock.
type Snapshottable interface {
	Snapshot(ctx context.Context, includeAssets bool) (json.RawMessage, error)
	Restore(ctx context.Context, data json.RawMessage) error
}
