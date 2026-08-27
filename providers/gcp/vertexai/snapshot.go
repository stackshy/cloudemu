package vertexai

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// vertexaiSnapshot is the full serialized state of the Vertex AI mock. Every
// store's value type is fully exported (driver.*), so each round-trips through
// the generic memstore helper keyed by its resource name (self-link). The wired
// *config.Options and monitoring backend are intentionally not serialized.
type vertexaiSnapshot struct {
	Datasets       json.RawMessage `json:"datasets,omitempty"`
	Models         json.RawMessage `json:"models,omitempty"`
	Evaluations    json.RawMessage `json:"evaluations,omitempty"`
	Endpoints      json.RawMessage `json:"endpoints,omitempty"`
	CustomJobs     json.RawMessage `json:"customJobs,omitempty"`
	BatchJobs      json.RawMessage `json:"batchJobs,omitempty"`
	HPOJobs        json.RawMessage `json:"hpoJobs,omitempty"`
	TrainPipes     json.RawMessage `json:"trainPipes,omitempty"`
	PipelineJobs   json.RawMessage `json:"pipelineJobs,omitempty"`
	TuningJobs     json.RawMessage `json:"tuningJobs,omitempty"`
	CachedContent  json.RawMessage `json:"cachedContent,omitempty"`
	Featurestores  json.RawMessage `json:"featurestores,omitempty"`
	EntityTypes    json.RawMessage `json:"entityTypes,omitempty"`
	EntityRecords  json.RawMessage `json:"entityRecords,omitempty"`
	FeatureGroups  json.RawMessage `json:"featureGroups,omitempty"`
	Features       json.RawMessage `json:"features,omitempty"`
	OnlineStores   json.RawMessage `json:"onlineStores,omitempty"`
	FeatureViews   json.RawMessage `json:"featureViews,omitempty"`
	Indexes        json.RawMessage `json:"indexes,omitempty"`
	IndexEndpoints json.RawMessage `json:"indexEndpoints,omitempty"`
	MetadataStores json.RawMessage `json:"metadataStores,omitempty"`
	Tensorboards   json.RawMessage `json:"tensorboards,omitempty"`
	Schedules      json.RawMessage `json:"schedules,omitempty"`
	NBTemplates    json.RawMessage `json:"nbTemplates,omitempty"`
	NBRuntimes     json.RawMessage `json:"nbRuntimes,omitempty"`
	Operations     json.RawMessage `json:"operations,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// Vertex AI holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	var snap vertexaiSnapshot
	if err := m.snapshotStores(&snap); err != nil {
		return nil, err
	}

	return json.Marshal(snap)
}

func (m *Mock) snapshotStores(snap *vertexaiSnapshot) error {
	dumps := []struct {
		dst *json.RawMessage
		fn  func() ([]byte, error)
	}{
		{&snap.Datasets, m.datasets.Snapshot},
		{&snap.Models, m.models.Snapshot},
		{&snap.Evaluations, m.evaluations.Snapshot},
		{&snap.Endpoints, m.endpoints.Snapshot},
		{&snap.CustomJobs, m.customJobs.Snapshot},
		{&snap.BatchJobs, m.batchJobs.Snapshot},
		{&snap.HPOJobs, m.hpoJobs.Snapshot},
		{&snap.TrainPipes, m.trainPipes.Snapshot},
		{&snap.PipelineJobs, m.pipelineJobs.Snapshot},
		{&snap.TuningJobs, m.tuningJobs.Snapshot},
		{&snap.CachedContent, m.cachedContent.Snapshot},
		{&snap.Featurestores, m.featurestores.Snapshot},
		{&snap.EntityTypes, m.entityTypes.Snapshot},
		{&snap.EntityRecords, m.entityRecords.Snapshot},
		{&snap.FeatureGroups, m.featureGroups.Snapshot},
		{&snap.Features, m.features.Snapshot},
		{&snap.OnlineStores, m.onlineStores.Snapshot},
		{&snap.FeatureViews, m.featureViews.Snapshot},
		{&snap.Indexes, m.indexes.Snapshot},
		{&snap.IndexEndpoints, m.indexEndpoints.Snapshot},
		{&snap.MetadataStores, m.metadataStores.Snapshot},
		{&snap.Tensorboards, m.tensorboards.Snapshot},
		{&snap.Schedules, m.schedules.Snapshot},
		{&snap.NBTemplates, m.nbTemplates.Snapshot},
		{&snap.NBRuntimes, m.nbRuntimes.Snapshot},
		{&snap.Operations, m.operations.Snapshot},
	}

	for _, d := range dumps {
		b, err := d.fn()
		if err != nil {
			return fmt.Errorf("vertexai: snapshot store: %w", err)
		}

		*d.dst = b
	}

	return nil
}

// Restore rebuilds the mock's state under the original identities: every
// resource name (self-link) is preserved so cross-references still resolve.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap vertexaiSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("vertexai: parse snapshot: %w", err)
	}

	return m.restoreStores(&snap)
}

func (m *Mock) restoreStores(snap *vertexaiSnapshot) error {
	loads := []struct {
		src json.RawMessage
		fn  func([]byte) error
	}{
		{snap.Datasets, m.datasets.LoadSnapshot},
		{snap.Models, m.models.LoadSnapshot},
		{snap.Evaluations, m.evaluations.LoadSnapshot},
		{snap.Endpoints, m.endpoints.LoadSnapshot},
		{snap.CustomJobs, m.customJobs.LoadSnapshot},
		{snap.BatchJobs, m.batchJobs.LoadSnapshot},
		{snap.HPOJobs, m.hpoJobs.LoadSnapshot},
		{snap.TrainPipes, m.trainPipes.LoadSnapshot},
		{snap.PipelineJobs, m.pipelineJobs.LoadSnapshot},
		{snap.TuningJobs, m.tuningJobs.LoadSnapshot},
		{snap.CachedContent, m.cachedContent.LoadSnapshot},
		{snap.Featurestores, m.featurestores.LoadSnapshot},
		{snap.EntityTypes, m.entityTypes.LoadSnapshot},
		{snap.EntityRecords, m.entityRecords.LoadSnapshot},
		{snap.FeatureGroups, m.featureGroups.LoadSnapshot},
		{snap.Features, m.features.LoadSnapshot},
		{snap.OnlineStores, m.onlineStores.LoadSnapshot},
		{snap.FeatureViews, m.featureViews.LoadSnapshot},
		{snap.Indexes, m.indexes.LoadSnapshot},
		{snap.IndexEndpoints, m.indexEndpoints.LoadSnapshot},
		{snap.MetadataStores, m.metadataStores.LoadSnapshot},
		{snap.Tensorboards, m.tensorboards.LoadSnapshot},
		{snap.Schedules, m.schedules.LoadSnapshot},
		{snap.NBTemplates, m.nbTemplates.LoadSnapshot},
		{snap.NBRuntimes, m.nbRuntimes.LoadSnapshot},
		{snap.Operations, m.operations.LoadSnapshot},
	}

	for _, l := range loads {
		if len(l.src) == 0 {
			continue
		}

		if err := l.fn(l.src); err != nil {
			return fmt.Errorf("vertexai: restore store: %w", err)
		}
	}

	return nil
}
