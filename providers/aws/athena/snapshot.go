package athena

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/athena/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// athenaSnapshot is the full serialized state of the Athena mock. The stores
// hold plain driver values keyed by their resource id (databases by the
// composite catalog/name key), so each map lifts straight out. The client-token
// idempotency index is captured too so a restored mock still deduplicates.
type athenaSnapshot struct {
	WorkGroups      map[string]driver.WorkGroup      `json:"workGroups,omitempty"`
	NamedQueries    map[string]driver.NamedQuery     `json:"namedQueries,omitempty"`
	QueryExecutions map[string]driver.QueryExecution `json:"queryExecutions,omitempty"`
	Databases       map[string]driver.Database       `json:"databases,omitempty"`
	DataCatalogs    map[string]driver.DataCatalog    `json:"dataCatalogs,omitempty"`
	Tokens          map[string]string                `json:"tokens,omitempty"`
	Tags            map[string]map[string]string     `json:"tags,omitempty"`
	Seq             int64                            `json:"seq,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// Athena holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := athenaSnapshot{
		WorkGroups:      deepCopyMap(m.workGroups.All(), copyWorkGroup),
		NamedQueries:    m.namedQueries.All(),
		QueryExecutions: deepCopyMap(m.queryExecutions.All(), copyQueryExecution),
		Databases:       deepCopyMap(m.databases.All(), copyDatabase),
		DataCatalogs:    deepCopyMap(m.dataCatalogs.All(), copyDataCatalog),
		Seq:             m.seq.Load(),
	}

	m.tokenMu.Lock()
	snap.Tokens = copyStringMap(m.tokens)
	m.tokenMu.Unlock()

	m.tagsMu.RLock()
	snap.Tags = deepCopyTags(m.tags)
	m.tagsMu.RUnlock()

	return json.Marshal(snap)
}

// Restore rebuilds the mock's state under the original identities.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap athenaSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("athena: parse snapshot: %w", err)
	}

	m.workGroups.Clear()
	m.namedQueries.Clear()
	m.queryExecutions.Clear()
	m.databases.Clear()
	m.dataCatalogs.Clear()

	for k := range snap.WorkGroups {
		m.workGroups.Set(k, copyWorkGroup(snap.WorkGroups[k]))
	}

	for k, v := range snap.NamedQueries {
		m.namedQueries.Set(k, v)
	}

	for k := range snap.QueryExecutions {
		m.queryExecutions.Set(k, copyQueryExecution(snap.QueryExecutions[k]))
	}

	for k, v := range snap.Databases {
		m.databases.Set(k, copyDatabase(v))
	}

	for k, v := range snap.DataCatalogs {
		m.dataCatalogs.Set(k, copyDataCatalog(v))
	}

	m.seq.Store(snap.Seq)

	m.tokenMu.Lock()
	m.tokens = copyStringMap(snap.Tokens)

	if m.tokens == nil {
		m.tokens = map[string]string{}
	}
	m.tokenMu.Unlock()

	m.tagsMu.Lock()
	m.tags = deepCopyTags(snap.Tags)

	if m.tags == nil {
		m.tags = map[string]map[string]string{}
	}
	m.tagsMu.Unlock()

	return nil
}

// deepCopyTags copies a nested ARN->tags map.
func deepCopyTags(in map[string]map[string]string) map[string]map[string]string {
	if len(in) == 0 {
		return nil
	}

	out := make(map[string]map[string]string, len(in))
	for k, v := range in {
		out[k] = copyStringMap(v)
	}

	return out
}

// deepCopyMap returns a copy of in with each value passed through cp.
func deepCopyMap[V any](in map[string]V, cp func(V) V) map[string]V {
	if len(in) == 0 {
		return nil
	}

	out := make(map[string]V, len(in))
	for k, v := range in {
		out[k] = cp(v)
	}

	return out
}
