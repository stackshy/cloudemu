package dynamodb

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

// dynamoSnapshot is the full serialized state of the DynamoDB mock: every table
// keyed by name, each with its config (including secondary indexes), items,
// TTL/stream configuration, stream records, sequence counter, tags, and PITR
// flag. Items are keyed by their internal store key so they restore under the
// same identity.
type dynamoSnapshot struct {
	Tables map[string]*tableSnapshot `json:"tables,omitempty"`
}

type tableSnapshot struct {
	Config        driver.TableConfig        `json:"config"`
	Items         map[string]map[string]any `json:"items,omitempty"`
	TTLConfig     driver.TTLConfig          `json:"ttlConfig,omitempty"`
	StreamConfig  driver.StreamConfig       `json:"streamConfig,omitempty"`
	StreamRecords []driver.StreamRecord     `json:"streamRecords,omitempty"`
	SeqCounter    int64                     `json:"seqCounter,omitempty"`
	Tags          map[string]string         `json:"tags,omitempty"`
	PITREnabled   bool                      `json:"pitrEnabled,omitempty"`
}

// Snapshot captures every table's full state as JSON. includeAssets is unused —
// DynamoDB items are the resource, so they are always captured.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	snap := dynamoSnapshot{Tables: make(map[string]*tableSnapshot, len(m.tables))}

	for name, td := range m.tables {
		snap.Tables[name] = &tableSnapshot{
			Config:        td.config,
			Items:         td.items.All(),
			TTLConfig:     td.ttlConfig,
			StreamConfig:  td.streamConfig,
			StreamRecords: td.streamRecords,
			SeqCounter:    td.seqCounter.Load(),
			Tags:          td.tags,
			PITREnabled:   td.pitrEnabled,
		}
	}

	return json.Marshal(snap)
}

// Restore rebuilds every table under its original name and item keys, retyping
// item numbers back into expr.Number so exact decimals survive the round-trip.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap dynamoSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("dynamodb: parse snapshot: %w", err)
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
			pitrEnabled:   ts.PITREnabled,
		}
		td.seqCounter.Store(ts.SeqCounter)

		for key, item := range ts.Items {
			td.items.Set(key, expr.RetypeItem(item))
		}

		m.tables[name] = td
	}

	return nil
}
