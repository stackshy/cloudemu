package cloudfront

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/cloudfront/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// cloudfrontSnapshot is the full serialized state of the CloudFront mock: the
// distribution store keyed by distribution id (each value carries its verbatim
// ConfigXML, tags, and identity), the per-distribution invalidations, and the
// creation counter so restored distributions keep their list order.
type cloudfrontSnapshot struct {
	Distributions json.RawMessage                           `json:"distributions,omitempty"`
	Invalidations map[string]map[string]driver.Invalidation `json:"invalidations,omitempty"`
	Seq           int64                                     `json:"seq,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// CloudFront holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	dists, err := m.dists.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("cloudfront: snapshot distributions: %w", err)
	}

	snap := cloudfrontSnapshot{
		Distributions: dists,
		Invalidations: m.snapshotInvalidations(),
		Seq:           m.snapshotSeq(),
	}

	return json.Marshal(snap)
}

func (m *Mock) snapshotInvalidations() map[string]map[string]driver.Invalidation {
	m.invMu.Lock()
	defer m.invMu.Unlock()

	if len(m.invalidations) == 0 {
		return nil
	}

	out := make(map[string]map[string]driver.Invalidation, len(m.invalidations))

	for distID, byID := range m.invalidations {
		cp := make(map[string]driver.Invalidation, len(byID))
		for id, inv := range byID {
			cp[id] = inv
		}

		out[distID] = cp
	}

	return out
}

func (m *Mock) snapshotSeq() int64 {
	m.seqMu.Lock()
	defer m.seqMu.Unlock()

	return m.seq
}

// Restore rebuilds the mock's state under the original identities: distribution
// ids, ARNs, ETags, tags, and per-distribution invalidations survive unchanged.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap cloudfrontSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("cloudfront: parse snapshot: %w", err)
	}

	if len(snap.Distributions) != 0 {
		if err := m.dists.LoadSnapshot(snap.Distributions); err != nil {
			return fmt.Errorf("cloudfront: restore distributions: %w", err)
		}
	}

	m.restoreInvalidations(snap.Invalidations)
	m.restoreSeq(snap.Seq)

	return nil
}

func (m *Mock) restoreInvalidations(inv map[string]map[string]driver.Invalidation) {
	m.invMu.Lock()
	defer m.invMu.Unlock()

	if inv == nil {
		m.invalidations = map[string]map[string]driver.Invalidation{}

		return
	}

	m.invalidations = inv
}

func (m *Mock) restoreSeq(seq int64) {
	m.seqMu.Lock()
	defer m.seqMu.Unlock()

	m.seq = seq
}
