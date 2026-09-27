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

	if err := validateStageName(in.StageName); err != nil {
		return nil, err
	}

	if len(in.Description) > maxDescriptionLen {
		return nil, badRequest("Description must be at most %d characters", maxDescriptionLen)
	}

	if err := validateTags(in.Tags); err != nil {
		return nil, err
	}

	ad.mu.Lock()
	defer ad.mu.Unlock()

	if _, ok := ad.stages[in.StageName]; ok {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "Stage already exists: %s", in.StageName)
	}

	if err := checkDeploymentID(ad, in.DeploymentID); err != nil {
		return nil, err
	}

	now := m.now()
	st := &driver.Stage{
		StageName: in.StageName, Description: in.Description, AutoDeploy: in.AutoDeploy,
		DeploymentID:         in.DeploymentID,
		StageVariables:       copyStrMap(in.StageVariables),
		DefaultRouteSettings: copyRouteSettings(in.DefaultRouteSettings),
		CreatedDate:          now, LastUpdatedDate: now,
		Tags: copyStrMap(in.Tags),
	}
	ad.stages[in.StageName] = st

	if in.DeploymentID != "" {
		m.pointStage(st, in.DeploymentID)
	}

	if st.AutoDeploy {
		m.deployStage(ad, st)
	}

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

// GetStages lists one page of an API's Stages, ordered by name.
func (m *Mock) GetStages(_ context.Context, apiID string, page *driver.PageInput) ([]driver.Stage, string, error) {
	return listPage(m, apiID, func(ad *apiData) map[string]*driver.Stage { return ad.stages }, copyStage,
		func(a, b driver.Stage) bool { return a.StageName < b.StageName }, page)
}

// UpdateStage applies the non-nil fields of in to a stored Stage (PATCH).
func (m *Mock) UpdateStage(_ context.Context, apiID, stageName string, in *driver.UpdateStageInput) (*driver.Stage, error) {
	ad, err := m.getAPI(apiID)
	if err != nil {
		return nil, err
	}

	ad.mu.Lock()
	defer ad.mu.Unlock()

	st, err := findStage(ad, stageName)
	if err != nil {
		return nil, err
	}

	if err := checkStageUpdate(ad, st, in); err != nil {
		return nil, err
	}

	wasAuto := st.AutoDeploy

	setString(&st.Description, in.Description)
	setBool(&st.AutoDeploy, in.AutoDeploy)

	if in.DeploymentID != nil && *in.DeploymentID != "" {
		m.pointStage(st, *in.DeploymentID)
	}

	if in.StageVariables != nil {
		st.StageVariables = copyStrMap(in.StageVariables)
	}

	if in.DefaultRouteSettings != nil {
		st.DefaultRouteSettings = copyRouteSettings(in.DefaultRouteSettings)
	}

	st.LastUpdatedDate = m.now()

	if st.AutoDeploy && !wasAuto {
		m.deployStage(ad, st)
	}

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

	st, err := findStage(ad, stageName)
	if err != nil {
		return err
	}

	if st.APIGatewayManaged {
		return badRequest(managedStageMessage)
	}

	delete(ad.stages, stageName)

	return nil
}

// managedStageMessage is the error for changing a quick-create $default stage.
const managedStageMessage = "Cannot modify or delete a stage managed by API Gateway"

// findStage returns the stored stage or a NotFound error. ad must be held.
func findStage(ad *apiData, stageName string) (*driver.Stage, error) {
	st, ok := ad.stages[stageName]
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "Invalid stage name specified %s", stageName)
	}

	return st, nil
}

// checkStageUpdate validates an UpdateStage request against the stored stage.
// ad must be held.
func checkStageUpdate(ad *apiData, st *driver.Stage, in *driver.UpdateStageInput) error {
	if st.APIGatewayManaged {
		return badRequest(managedStageMessage)
	}

	if in.Description != nil && len(*in.Description) > maxDescriptionLen {
		return badRequest("Description must be at most %d characters", maxDescriptionLen)
	}

	if in.DeploymentID == nil || *in.DeploymentID == "" {
		return nil
	}

	autoDeploy := st.AutoDeploy
	if in.AutoDeploy != nil {
		autoDeploy = *in.AutoDeploy
	}

	if autoDeploy {
		return badRequest("DeploymentId can't be updated if autoDeploy is enabled")
	}

	return checkDeploymentID(ad, *in.DeploymentID)
}

// checkDeploymentID rejects a stage deploymentId that names no deployment of
// the API. An empty id is allowed. ad must be held.
func checkDeploymentID(ad *apiData, deploymentID string) error {
	if deploymentID == "" {
		return nil
	}

	if _, ok := ad.deployments[deploymentID]; !ok {
		return badRequest("Invalid deployment identifier specified %s", deploymentID)
	}

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
	out.Tags = copyStrMap(s.Tags)
	out.StageVariables = copyStrMap(s.StageVariables)
	out.DefaultRouteSettings = copyRouteSettings(s.DefaultRouteSettings)

	return out
}
