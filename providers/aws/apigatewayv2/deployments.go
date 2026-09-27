package apigatewayv2

import (
	"context"
	"fmt"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/apigatewayv2/driver"
)

// autoDeployDescription is the description API Gateway gives the deployments
// it creates for an autoDeploy stage.
const autoDeployDescription = "Automatic deployment triggered by changes to the Api configuration"

// noRoutesMessage is the error for deploying an API that has no routes.
const noRoutesMessage = "Unable to deploy API because no routes exist in this API"

// deploymentRecord is a deployment plus the routes and integrations it froze.
// Its fields are exported so a snapshot can serialize it directly.
type deploymentRecord struct {
	Deployment   driver.Deployment              `json:"deployment"`
	Routes       map[string]*driver.Route       `json:"routes,omitempty"`
	Integrations map[string]*driver.Integration `json:"integrations,omitempty"`
}

// newDeployment freezes the API's current routes and integrations into a new
// deployment and stores it. ad must be held for writing.
func (m *Mock) newDeployment(ad *apiData, description string, auto bool) *deploymentRecord {
	rec := &deploymentRecord{
		Deployment: driver.Deployment{
			DeploymentID: genID(), Description: description, CreatedDate: m.now(),
			DeploymentStatus: driver.DeploymentStatusDeployed, AutoDeployed: auto,
		},
		Routes:       make(map[string]*driver.Route, len(ad.routes)),
		Integrations: make(map[string]*driver.Integration, len(ad.integrations)),
	}

	for id, rt := range ad.routes {
		cp := copyRoute(rt)
		rec.Routes[id] = &cp
	}

	for id, ig := range ad.integrations {
		cp := copyIntegration(ig)
		rec.Integrations[id] = &cp
	}

	ad.deployments[rec.Deployment.DeploymentID] = rec

	return rec
}

// pointStage moves a stage onto a deployment. ad must be held for writing.
func (m *Mock) pointStage(st *driver.Stage, deploymentID string) {
	st.DeploymentID = deploymentID
	st.LastDeploymentStatusMessage = fmt.Sprintf("Successfully deployed stage with deployment ID '%s'", deploymentID)
	st.LastUpdatedDate = m.now()
}

// autoDeploy gives every autoDeploy stage a fresh deployment after the API's
// routes or integrations change. An API with no routes cannot be deployed, so
// its stages keep what they have. ad must be held for writing.
func (m *Mock) autoDeploy(ad *apiData) {
	for _, st := range ad.stages {
		if st.AutoDeploy {
			m.deployStage(ad, st)
		}
	}
}

// deployStage creates an automatic deployment for one stage. ad must be held
// for writing.
func (m *Mock) deployStage(ad *apiData, st *driver.Stage) {
	if len(ad.routes) == 0 {
		return
	}

	rec := m.newDeployment(ad, autoDeployDescription, true)
	m.pointStage(st, rec.Deployment.DeploymentID)
}

// CreateDeployment snapshots the API's routes and integrations. When StageName
// is set the stage moves onto the new deployment.
func (m *Mock) CreateDeployment(
	_ context.Context, apiID string, in *driver.CreateDeploymentInput,
) (*driver.Deployment, error) {
	ad, err := m.getAPI(apiID)
	if err != nil {
		return nil, err
	}

	if len(in.Description) > maxDescriptionLen {
		return nil, badRequest("Description must be at most %d characters", maxDescriptionLen)
	}

	ad.mu.Lock()
	defer ad.mu.Unlock()

	var st *driver.Stage

	if in.StageName != "" {
		var ok bool
		if st, ok = ad.stages[in.StageName]; !ok {
			return nil, cerrors.New(cerrors.NotFound, "Invalid stage identifier specified")
		}
	}

	if len(ad.routes) == 0 {
		return nil, badRequest(noRoutesMessage)
	}

	rec := m.newDeployment(ad, in.Description, false)

	if st != nil {
		m.pointStage(st, rec.Deployment.DeploymentID)
	}

	out := rec.Deployment

	return &out, nil
}

// GetDeployment returns a single Deployment.
func (m *Mock) GetDeployment(_ context.Context, apiID, deploymentID string) (*driver.Deployment, error) {
	ad, err := m.getAPI(apiID)
	if err != nil {
		return nil, err
	}

	ad.mu.RLock()
	defer ad.mu.RUnlock()

	rec, err := findDeployment(ad, deploymentID)
	if err != nil {
		return nil, err
	}

	out := rec.Deployment

	return &out, nil
}

// GetDeployments lists one page of an API's Deployments, newest first.
func (m *Mock) GetDeployments(_ context.Context, apiID string, page *driver.PageInput) ([]driver.Deployment, string, error) {
	return listPage(m, apiID, func(ad *apiData) map[string]*deploymentRecord { return ad.deployments },
		func(rec *deploymentRecord) driver.Deployment { return rec.Deployment },
		func(a, b driver.Deployment) bool { return newestFirst(&a, &b) }, page)
}

// newestFirst orders deployments by creation time, newest first, then by id.
func newestFirst(a, b *driver.Deployment) bool {
	if a.CreatedDate != b.CreatedDate {
		return a.CreatedDate > b.CreatedDate
	}

	return a.DeploymentID < b.DeploymentID
}

// UpdateDeployment changes a Deployment's description. The frozen routes and
// integrations never change.
func (m *Mock) UpdateDeployment(
	_ context.Context, apiID, deploymentID string, in *driver.UpdateDeploymentInput,
) (*driver.Deployment, error) {
	ad, err := m.getAPI(apiID)
	if err != nil {
		return nil, err
	}

	ad.mu.Lock()
	defer ad.mu.Unlock()

	rec, err := findDeployment(ad, deploymentID)
	if err != nil {
		return nil, err
	}

	if in.Description != nil {
		if len(*in.Description) > maxDescriptionLen {
			return nil, badRequest("Description must be at most %d characters", maxDescriptionLen)
		}

		rec.Deployment.Description = *in.Description
	}

	out := rec.Deployment

	return &out, nil
}

// DeleteDeployment removes a Deployment no stage points at.
func (m *Mock) DeleteDeployment(_ context.Context, apiID, deploymentID string) error {
	ad, err := m.getAPI(apiID)
	if err != nil {
		return err
	}

	ad.mu.Lock()
	defer ad.mu.Unlock()

	if _, err := findDeployment(ad, deploymentID); err != nil {
		return err
	}

	for _, st := range ad.stages {
		if st.DeploymentID == deploymentID {
			return badRequest("Active stages pointing to this deployment must be moved or deleted")
		}
	}

	delete(ad.deployments, deploymentID)

	return nil
}

// findDeployment returns the stored deployment or a NotFound error. ad must
// be held.
func findDeployment(ad *apiData, deploymentID string) (*deploymentRecord, error) {
	rec, ok := ad.deployments[deploymentID]
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "Invalid deployment identifier specified %s", deploymentID)
	}

	return rec, nil
}

// copyDeploymentRecord returns a deep copy of a deployment record.
func copyDeploymentRecord(rec *deploymentRecord) *deploymentRecord {
	out := &deploymentRecord{
		Deployment:   rec.Deployment,
		Routes:       make(map[string]*driver.Route, len(rec.Routes)),
		Integrations: make(map[string]*driver.Integration, len(rec.Integrations)),
	}

	for id, rt := range rec.Routes {
		cp := copyRoute(rt)
		out.Routes[id] = &cp
	}

	for id, ig := range rec.Integrations {
		cp := copyIntegration(ig)
		out.Integrations[id] = &cp
	}

	return out
}
