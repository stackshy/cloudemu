package apigatewayv2

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/apigatewayv2/driver"
)

// CreateStage creates a Stage on an API. StageName is the stage's identity, so
// a duplicate name is a conflict.
func (m *Mock) CreateStage(_ context.Context, apiID string, in *driver.CreateStageInput) (*driver.Stage, error) {
	ad, err := m.getAPI(apiID)
	if err != nil {
		return nil, err
	}

	if in.StageName == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "StageName is required")
	}

	ad.mu.Lock()
	defer ad.mu.Unlock()

	if _, ok := ad.stages[in.StageName]; ok {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "Stage already exists: %s", in.StageName)
	}

	now := m.now()
	st := &driver.Stage{
		StageName: in.StageName, Description: in.Description, AutoDeploy: in.AutoDeploy,
		DeploymentID:         in.DeploymentID,
		StageVariables:       copyStrMap(in.StageVariables),
		DefaultRouteSettings: copyRouteSettings(in.DefaultRouteSettings),
		CreatedDate:          now, LastUpdatedDate: now,
	}
	ad.stages[in.StageName] = st

	out := copyStage(st)

	return &out, nil
}

// GetStage returns a single Stage.
func (m *Mock) GetStage(_ context.Context, apiID, stageName string) (*driver.Stage, error) {
	ad, err := m.getAPI(apiID)
	if err != nil {
		return nil, err
	}

	ad.mu.RLock()
	defer ad.mu.RUnlock()

	st, ok := ad.stages[stageName]
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "Invalid stage name specified %s", stageName)
	}

	out := copyStage(st)

	return &out, nil
}

// GetStages lists an API's Stages.
func (m *Mock) GetStages(_ context.Context, apiID string) ([]driver.Stage, error) {
	ad, err := m.getAPI(apiID)
	if err != nil {
		return nil, err
	}

	ad.mu.RLock()
	defer ad.mu.RUnlock()

	out := make([]driver.Stage, 0, len(ad.stages))
	for _, st := range ad.stages {
		out = append(out, copyStage(st))
	}

	return out, nil
}

// UpdateStage applies the non-nil fields of in to a stored Stage (PATCH).
func (m *Mock) UpdateStage(_ context.Context, apiID, stageName string, in *driver.UpdateStageInput) (*driver.Stage, error) {
	ad, err := m.getAPI(apiID)
	if err != nil {
		return nil, err
	}

	ad.mu.Lock()
	defer ad.mu.Unlock()

	st, ok := ad.stages[stageName]
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "Invalid stage name specified %s", stageName)
	}

	setString(&st.Description, in.Description)
	setString(&st.DeploymentID, in.DeploymentID)
	setBool(&st.AutoDeploy, in.AutoDeploy)

	if in.StageVariables != nil {
		st.StageVariables = copyStrMap(in.StageVariables)
	}

	if in.DefaultRouteSettings != nil {
		st.DefaultRouteSettings = copyRouteSettings(in.DefaultRouteSettings)
	}

	st.LastUpdatedDate = m.now()

	out := copyStage(st)

	return &out, nil
}

// DeleteStage removes a Stage.
func (m *Mock) DeleteStage(_ context.Context, apiID, stageName string) error {
	ad, err := m.getAPI(apiID)
	if err != nil {
		return err
	}

	ad.mu.Lock()
	defer ad.mu.Unlock()

	if _, ok := ad.stages[stageName]; !ok {
		return cerrors.Newf(cerrors.NotFound, "Invalid stage name specified %s", stageName)
	}

	delete(ad.stages, stageName)

	return nil
}

// copyRouteSettings returns a deep copy of a RouteSettings, or nil.
func copyRouteSettings(rs *driver.RouteSettings) *driver.RouteSettings {
	if rs == nil {
		return nil
	}

	out := *rs

	return &out
}

// copyStage returns a deep copy of a Stage.
func copyStage(s *driver.Stage) driver.Stage {
	out := *s
	out.StageVariables = copyStrMap(s.StageVariables)
	out.DefaultRouteSettings = copyRouteSettings(s.DefaultRouteSettings)

	return out
}
