package glue

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/glue/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// glueSnapshot is the full serialized state of the Glue mock. Every store wraps
// its driver record in an unexported *xData holding a per-record mutex that
// json.Marshal cannot see, so the exported driver value (or, for tables and
// schemas, a small promotion struct that also carries their version history) is
// lifted out of each wrapper keyed by its resource id. The composite keys are
// opaque strings and survive unchanged. The tag/policy/encryption side-maps are
// captured under their locks; the scope locks, the per-record mutexes, and the
// wired *config.Options are intentionally not serialized.
type glueSnapshot struct {
	Databases     map[string]driver.Database              `json:"databases,omitempty"`
	Tables        map[string]tableDataSnapshot            `json:"tables,omitempty"`
	Partitions    map[string]driver.Partition             `json:"partitions,omitempty"`
	UDFs          map[string]driver.UserDefinedFunction   `json:"udfs,omitempty"`
	Connections   map[string]driver.Connection            `json:"connections,omitempty"`
	Catalogs      map[string]driver.Catalog               `json:"catalogs,omitempty"`
	Crawlers      map[string]driver.Crawler               `json:"crawlers,omitempty"`
	Classifiers   map[string]driver.Classifier            `json:"classifiers,omitempty"`
	Jobs          map[string]driver.Job                   `json:"jobs,omitempty"`
	JobRuns       map[string]driver.JobRun                `json:"jobRuns,omitempty"`
	Triggers      map[string]driver.Trigger               `json:"triggers,omitempty"`
	Workflows     map[string]driver.Workflow              `json:"workflows,omitempty"`
	WorkflowRuns  map[string]driver.WorkflowRun           `json:"workflowRuns,omitempty"`
	Blueprints    map[string]driver.Blueprint             `json:"blueprints,omitempty"`
	BlueprintRuns map[string]driver.BlueprintRun          `json:"blueprintRuns,omitempty"`
	SecConfigs    map[string]driver.SecurityConfiguration `json:"secConfigs,omitempty"`
	Registries    map[string]driver.Registry              `json:"registries,omitempty"`
	Schemas       map[string]schemaDataSnapshot           `json:"schemas,omitempty"`
	DevEndpoints  map[string]driver.DevEndpoint           `json:"devEndpoints,omitempty"`
	Tags          map[string]map[string]string            `json:"tags,omitempty"`
	Policies      map[string]string                       `json:"policies,omitempty"`
	EncSettings   map[string]map[string]any               `json:"encSettings,omitempty"`
}

// tableDataSnapshot promotes tableData, carrying the table plus its full version
// history and the monotonic next-version counter.
type tableDataSnapshot struct {
	Table    driver.Table          `json:"table"`
	Versions []driver.TableVersion `json:"versions,omitempty"`
	NextVer  int64                 `json:"nextVer,omitempty"`
}

// schemaDataSnapshot promotes schemaData, carrying the schema plus its version
// history.
type schemaDataSnapshot struct {
	Schema   driver.Schema          `json:"schema"`
	Versions []driver.SchemaVersion `json:"versions,omitempty"`
}

// snapshotWrapped promotes a store of mutex-guarded *D wrappers to a plain
// map[id]V, reading each record through get (which locks the record's mutex).
func snapshotWrapped[D, V any](s *memstore.Store[*D], get func(*D) V) map[string]V {
	if s.Len() == 0 {
		return nil
	}

	out := make(map[string]V, s.Len())
	for id, d := range s.All() {
		out[id] = get(d)
	}

	return out
}

// restoreWrapped reinstates each id→V under a freshly-built *D wrapper. build
// takes a pointer to avoid copying heavy driver values by value.
func restoreWrapped[D, V any](s *memstore.Store[*D], in map[string]V, build func(*V) *D) {
	for id, v := range in {
		rec := build(&v)
		s.Set(id, rec)
	}
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// Glue holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := m.snapshotStores()
	m.snapshotSideMaps(&snap)

	return json.Marshal(snap)
}

func (m *Mock) snapshotStores() glueSnapshot {
	return glueSnapshot{
		Databases:     snapshotWrapped(m.databases, getGlueDB),
		Tables:        snapshotWrapped(m.tables, getGlueTable),
		Partitions:    snapshotWrapped(m.partitions, getGluePart),
		UDFs:          snapshotWrapped(m.udfs, getGlueUDF),
		Connections:   snapshotWrapped(m.connections, getGlueConn),
		Catalogs:      snapshotWrapped(m.catalogs, getGlueCatalog),
		Crawlers:      snapshotWrapped(m.crawlers, getGlueCrawler),
		Classifiers:   snapshotWrapped(m.classifiers, getGlueClassifier),
		Jobs:          snapshotWrapped(m.jobs, getGlueJob),
		JobRuns:       snapshotWrapped(m.jobRuns, getGlueJobRun),
		Triggers:      snapshotWrapped(m.triggers, getGlueTrigger),
		Workflows:     snapshotWrapped(m.workflows, getGlueWorkflow),
		WorkflowRuns:  snapshotWrapped(m.workflowRuns, getGlueWorkflowRun),
		Blueprints:    snapshotWrapped(m.blueprints, getGlueBlueprint),
		BlueprintRuns: snapshotWrapped(m.blueprintRuns, getGlueBlueprintRun),
		SecConfigs:    snapshotWrapped(m.secConfigs, getGlueSecConfig),
		Registries:    snapshotWrapped(m.registries, getGlueRegistry),
		Schemas:       snapshotWrapped(m.schemas, getGlueSchema),
		DevEndpoints:  snapshotWrapped(m.devEndpoints, getGlueDevEndpoint),
	}
}

func (m *Mock) snapshotSideMaps(snap *glueSnapshot) {
	m.tagsMu.RLock()
	snap.Tags = deepCopyTags(m.tags)
	m.tagsMu.RUnlock()

	m.policyMu.RLock()
	snap.Policies = copyStrMap(m.policies)
	m.policyMu.RUnlock()

	m.encMu.RLock()
	snap.EncSettings = copyEncSettings(m.encSettings)
	m.encMu.RUnlock()
}

// Restore rebuilds the mock's state under the original identities: every catalog
// resource, ETL job, workflow, and registry schema is reinstated under its
// stored (composite) key.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap glueSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("glue: parse snapshot: %w", err)
	}

	restoreWrapped(m.databases, snap.Databases, buildGlueDB)
	restoreWrapped(m.tables, snap.Tables, buildGlueTable)
	restoreWrapped(m.partitions, snap.Partitions, buildGluePart)
	restoreWrapped(m.udfs, snap.UDFs, buildGlueUDF)
	restoreWrapped(m.connections, snap.Connections, buildGlueConn)
	restoreWrapped(m.catalogs, snap.Catalogs, buildGlueCatalog)
	restoreWrapped(m.crawlers, snap.Crawlers, buildGlueCrawler)
	restoreWrapped(m.classifiers, snap.Classifiers, buildGlueClassifier)
	restoreWrapped(m.jobs, snap.Jobs, buildGlueJob)
	restoreWrapped(m.jobRuns, snap.JobRuns, buildGlueJobRun)
	restoreWrapped(m.triggers, snap.Triggers, buildGlueTrigger)
	restoreWrapped(m.workflows, snap.Workflows, buildGlueWorkflow)
	restoreWrapped(m.workflowRuns, snap.WorkflowRuns, buildGlueWorkflowRun)
	restoreWrapped(m.blueprints, snap.Blueprints, buildGlueBlueprint)
	restoreWrapped(m.blueprintRuns, snap.BlueprintRuns, buildGlueBlueprintRun)
	restoreWrapped(m.secConfigs, snap.SecConfigs, buildGlueSecConfig)
	restoreWrapped(m.registries, snap.Registries, buildGlueRegistry)
	restoreWrapped(m.schemas, snap.Schemas, buildGlueSchema)
	restoreWrapped(m.devEndpoints, snap.DevEndpoints, buildGlueDevEndpoint)
	m.restoreSideMaps(&snap)

	return nil
}

func (m *Mock) restoreSideMaps(snap *glueSnapshot) {
	if snap.Tags != nil {
		m.tagsMu.Lock()
		m.tags = snap.Tags
		m.tagsMu.Unlock()
	}

	if snap.Policies != nil {
		m.policyMu.Lock()
		m.policies = snap.Policies
		m.policyMu.Unlock()
	}

	if snap.EncSettings != nil {
		m.encMu.Lock()
		m.encSettings = snap.EncSettings
		m.encMu.Unlock()
	}
}

func deepCopyTags(in map[string]map[string]string) map[string]map[string]string {
	if len(in) == 0 {
		return nil
	}

	out := make(map[string]map[string]string, len(in))
	for k, v := range in {
		out[k] = copyStrMap(v)
	}

	return out
}

func copyStrMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}

	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}

	return out
}

func copyEncSettings(in map[string]map[string]any) map[string]map[string]any {
	if len(in) == 0 {
		return nil
	}

	out := make(map[string]map[string]any, len(in))

	for k, v := range in {
		inner := make(map[string]any, len(v))

		for ik, iv := range v {
			inner[ik] = iv
		}

		out[k] = inner
	}

	return out
}
