package apigateway

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/apigateway/driver"
)

// CreateDeployment snapshots the API and, when a StageName is supplied,
// creates (or re-points) that stage to the new deployment — the one-shot deploy
// the real CreateDeployment performs.
func (m *Mock) CreateDeployment(
	_ context.Context, restAPIID string, in driver.CreateDeploymentInput,
) (*driver.Deployment, error) {
	ad, err := m.getAPI(restAPIID)
	if err != nil {
		return nil, err
	}

	ad.mu.Lock()
	defer ad.mu.Unlock()

	dep := &driver.Deployment{
		ID: genID(), RestAPIID: restAPIID, Description: in.Description, CreatedDate: m.now(),
	}
	ad.deployments[dep.ID] = dep

	if in.StageName != "" {
		ad.stages[in.StageName] = &driver.Stage{
			StageName: in.StageName, RestAPIID: restAPIID,
			DeploymentID: dep.ID, CreatedDate: m.now(),
		}
	}

	out := *dep

	return &out, nil
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
		return nil, cerrors.Newf(cerrors.NotFound, "Invalid deployment identifier specified %s", deploymentID)
	}

	out := *d

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
		return cerrors.Newf(cerrors.NotFound, "Invalid deployment identifier specified %s", deploymentID)
	}

	for _, st := range ad.stages {
		if st.DeploymentID == deploymentID {
			return cerrors.New(cerrors.FailedPrecondition,
				"Active stages pointing to this deployment must be moved or deleted")
		}
	}

	delete(ad.deployments, deploymentID)

	return nil
}

// CreateStage points a named stage at an existing deployment.
func (m *Mock) CreateStage(_ context.Context, restAPIID string, in driver.CreateStageInput) (*driver.Stage, error) {
	if in.StageName == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "stageName is required")
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
		return nil, cerrors.Newf(cerrors.NotFound, "Invalid deployment identifier specified %s", in.DeploymentID)
	}

	if _, exists := ad.stages[in.StageName]; exists {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "Stage already exists: %s", in.StageName)
	}

	st := &driver.Stage{
		StageName: in.StageName, RestAPIID: restAPIID, DeploymentID: in.DeploymentID,
		Description: in.Description, CreatedDate: m.now(), Variables: copyStrMap(in.Variables),
	}
	ad.stages[in.StageName] = st

	out := copyStage(st)

	return &out, nil
}

// GetStages lists every stage of a REST API.
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
