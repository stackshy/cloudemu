package bigquery

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/bigquery/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// bqSnapshot is the full serialized state of the BigQuery mock: every dataset
// keyed by "{project}\x00{datasetId}", each with its metadata and tables, so
// identifiers (etag, ids, timestamps) restore unchanged.
type bqSnapshot struct {
	Datasets map[string]*datasetSnapshot `json:"datasets,omitempty"`
}

type datasetSnapshot struct {
	Info   driver.Dataset           `json:"info"`
	Tables map[string]*driver.Table `json:"tables,omitempty"`
}

// Snapshot captures every dataset and its tables as JSON.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := bqSnapshot{Datasets: make(map[string]*datasetSnapshot)}

	for key, entry := range m.datasets.All() {
		entry.mu.RLock()

		tables := make(map[string]*driver.Table, len(entry.tables))
		for id, tbl := range entry.tables {
			tables[id] = cloneTable(tbl)
		}

		snap.Datasets[key] = &datasetSnapshot{
			Info:   *cloneDataset(&entry.info),
			Tables: tables,
		}

		entry.mu.RUnlock()
	}

	return json.Marshal(snap)
}

// Restore rebuilds every dataset under its original key with its tables intact.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap bqSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("bigquery: parse snapshot: %w", err)
	}

	for key, ds := range snap.Datasets {
		tables := make(map[string]*driver.Table, len(ds.Tables))
		for id, tbl := range ds.Tables {
			tables[id] = cloneTable(tbl)
		}

		m.datasets.Set(key, &datasetEntry{
			info:   *cloneDataset(&ds.Info),
			tables: tables,
		})
	}

	return nil
}
