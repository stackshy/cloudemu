package datacatalog

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// dataCatalogSnapshot is the full serialized state of the Data Catalog mock.
// Each store holds fully-exported driver value types keyed by their full GCP
// resource name, so each round-trips through the generic memstore helper — no
// field promotion is needed. The wired deps (m.opts) and the RWMutex are
// intentionally not serialized.
type dataCatalogSnapshot struct {
	EntryGroups  json.RawMessage `json:"entryGroups,omitempty"`
	Entries      json.RawMessage `json:"entries,omitempty"`
	TagTemplates json.RawMessage `json:"tagTemplates,omitempty"`
	Tags         json.RawMessage `json:"tags,omitempty"`
}

// Snapshot captures every entry group, entry, tag template, and tag as JSON.
// includeAssets is unused — Data Catalog is control-plane only and holds no bulk
// object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var snap dataCatalogSnapshot

	dumps := []struct {
		dst *json.RawMessage
		fn  func() ([]byte, error)
	}{
		{&snap.EntryGroups, m.entryGroups.Snapshot},
		{&snap.Entries, m.entries.Snapshot},
		{&snap.TagTemplates, m.tagTemplates.Snapshot},
		{&snap.Tags, m.tags.Snapshot},
	}

	for _, d := range dumps {
		b, err := d.fn()
		if err != nil {
			return nil, fmt.Errorf("datacatalog: snapshot store: %w", err)
		}

		*d.dst = b
	}

	return json.Marshal(snap)
}

// Restore rebuilds every entry group, entry, tag template, and tag under its
// original resource name.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap dataCatalogSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("datacatalog: parse snapshot: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	loads := []struct {
		src json.RawMessage
		fn  func([]byte) error
	}{
		{snap.EntryGroups, m.entryGroups.LoadSnapshot},
		{snap.Entries, m.entries.LoadSnapshot},
		{snap.TagTemplates, m.tagTemplates.LoadSnapshot},
		{snap.Tags, m.tags.LoadSnapshot},
	}

	for _, l := range loads {
		if len(l.src) == 0 {
			continue
		}

		if err := l.fn(l.src); err != nil {
			return fmt.Errorf("datacatalog: restore store: %w", err)
		}
	}

	return nil
}
