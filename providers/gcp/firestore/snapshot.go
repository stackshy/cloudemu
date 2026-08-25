package firestore

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/database/driver"
	"github.com/stackshy/cloudemu/v2/services/database/driver/expr"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// firestoreSnapshot is the full serialized state of the Firestore mock: every
// collection keyed by name, each with its config (including composite indexes),
// documents, TTL/listener configuration, snapshot records, sequence counter,
// and labels. Documents are keyed by their internal store key so they restore
// under the same identity.
type firestoreSnapshot struct {
	Collections map[string]*collectionSnapshot `json:"collections,omitempty"`
}

type collectionSnapshot struct {
	Config        driver.TableConfig        `json:"config"`
	Items         map[string]map[string]any `json:"items,omitempty"`
	TTLConfig     driver.TTLConfig          `json:"ttlConfig,omitempty"`
	StreamConfig  driver.StreamConfig       `json:"streamConfig,omitempty"`
	StreamRecords []driver.StreamRecord     `json:"streamRecords,omitempty"`
	SeqCounter    int64                     `json:"seqCounter,omitempty"`
	Labels        map[string]string         `json:"labels,omitempty"`
}

// Snapshot captures every collection's full state as JSON. includeAssets is
// unused — Firestore documents are the resource, so they are always captured.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	snap := firestoreSnapshot{Collections: make(map[string]*collectionSnapshot, len(m.collections))}

	for name, cd := range m.collections {
		snap.Collections[name] = &collectionSnapshot{
			Config:        cd.config,
			Items:         cd.items.All(),
			TTLConfig:     cd.ttlConfig,
			StreamConfig:  cd.streamConfig,
			StreamRecords: cd.streamRecords,
			SeqCounter:    cd.seqCounter.Load(),
			Labels:        cd.labels,
		}
	}

	return json.Marshal(snap)
}

// Restore rebuilds every collection under its original name and document keys,
// retyping document numbers back into expr.Number so exact decimals survive the
// round-trip.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap firestoreSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("firestore: parse snapshot: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for name, cs := range snap.Collections {
		cd := &collectionData{
			config:        cs.Config,
			items:         memstore.New[map[string]any](),
			ttlConfig:     cs.TTLConfig,
			streamConfig:  cs.StreamConfig,
			streamRecords: cs.StreamRecords,
			labels:        cs.Labels,
		}
		cd.seqCounter.Store(cs.SeqCounter)

		for key, item := range cs.Items {
			cd.items.Set(key, expr.RetypeItem(item))
		}

		m.collections[name] = cd
	}

	return nil
}
