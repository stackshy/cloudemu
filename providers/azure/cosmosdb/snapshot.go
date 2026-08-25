package cosmosdb

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"

	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/database/driver"
	"github.com/stackshy/cloudemu/v2/services/database/driver/expr"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// cosmosSnapshot is the full serialized state of the Cosmos DB mock: every
// container keyed by name (with its config, items, TTL/change-feed
// configuration, feed records, sequence counter, and tags), plus the
// Cosmos-account cost attributes seeded for the TableAttributes discovery
// capability. Items are keyed by their internal store key so they restore under
// the same identity.
type cosmosSnapshot struct {
	Tables       map[string]*tableSnapshot           `json:"tables,omitempty"`
	AccountAttrs map[string]driver.AccountAttributes `json:"accountAttrs,omitempty"`
}

type tableSnapshot struct {
	Config        driver.TableConfig        `json:"config"`
	Items         map[string]map[string]any `json:"items,omitempty"`
	TTLConfig     driver.TTLConfig          `json:"ttlConfig,omitempty"`
	StreamConfig  driver.StreamConfig       `json:"streamConfig,omitempty"`
	StreamRecords []driver.StreamRecord     `json:"streamRecords,omitempty"`
	SeqCounter    int64                     `json:"seqCounter,omitempty"`
	Tags          map[string]string         `json:"tags,omitempty"`
}

// Snapshot captures every container's full state as JSON. includeAssets is
// unused — Cosmos items are the resource, so they are always captured.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	snap := cosmosSnapshot{Tables: make(map[string]*tableSnapshot, len(m.tables))}

	for name, td := range m.tables {
		snap.Tables[name] = &tableSnapshot{
			Config:        td.config,
			Items:         td.items.All(),
			TTLConfig:     td.ttlConfig,
			StreamConfig:  td.streamConfig,
			StreamRecords: td.streamRecords,
			SeqCounter:    td.seqCounter.Load(),
			Tags:          td.tags,
		}
	}

	if len(m.accountAttrs) > 0 {
		snap.AccountAttrs = make(map[string]driver.AccountAttributes, len(m.accountAttrs))
		maps.Copy(snap.AccountAttrs, m.accountAttrs)
	}

	return json.Marshal(snap)
}

// Restore rebuilds every container under its original name and item keys,
// retyping item numbers back into expr.Number so exact decimals survive the
// round-trip.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap cosmosSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("cosmosdb: parse snapshot: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for name, ts := range snap.Tables {
		td := &tableData{
			config:        ts.Config,
			items:         memstore.New[map[string]any](),
			ttlConfig:     ts.TTLConfig,
			streamConfig:  ts.StreamConfig,
			streamRecords: ts.StreamRecords,
			tags:          ts.Tags,
		}
		td.seqCounter.Store(ts.SeqCounter)

		for key, item := range ts.Items {
			td.items.Set(key, expr.RetypeItem(item))
		}

		m.tables[name] = td
	}

	if len(snap.AccountAttrs) > 0 {
		if m.accountAttrs == nil {
			m.accountAttrs = make(map[string]driver.AccountAttributes, len(snap.AccountAttrs))
		}

		maps.Copy(m.accountAttrs, snap.AccountAttrs)
	}

	return nil
}
