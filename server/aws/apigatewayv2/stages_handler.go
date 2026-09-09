package apigatewayv2

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/apigatewayv2/driver"
)

// serveStages handles /v2/apis/{apiId}/stages: GET=GetStages, POST=CreateStage.
func (h *Handler) serveStages(w http.ResponseWriter, r *http.Request, apiID string) {
	switch r.Method {
	case http.MethodGet:
		serveList(w, func() ([]driver.Stage, error) { return h.ag.GetStages(r.Context(), apiID) }, toStageResponse)
	case http.MethodPost:
		h.createStage(w, r, apiID)
	default:
		writeMethodNotAllowed(w)
	}
}

func (h *Handler) createStage(w http.ResponseWriter, r *http.Request, apiID string) {
	var req stageRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	st, err := h.ag.CreateStage(r.Context(), apiID, &driver.CreateStageInput{
		StageName: req.StageName, Description: req.Description, AutoDeploy: req.AutoDeploy,
		DeploymentID: req.DeploymentID, StageVariables: req.StageVariables,
		DefaultRouteSettings: routeSettingsToDriver(req.DefaultRouteSettings),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, toStageResponse(st))
}

// serveStageItem handles /v2/apis/{apiId}/stages/{stageName}: GET, PATCH, DELETE.
func (h *Handler) serveStageItem(w http.ResponseWriter, r *http.Request, apiID, stageName string) {
	serveItem(w, r,
		func() (*driver.Stage, error) { return h.ag.GetStage(r.Context(), apiID, stageName) },
		toStageResponse,
		func() { h.updateStage(w, r, apiID, stageName) },
		func() error { return h.ag.DeleteStage(r.Context(), apiID, stageName) },
	)
}

func (h *Handler) updateStage(w http.ResponseWriter, r *http.Request, apiID, stageName string) {
	var req updateStageRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	st, err := h.ag.UpdateStage(r.Context(), apiID, stageName, &driver.UpdateStageInput{
		Description: req.Description, AutoDeploy: req.AutoDeploy, DeploymentID: req.DeploymentID,
		StageVariables:       req.StageVariables,
		DefaultRouteSettings: routeSettingsToDriver(req.DefaultRouteSettings),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toStageResponse(st))
}
