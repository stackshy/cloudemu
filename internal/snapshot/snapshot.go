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
	"reflect"
	"strings"
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

// Discover reflects over the exported struct fields of p (a provider factory,
// passed as a pointer to its struct) and returns the ones whose value
// implements Snapshottable, keyed by a stable service name. The key is the
// field name lowercased ("S3"->"s3", "SecretsManager"->"secretsmanager",
// "EC2"->"ec2", "VPC"->"vpc"): a deterministic, build-independent identifier so
// a snapshot restores into the same service across runs. persist iterates this
// map, so the persisted surface automatically tracks whichever services
// implement Snapshottable — no hand-kept registry that can drift.
//
// This is a one-shot enumeration run at snapshot/restore time (not a hot path).
// A nil pointer field, an unexported field, or a field that does not implement
// Snapshottable contributes nothing.
func Discover(p any) map[string]Snapshottable {
	out := map[string]Snapshottable{}

	v := reflect.ValueOf(p)
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return out
		}

		v = v.Elem()
	}

	if v.Kind() != reflect.Struct {
		return out
	}

	t := v.Type()

	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}

		fv := v.Field(i)
		if fv.Kind() == reflect.Pointer && fv.IsNil() {
			continue
		}

		if s, ok := fv.Interface().(Snapshottable); ok {
			out[strings.ToLower(f.Name)] = s
		}
	}

	return out
}
