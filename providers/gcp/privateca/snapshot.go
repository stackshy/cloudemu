package privateca

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// privatecaSnapshot is the full serialized state of the Certificate Authority
// Service mock. Every store holds fully-exported pcadriver value types keyed by
// their full GCP resource name, so each round-trips through the generic memstore
// helper — no field promotion is needed. opSeq is the operation-name counter,
// captured beside the stores so restored operation ids do not collide with fresh
// ones. The wired deps (m.opts) and the RWMutex are intentionally not serialized.
type privatecaSnapshot struct {
	Pools        json.RawMessage `json:"pools,omitempty"`
	Authorities  json.RawMessage `json:"authorities,omitempty"`
	Templates    json.RawMessage `json:"templates,omitempty"`
	Certificates json.RawMessage `json:"certificates,omitempty"`
	Operations   json.RawMessage `json:"operations,omitempty"`
	OpSeq        uint64          `json:"opSeq,omitempty"`
}

// Snapshot captures every CA pool, certificate authority, certificate template,
// certificate, and operation as JSON. includeAssets is unused — CA Service admin
// is control-plane only and holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var snap privatecaSnapshot

	dumps := []struct {
		dst *json.RawMessage
		fn  func() ([]byte, error)
	}{
		{&snap.Pools, m.pools.Snapshot},
		{&snap.Authorities, m.authorities.Snapshot},
		{&snap.Templates, m.templates.Snapshot},
		{&snap.Certificates, m.certificates.Snapshot},
		{&snap.Operations, m.operations.Snapshot},
	}

	for _, d := range dumps {
		b, err := d.fn()
		if err != nil {
			return nil, fmt.Errorf("privateca: snapshot store: %w", err)
		}

		*d.dst = b
	}

	snap.OpSeq = m.opSeq.Load()

	return json.Marshal(snap)
}

// Restore rebuilds every CA pool, certificate authority, certificate template,
// certificate, and operation under its original resource name.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap privatecaSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("privateca: parse snapshot: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	loads := []struct {
		src json.RawMessage
		fn  func([]byte) error
	}{
		{snap.Pools, m.pools.LoadSnapshot},
		{snap.Authorities, m.authorities.LoadSnapshot},
		{snap.Templates, m.templates.LoadSnapshot},
		{snap.Certificates, m.certificates.LoadSnapshot},
		{snap.Operations, m.operations.LoadSnapshot},
	}

	for _, l := range loads {
		if len(l.src) == 0 {
			continue
		}

		if err := l.fn(l.src); err != nil {
			return fmt.Errorf("privateca: restore store: %w", err)
		}
	}

	m.opSeq.Store(snap.OpSeq)

	return nil
}
