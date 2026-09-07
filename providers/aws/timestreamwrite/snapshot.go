package timestreamwrite

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/timestreamwrite/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// timestreamSnapshot is the full serialized state of the Timestream Write mock.
// The stores hold exported driver types, so they serialize directly, keyed by
// database name and table "databaseName/tableName". The wired opts are not
// serialized.
type timestreamSnapshot struct {
	Databases map[string]driver.Database `json:"databases,omitempty"`
	Tables    map[string]driver.Table    `json:"tables,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// Timestream Write is control-plane only and holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := timestreamSnapshot{}

	if m.databases.Len() > 0 {
		snap.Databases = m.databases.All()
	}

	if m.tables.Len() > 0 {
		snap.Tables = m.tables.All()
	}

	b, err := json.Marshal(snap)
	if err != nil {
		return nil, fmt.Errorf("timestreamwrite: snapshot: %w", err)
	}

	return b, nil
}

// Restore rebuilds the mock's state under the original identities: every
// database name (and the arn derived from it) and every table key is preserved,
// so a restore is transparent to clients.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap timestreamSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("timestreamwrite: parse snapshot: %w", err)
	}

	for name := range snap.Databases {
		m.databases.Set(name, snap.Databases[name])
	}

	for key := range snap.Tables {
		m.tables.Set(key, snap.Tables[key])
	}

	return nil
}
