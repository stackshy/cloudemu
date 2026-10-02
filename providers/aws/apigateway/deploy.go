package apigateway

import (
	"context"
	"sort"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/apigateway/driver"
)

// CreateDeployment captures the API's current resource tree and, when a
// StageName is supplied, creates that stage or re-points it at the new
// deployment. That's the one-shot deploy the real CreateDeployment performs.
// An API with no methods, or with a method that has no integration, cannot be
// deployed.
func (m *Mock) CreateDeployment(
	_ context.Context, restAPIID string, in driver.CreateDeploymentInput,
) (*driver.Deployment, error) {
	ad, err := m.getAPI(restAPIID)
	if err != nil {
		return nil, err
	}

	ad.mu.Lock()
	defer ad.mu.Unlock()

	if err := validateDeployable(ad.resources); err != nil {
		return nil, err
	}

	if in.StageName != "" {
		if err := validateStageName(in.StageName); err != nil {
			return nil, err
		}
	}

	dep := &driver.Deployment{
		ID: genID(), RestAPIID: restAPIID, Description: in.Description, CreatedDate: m.now(),
	}
	ad.deployments[dep.ID] = dep
	ad.trees[dep.ID] = copyTree(ad.resources)

	if in.StageName != "" {
		m.deployToStage(ad, dep.ID, &in)
	}

	out := *dep

	return &out, nil
}

// deployToStage points the named stage at deploymentID, creating the stage
// with the deployment's stage description when it does not exist yet. An
// existing stage keeps its settings, and the input variables are merged in.
func (m *Mock) deployToStage(ad *apiData, deploymentID string, in *driver.CreateDeploymentInput) {
	st, ok := ad.stages[in.StageName]
	if !ok {
		st = &driver.Stage{
			StageName: in.StageName, RestAPIID: ad.api.ID,
			Description: in.StageDescription, CreatedDate: m.now(),
		}
		ad.stages[in.StageName] = st
	}

	st.DeploymentID = deploymentID

	if len(in.Variables) > 0 && st.Variables == nil {
		st.Variables = make(map[string]string, len(in.Variables))
	}

	for k, v := range in.Variables {
		st.Variables[k] = v
	}
}

// copyTree deep-copies a resource tree so a deployment's capture never shares
// a pointer with the live resources.
func copyTree(resources map[string]*driver.Resource) map[string]*driver.Resource {
	out := make(map[string]*driver.Resource, len(resources))

	for id, r := range resources {
		cp := copyResource(r)
		out[id] = &cp
	}

	return out
}

// apiSummary renders a captured tree as the path -> method -> summary map
// GetDeployment returns under embed=apisummary.
func apiSummary(tree map[string]*driver.Resource) map[string]map[string]driver.MethodSnapshot {
	out := map[string]map[string]driver.MethodSnapshot{}

	for _, r := range tree {
		if len(r.Methods) == 0 {
			continue
		}

		methods := make(map[string]driver.MethodSnapshot, len(r.Methods))
		for name, mth := range r.Methods {
			methods[name] = driver.MethodSnapshot{
				AuthorizationType: mth.AuthorizationType, APIKeyRequired: mth.APIKeyRequired,
			}
		}

		out[r.Path] = methods
	}

	return out
}

// GetDeployments lists every deployment of a REST API.
func (m *Mock) GetDeployments(_ context.Context, restAPIID string) ([]driver.Deployment, error) {
	ad, err := m.getAPI(restAPIID)
	if err != nil {
		return nil, err
	}

	ad.mu.RLock()
	defer ad.mu.RUnlock()

	out := make([]driver.Deployment, 0, len(ad.deployments))
	for _, d := range ad.deployments {
		out = append(out, *d)
	}

	// Deterministic order: oldest first, ties broken by id (the backing map
	// iterates randomly).
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedDate != out[j].CreatedDate {
			return out[i].CreatedDate < out[j].CreatedDate
		}

		return out[i].ID < out[j].ID
	})

	return out, nil
}

// GetDeployment returns a single deployment by id.
func (m *Mock) GetDeployment(_ context.Context, restAPIID, deploymentID string) (*driver.Deployment, error) {
	ad, err := m.getAPI(restAPIID)
	if err != nil {
		return nil, err
	}

	ad.mu.RLock()
	defer ad.mu.RUnlock()

	d, ok := ad.deployments[deploymentID]
	if !ok {
		return nil, cerrors.New(cerrors.NotFound, msgDeploymentNotFound)
	}

	out := *d
	out.APISummary = apiSummary(ad.trees[deploymentID])

	return &out, nil
}

// DeleteDeployment removes a deployment. It is rejected with a
// FailedPrecondition error while any stage still points at it, matching real
// API Gateway ("Active stages pointing to this deployment must be moved or
// deleted").
func (m *Mock) DeleteDeployment(_ context.Context, restAPIID, deploymentID string) error {
	ad, err := m.getAPI(restAPIID)
	if err != nil {
		return err
	}

	ad.mu.Lock()
	defer ad.mu.Unlock()

	if _, ok := ad.deployments[deploymentID]; !ok {
		return cerrors.New(cerrors.NotFound, msgDeploymentNotFound)
	}

	for _, st := range ad.stages {
		if st.DeploymentID == deploymentID {
			return cerrors.New(cerrors.FailedPrecondition,
				"Active stages pointing to this deployment must be moved or deleted")
		}
	}

	delete(ad.deployments, deploymentID)
	delete(ad.trees, deploymentID)

	return nil
}

// CreateStage points a named stage at an existing deployment.
func (m *Mock) CreateStage(_ context.Context, restAPIID string, in driver.CreateStageInput) (*driver.Stage, error) {
	if err := validateStageName(in.StageName); err != nil {
		return nil, err
	}

	if in.DeploymentID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "deploymentId is required")
	}

	ad, err := m.getAPI(restAPIID)
	if err != nil {
		return nil, err
	}

	ad.mu.Lock()
	defer ad.mu.Unlock()

	if _, ok := ad.deployments[in.DeploymentID]; !ok {
		return nil, cerrors.New(cerrors.NotFound, msgDeploymentNotFound)
	}

	if _, exists := ad.stages[in.StageName]; exists {
		return nil, cerrors.New(cerrors.AlreadyExists, msgStageExists)
	}

	if _, ok := ad.docVersions[in.DocumentationVersion]; in.DocumentationVersion != "" && !ok {
		return nil, cerrors.New(cerrors.NotFound, msgDocVersionNotFound)
	}

	st := &driver.Stage{
		StageName: in.StageName, RestAPIID: restAPIID, DeploymentID: in.DeploymentID,
		Description: in.Description, CreatedDate: m.now(), Variables: copyStrMap(in.Variables),
		DocumentationVersion: in.DocumentationVersion,
	}
	ad.stages[in.StageName] = st

	out := copyStage(st)

	return &out, nil
}

// GetStages lists every stage of a REST API.
//
//nolint:dupl // mirrors the sibling GetResources list-and-sort by design
func (m *Mock) GetStages(_ context.Context, restAPIID string) ([]driver.Stage, error) {
	ad, err := m.getAPI(restAPIID)
	if err != nil {
		return nil, err
	}

	ad.mu.RLock()
	defer ad.mu.RUnlock()

	out := make([]driver.Stage, 0, len(ad.stages))
	for _, s := range ad.stages {
		out = append(out, copyStage(s))
	}

	// Deterministic order by stage name (the backing map iterates randomly).
	sort.Slice(out, func(i, j int) bool { return out[i].StageName < out[j].StageName })

	return out, nil
}

// DeleteStage removes a named stage.
func (m *Mock) DeleteStage(_ context.Context, restAPIID, stageName string) error {
	ad, err := m.getAPI(restAPIID)
	if err != nil {
		return err
	}

	ad.mu.Lock()
	defer ad.mu.Unlock()

	if _, ok := ad.stages[stageName]; !ok {
		return cerrors.Newf(cerrors.NotFound, "Invalid stage identifier specified %s", stageName)
	}

	delete(ad.stages, stageName)

	return nil
}

// GetStage returns a named stage.
func (m *Mock) GetStage(_ context.Context, restAPIID, stageName string) (*driver.Stage, error) {
	ad, err := m.getAPI(restAPIID)
	if err != nil {
		return nil, err
	}

	ad.mu.RLock()
	defer ad.mu.RUnlock()

	st, ok := ad.stages[stageName]
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "Invalid stage identifier specified %s", stageName)
	}

	out := copyStage(st)

	return &out, nil
}

func copyStage(s *driver.Stage) driver.Stage {
	out := *s
	out.Variables = copyStrMap(s.Variables)

	return out
}
