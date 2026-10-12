package sagemaker

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// snapshotStore is the persistence surface every memstore.Store exposes.
type snapshotStore interface {
	Snapshot() ([]byte, error)
	LoadSnapshot([]byte) error
}

// namedStore pairs a store with its key in the serialized snapshot.
type namedStore struct {
	key   string
	store snapshotStore
}

// stores is the single list of every store the SageMaker mock persists. A new
// resource family adds its store HERE and nowhere else: Snapshot and Restore
// both iterate this list, and TestSnapshot_ReflectionAllStores fails when a
// store field of Mock is missing from it. Keys are stable on-disk names, so an
// older snapshot (which lacks the keys of newer stores) restores unchanged.
//
// The pkgMu mutex, the wired *config.Options, the monitoring backend and the
// event publisher are intentionally not serialized.
func (m *Mock) stores() []namedStore {
	return []namedStore{
		{"trainingJobs", m.trainingJobs},
		{"processingJobs", m.processingJobs},
		{"transformJobs", m.transformJobs},
		{"tuningJobs", m.tuningJobs},
		{"autoMlJobs", m.autoMLJobs},
		{"labelingJobs", m.labelingJobs},
		{"compilationJobs", m.compilationJobs},
		{"models", m.models},
		{"endpointConfigs", m.endpointConfigs},
		{"endpoints", m.endpoints},
		{"inferenceComponents", m.inferenceComponents},
		{"packageGroups", m.packageGroups},
		{"packages", m.packages},
		{"packagePolicies", m.packagePolicies},
		{"domains", m.domains},
		{"userProfiles", m.userProfiles},
		{"spaces", m.spaces},
		{"apps", m.apps},
		{"notebooks", m.notebooks},
		{"notebookLcs", m.notebookLCs},
		{"codeRepos", m.codeRepos},
		{"clusters", m.clusters},
		{"featureGroups", m.featureGroups},
		{"featureRecords", m.featureRecords},
		{"pipelines", m.pipelines},
		{"executions", m.executions},
		{"experiments", m.experiments},
		{"trials", m.trials},
		{"projects", m.projects},
		{"monSchedules", m.monSchedules},
		{"monDefs", m.monDefs},
		{"monExecs", m.monExecs},
		{"monAlerts", m.monAlerts},
		{"trialComponents", m.trialComponents},
		{"lineageEntities", m.lineageEntities},
		{"associations", m.associations},
		{"lineageGroup", m.lineageGroup},
		{"images", m.images},
		{"imageVersions", m.imageVersions},
		{"appImageConfigs", m.appImageConfigs},
		{"workforces", m.workforces},
		{"workteams", m.workteams},
		{"tags", m.tags},
	}
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused.
// SageMaker holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := make(map[string]json.RawMessage)

	for _, s := range m.stores() {
		b, err := s.store.Snapshot()
		if err != nil {
			return nil, fmt.Errorf("sagemaker: snapshot store %s: %w", s.key, err)
		}

		snap[s.key] = b
	}

	return json.Marshal(snap)
}

// Restore rebuilds the mock's state under the original identities: every job,
// model, endpoint, and registry resource is reinstated under its stored id.
// Stores absent from the snapshot (written by an older version) stay empty.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap map[string]json.RawMessage
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("sagemaker: parse snapshot: %w", err)
	}

	for _, s := range m.stores() {
		raw := snap[s.key]
		if len(raw) == 0 {
			continue
		}

		if err := s.store.LoadSnapshot(raw); err != nil {
			return fmt.Errorf("sagemaker: restore store %s: %w", s.key, err)
		}
	}

	m.backfillEndpointSummaries()

	return nil
}

// backfillEndpointSummaries gives endpoints restored from an older snapshot
// (which stored only the config's variants) the runtime variant view Describe
// reports, so they answer exactly as a freshly created endpoint does.
func (m *Mock) backfillEndpointSummaries() {
	m.epMu.Lock()
	defer m.epMu.Unlock()

	for _, ep := range m.endpoints.All() {
		if len(ep.VariantSummaries) > 0 || len(ep.Variants) == 0 {
			continue
		}

		updated := cloneEndpoint(ep)
		updated.VariantSummaries = m.variantSummaries(ep.Variants, ep.CreationTime)
		m.endpoints.Set(ep.EndpointName, updated)
	}
}
