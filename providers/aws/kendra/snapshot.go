package kendra

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/kendra/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// kendraSnapshot is the full serialized state of the Kendra mock. The stores
// hold exported driver types, so they serialize directly, keyed by index id and
// data source "indexId/dataSourceId". The wired opts are not serialized.
type kendraSnapshot struct {
	Indexes     map[string]driver.Index      `json:"indexes,omitempty"`
	DataSources map[string]driver.DataSource `json:"dataSources,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// Kendra is control-plane only and holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := kendraSnapshot{}

	if m.indexes.Len() > 0 {
		snap.Indexes = m.indexes.All()
	}

	if m.dataSources.Len() > 0 {
		snap.DataSources = m.dataSources.All()
	}

	b, err := json.Marshal(snap)
	if err != nil {
		return nil, fmt.Errorf("kendra: snapshot: %w", err)
	}

	return b, nil
}

// Restore rebuilds the mock's state under the original identities: every index
// id (and the arn Terraform derives from it) and every data source id is
// preserved, so a restore is transparent to clients.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap kendraSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("kendra: parse snapshot: %w", err)
	}

	for id := range snap.Indexes {
		m.indexes.Set(id, snap.Indexes[id])
	}

	for key := range snap.DataSources {
		m.dataSources.Set(key, snap.DataSources[key])
	}

	return nil
}
