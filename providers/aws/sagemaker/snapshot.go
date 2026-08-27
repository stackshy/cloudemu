package sagemaker

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// sagemakerSnapshot is the full serialized state of the SageMaker mock. Every
// store's value type is fully exported (driver.* DTOs), so each round-trips
// through the generic memstore helper keyed by its resource id. The composite
// user-profile/space/app/feature-record keys are opaque strings and survive
// unchanged. The pkgMu mutex, the wired *config.Options, and the monitoring
// backend are intentionally not serialized.
type sagemakerSnapshot struct {
	TrainingJobs        json.RawMessage `json:"trainingJobs,omitempty"`
	ProcessingJobs      json.RawMessage `json:"processingJobs,omitempty"`
	TransformJobs       json.RawMessage `json:"transformJobs,omitempty"`
	TuningJobs          json.RawMessage `json:"tuningJobs,omitempty"`
	AutoMLJobs          json.RawMessage `json:"autoMlJobs,omitempty"`
	LabelingJobs        json.RawMessage `json:"labelingJobs,omitempty"`
	CompilationJobs     json.RawMessage `json:"compilationJobs,omitempty"`
	Models              json.RawMessage `json:"models,omitempty"`
	EndpointConfigs     json.RawMessage `json:"endpointConfigs,omitempty"`
	Endpoints           json.RawMessage `json:"endpoints,omitempty"`
	InferenceComponents json.RawMessage `json:"inferenceComponents,omitempty"`
	PackageGroups       json.RawMessage `json:"packageGroups,omitempty"`
	Packages            json.RawMessage `json:"packages,omitempty"`
	Domains             json.RawMessage `json:"domains,omitempty"`
	UserProfiles        json.RawMessage `json:"userProfiles,omitempty"`
	Spaces              json.RawMessage `json:"spaces,omitempty"`
	Apps                json.RawMessage `json:"apps,omitempty"`
	Notebooks           json.RawMessage `json:"notebooks,omitempty"`
	NotebookLCs         json.RawMessage `json:"notebookLcs,omitempty"`
	CodeRepos           json.RawMessage `json:"codeRepos,omitempty"`
	Clusters            json.RawMessage `json:"clusters,omitempty"`
	FeatureGroups       json.RawMessage `json:"featureGroups,omitempty"`
	FeatureRecords      json.RawMessage `json:"featureRecords,omitempty"`
	Pipelines           json.RawMessage `json:"pipelines,omitempty"`
	Executions          json.RawMessage `json:"executions,omitempty"`
	Experiments         json.RawMessage `json:"experiments,omitempty"`
	Trials              json.RawMessage `json:"trials,omitempty"`
	Tags                json.RawMessage `json:"tags,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// SageMaker holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	var snap sagemakerSnapshot
	if err := m.snapshotStores(&snap); err != nil {
		return nil, err
	}

	return json.Marshal(snap)
}

func (m *Mock) snapshotStores(snap *sagemakerSnapshot) error {
	dumps := []struct {
		dst *json.RawMessage
		fn  func() ([]byte, error)
	}{
		{&snap.TrainingJobs, m.trainingJobs.Snapshot},
		{&snap.ProcessingJobs, m.processingJobs.Snapshot},
		{&snap.TransformJobs, m.transformJobs.Snapshot},
		{&snap.TuningJobs, m.tuningJobs.Snapshot},
		{&snap.AutoMLJobs, m.autoMLJobs.Snapshot},
		{&snap.LabelingJobs, m.labelingJobs.Snapshot},
		{&snap.CompilationJobs, m.compilationJobs.Snapshot},
		{&snap.Models, m.models.Snapshot},
		{&snap.EndpointConfigs, m.endpointConfigs.Snapshot},
		{&snap.Endpoints, m.endpoints.Snapshot},
		{&snap.InferenceComponents, m.inferenceComponents.Snapshot},
		{&snap.PackageGroups, m.packageGroups.Snapshot},
		{&snap.Packages, m.packages.Snapshot},
		{&snap.Domains, m.domains.Snapshot},
		{&snap.UserProfiles, m.userProfiles.Snapshot},
		{&snap.Spaces, m.spaces.Snapshot},
		{&snap.Apps, m.apps.Snapshot},
		{&snap.Notebooks, m.notebooks.Snapshot},
		{&snap.NotebookLCs, m.notebookLCs.Snapshot},
		{&snap.CodeRepos, m.codeRepos.Snapshot},
		{&snap.Clusters, m.clusters.Snapshot},
		{&snap.FeatureGroups, m.featureGroups.Snapshot},
		{&snap.FeatureRecords, m.featureRecords.Snapshot},
		{&snap.Pipelines, m.pipelines.Snapshot},
		{&snap.Executions, m.executions.Snapshot},
		{&snap.Experiments, m.experiments.Snapshot},
		{&snap.Trials, m.trials.Snapshot},
		{&snap.Tags, m.tags.Snapshot},
	}

	for _, d := range dumps {
		b, err := d.fn()
		if err != nil {
			return fmt.Errorf("sagemaker: snapshot store: %w", err)
		}

		*d.dst = b
	}

	return nil
}

// Restore rebuilds the mock's state under the original identities: every job,
// model, endpoint, and registry resource is reinstated under its stored id.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap sagemakerSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("sagemaker: parse snapshot: %w", err)
	}

	return m.restoreStores(&snap)
}

func (m *Mock) restoreStores(snap *sagemakerSnapshot) error {
	loads := []struct {
		src json.RawMessage
		fn  func([]byte) error
	}{
		{snap.TrainingJobs, m.trainingJobs.LoadSnapshot},
		{snap.ProcessingJobs, m.processingJobs.LoadSnapshot},
		{snap.TransformJobs, m.transformJobs.LoadSnapshot},
		{snap.TuningJobs, m.tuningJobs.LoadSnapshot},
		{snap.AutoMLJobs, m.autoMLJobs.LoadSnapshot},
		{snap.LabelingJobs, m.labelingJobs.LoadSnapshot},
		{snap.CompilationJobs, m.compilationJobs.LoadSnapshot},
		{snap.Models, m.models.LoadSnapshot},
		{snap.EndpointConfigs, m.endpointConfigs.LoadSnapshot},
		{snap.Endpoints, m.endpoints.LoadSnapshot},
		{snap.InferenceComponents, m.inferenceComponents.LoadSnapshot},
		{snap.PackageGroups, m.packageGroups.LoadSnapshot},
		{snap.Packages, m.packages.LoadSnapshot},
		{snap.Domains, m.domains.LoadSnapshot},
		{snap.UserProfiles, m.userProfiles.LoadSnapshot},
		{snap.Spaces, m.spaces.LoadSnapshot},
		{snap.Apps, m.apps.LoadSnapshot},
		{snap.Notebooks, m.notebooks.LoadSnapshot},
		{snap.NotebookLCs, m.notebookLCs.LoadSnapshot},
		{snap.CodeRepos, m.codeRepos.LoadSnapshot},
		{snap.Clusters, m.clusters.LoadSnapshot},
		{snap.FeatureGroups, m.featureGroups.LoadSnapshot},
		{snap.FeatureRecords, m.featureRecords.LoadSnapshot},
		{snap.Pipelines, m.pipelines.LoadSnapshot},
		{snap.Executions, m.executions.LoadSnapshot},
		{snap.Experiments, m.experiments.LoadSnapshot},
		{snap.Trials, m.trials.LoadSnapshot},
		{snap.Tags, m.tags.LoadSnapshot},
	}

	for _, l := range loads {
		if len(l.src) == 0 {
			continue
		}

		if err := l.fn(l.src); err != nil {
			return fmt.Errorf("sagemaker: restore store: %w", err)
		}
	}

	return nil
}
