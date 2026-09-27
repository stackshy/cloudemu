package athena

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/athena/driver"
	gluedriver "github.com/stackshy/cloudemu/v2/services/glue/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// athenaSnapshot is the full serialized state of the Athena mock. The stores
// hold plain driver values keyed by their resource id, so each map lifts
// straight out. The client-token idempotency index is captured too so a
// restored mock still deduplicates.
//
// Databases uses the pre-Glue layout, keyed "catalog/name". With Glue wired it
// only carries databases not yet imported into Glue. Without Glue it carries
// the fallback store.
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

// Snapshot captures the mock's entire state as JSON. includeAssets is unused. Athena holds no bulk
// object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := athenaSnapshot{
		WorkGroups:      deepCopyMap(m.workGroups.All(), copyWorkGroup),
		NamedQueries:    m.namedQueries.All(),
		QueryExecutions: deepCopyMap(m.queryExecutions.All(), copyQueryExecution),
		Databases:       m.snapshotDatabases(),
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
	m.local.dbs.Clear()
	m.dataCatalogs.Clear()

	for k := range snap.WorkGroups {
		wg := copyWorkGroup(snap.WorkGroups[k])
		if isLegacySeededPrimary(&wg) {
			wg.Configuration.EnforceWorkGroupConfiguration = boolPtr(false)
		}

		m.workGroups.Set(k, wg)
	}

	for k, v := range snap.NamedQueries {
		m.namedQueries.Set(k, v)
	}

	for k := range snap.QueryExecutions {
		m.queryExecutions.Set(k, copyQueryExecution(snap.QueryExecutions[k]))
	}

	m.legacyMu.Lock()
	m.legacy = deepCopyMap(snap.Databases, copyDatabase)
	m.legacyMu.Unlock()

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

// isLegacySeededPrimary reports whether wg is primary exactly as older builds
// seeded it: enforcement on, no result configuration and every other setting
// at its seed value. Such a primary rejects every query that names its own
// output location, so Restore turns enforcement off to match the current
// seed. A primary the user changed is left alone.
func isLegacySeededPrimary(wg *driver.WorkGroup) bool {
	return wg.Name == driver.DefaultWorkGroup &&
		wg.State == driver.WorkGroupStateEnabled &&
		wg.Description == "" &&
		isLegacySeedConfig(&wg.Configuration)
}

// isLegacySeedConfig reports whether c is the old primary seed configuration.
func isLegacySeedConfig(c *driver.WorkGroupConfiguration) bool {
	return c.ResultConfiguration == nil &&
		c.BytesScannedCutoffPerQuery == nil &&
		boolIs(c.EnforceWorkGroupConfiguration, true) &&
		boolIs(c.PublishCloudWatchMetricsEnabled, true) &&
		boolIs(c.RequesterPaysEnabled, false) &&
		c.EngineVersion == resolveEngineVersion(driver.EngineVersion{})
}

func boolIs(p *bool, v bool) bool { return p != nil && *p == v }

// snapshotDatabases returns the databases Athena itself still owns: the
// pending legacy imports plus, when no Glue catalog is wired, the fallback
// store.
func (m *Mock) snapshotDatabases() map[string]driver.Database {
	out := map[string]driver.Database{}

	m.legacyMu.Lock()
	for k, v := range m.legacy {
		out[k] = copyDatabase(v)
	}
	m.legacyMu.Unlock()

	if m.catalog == Catalog(m.local) {
		for _, db := range m.local.dbs.All() {
			out[driver.DefaultDataCatalog+keySep+db.Name] = databaseFromGlue(&db)
		}
	}

	if len(out) == 0 {
		return nil
	}

	return out
}

// importLegacyDatabases moves restored pre-Glue databases into the catalog.
// A database Glue already has is kept as is. Entries whose catalog does not
// resolve stay pending so a later snapshot still carries them. importMu is
// held across the catalog calls so a concurrent caller waits for the import.
// The catalog must not call back into Athena, as the Catalog contract says.
func (m *Mock) importLegacyDatabases(ctx context.Context) {
	m.importMu.Lock()
	defer m.importMu.Unlock()

	m.legacyMu.Lock()
	pending := deepCopyMap(m.legacy, copyDatabase)
	m.legacyMu.Unlock()

	if len(pending) == 0 {
		return
	}

	done := make([]string, 0, len(pending))

	for key, db := range pending {
		catalogName, name, ok := strings.Cut(key, keySep)
		if !ok {
			continue
		}

		catalogID, err := m.glueCatalogID(catalogName)
		if err != nil {
			continue
		}

		err = m.catalog.CreateDatabase(ctx, catalogID, gluedriver.Database{
			Name:        name,
			Description: db.Description,
			Parameters:  copyStringMap(db.Parameters),
		})
		if err == nil || cerrors.GetCode(err) == cerrors.AlreadyExists {
			done = append(done, key)
		}
	}

	m.legacyMu.Lock()
	for _, key := range done {
		delete(m.legacy, key)
	}
	m.legacyMu.Unlock()
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
