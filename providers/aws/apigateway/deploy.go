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
