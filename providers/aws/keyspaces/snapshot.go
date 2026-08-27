package keyspaces

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// keyspacesSnapshot is the full serialized state of the Amazon Keyspaces mock.
// The keyspaces/tables/udts stores hold fully-exported ksdriver value types, so
// each round-trips through the generic memstore helper keyed by its store key
// (keyspace name; "keyspace/table"; "keyspace/typename"). The mu-guarded tags
// map (keyed by resource ARN) is captured beside them. The wired opts are not
// serialized. A fresh mock is seeded with the system keyspaces, and the snapshot
// carries them too, so LoadSnapshot's merge is idempotent.
type keyspacesSnapshot struct {
	Keyspaces json.RawMessage              `json:"keyspaces,omitempty"`
	Tables    json.RawMessage              `json:"tables,omitempty"`
	UDTs      json.RawMessage              `json:"udts,omitempty"`
	Tags      map[string]map[string]string `json:"tags,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// Keyspaces holds schema metadata, not bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	var snap keyspacesSnapshot

	dumps := []struct {
		dst *json.RawMessage
		fn  func() ([]byte, error)
	}{
		{&snap.Keyspaces, m.keyspaces.Snapshot},
		{&snap.Tables, m.tables.Snapshot},
		{&snap.UDTs, m.udts.Snapshot},
	}

	for _, d := range dumps {
		b, err := d.fn()
		if err != nil {
			return nil, fmt.Errorf("keyspaces: snapshot store: %w", err)
		}

		*d.dst = b
	}

	m.mu.RLock()
	if len(m.tags) > 0 {
		snap.Tags = m.tags
	}
	m.mu.RUnlock()

	return json.Marshal(snap)
}

// Restore rebuilds the mock's state under the original identities: every
// keyspace name, "keyspace/table" and "keyspace/typename" key, and tag ARN is
// preserved so a client's identifiers still resolve.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap keyspacesSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("keyspaces: parse snapshot: %w", err)
	}

	loads := []struct {
		src json.RawMessage
		fn  func([]byte) error
	}{
		{snap.Keyspaces, m.keyspaces.LoadSnapshot},
		{snap.Tables, m.tables.LoadSnapshot},
		{snap.UDTs, m.udts.LoadSnapshot},
	}

	for _, l := range loads {
		if len(l.src) == 0 {
			continue
		}

		if err := l.fn(l.src); err != nil {
			return fmt.Errorf("keyspaces: restore store: %w", err)
		}
	}

	if snap.Tags != nil {
		m.mu.Lock()
		m.tags = snap.Tags
		m.mu.Unlock()
	}

	return nil
}
