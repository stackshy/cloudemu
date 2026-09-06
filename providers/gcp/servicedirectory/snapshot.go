package servicedirectory

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// serviceDirectorySnapshot is the full serialized state of the Service Directory
// mock. Each store holds fully-exported driver value types keyed by their full
// GCP resource name, so each round-trips through the generic memstore helper —
// no field promotion is needed. The wired deps (m.opts) and the RWMutex are
// intentionally not serialized.
type serviceDirectorySnapshot struct {
	Namespaces json.RawMessage `json:"namespaces,omitempty"`
	Services   json.RawMessage `json:"services,omitempty"`
	Endpoints  json.RawMessage `json:"endpoints,omitempty"`
}

// Snapshot captures every namespace, service, and endpoint as JSON.
// includeAssets is unused — Service Directory is control-plane only and holds no
// bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var snap serviceDirectorySnapshot

	dumps := []struct {
		dst *json.RawMessage
		fn  func() ([]byte, error)
	}{
		{&snap.Namespaces, m.namespaces.Snapshot},
		{&snap.Services, m.services.Snapshot},
		{&snap.Endpoints, m.endpoints.Snapshot},
	}

	for _, d := range dumps {
		b, err := d.fn()
		if err != nil {
			return nil, fmt.Errorf("servicedirectory: snapshot store: %w", err)
		}

		*d.dst = b
	}

	return json.Marshal(snap)
}

// Restore rebuilds every namespace, service, and endpoint under its original
// resource name.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap serviceDirectorySnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("servicedirectory: parse snapshot: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	loads := []struct {
		src json.RawMessage
		fn  func([]byte) error
	}{
		{snap.Namespaces, m.namespaces.LoadSnapshot},
		{snap.Services, m.services.LoadSnapshot},
		{snap.Endpoints, m.endpoints.LoadSnapshot},
	}

	for _, l := range loads {
		if len(l.src) == 0 {
			continue
		}

		if err := l.fn(l.src); err != nil {
			return fmt.Errorf("servicedirectory: restore store: %w", err)
		}
	}

	return nil
}
