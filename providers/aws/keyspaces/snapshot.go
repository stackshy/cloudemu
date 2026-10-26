package keyspaces

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	ksdriver "github.com/stackshy/cloudemu/v2/services/keyspaces/driver"
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

// Snapshot captures the mock's entire state as JSON. includeAssets is unused. Keyspaces holds
// schema metadata, not bulk object bodies.
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

	// Copy the tag maps under the lock: json.Marshal runs after the unlock, and
	// TagResource mutates the inner maps in place.
	m.mu.RLock()
	if len(m.tags) > 0 {
		snap.Tags = copyTagMaps(m.tags)
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

	m.mu.Lock()
	m.normalizeRestoredTablesLocked()
	m.mu.Unlock()

	if snap.Tags != nil {
		m.mu.Lock()
		m.tags = m.canonicalTagsLocked(snap.Tags)
		m.mu.Unlock()
	}

	return nil
}

// normalizeRestoredTablesLocked clears the ttl and clientSideTimestamps
// status of restored tables that never enabled them. An older build stored
// DISABLED for both on every table, which GetTable then reported although real
// Keyspaces omits the members (see enabledOnly). The caller holds the write
// lock, and the tables store is already restored.
func (m *Mock) normalizeRestoredTablesLocked() {
	for _, key := range m.tables.Keys() {
		m.tables.Update(key, func(t ksdriver.Table) ksdriver.Table {
			t.TTLStatus = enabledOnly(t.TTLStatus)
			t.ClientSideTimestamps = enabledOnly(t.ClientSideTimestamps)

			return t
		})
	}
}

// canonicalTagsLocked keys restored tags by the canonical ARN of the keyspace
// or table they belong to. Tag reads and writes resolve to the canonical ARN,
// but an older build stored tags under whatever ARN the caller passed (for
// example the slash-less table form) and accepted ARNs of resources that don't
// exist. A non-canonical entry for an existing resource is merged into the
// canonical one, with the canonical entry's values winning on a key clash. An
// entry that names no existing resource is dropped: no API can reach it, and
// keeping it would hand its tags to a resource created later under that ARN.
// The caller holds the write lock, and the stores are already restored.
func (m *Mock) canonicalTagsLocked(in map[string]map[string]string) map[string]map[string]string {
	out := make(map[string]map[string]string, len(in))

	for arn, tags := range in {
		if m.existingResourceARN(arn) == arn {
			out[arn] = copyTags(tags)
		}
	}

	for arn, tags := range in {
		canonical := m.existingResourceARN(arn)
		if canonical == "" || canonical == arn || arn+"/" != canonical {
			continue
		}

		cur := out[canonical]
		if cur == nil {
			cur = make(map[string]string, len(tags))
			out[canonical] = cur
		}

		for k, v := range tags {
			if _, ok := cur[k]; !ok {
				cur[k] = v
			}
		}
	}

	return out
}

func copyTagMaps(in map[string]map[string]string) map[string]map[string]string {
	out := make(map[string]map[string]string, len(in))
	for arn, tags := range in {
		out[arn] = copyTags(tags)
	}

	return out
}
