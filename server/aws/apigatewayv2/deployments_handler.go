package apigatewayv2

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/apigatewayv2/driver"
)

// serveDeployments handles /v2/apis/{apiId}/deployments: GET=GetDeployments,
// POST=CreateDeployment.
func (h *Handler) serveDeployments(w http.ResponseWriter, r *http.Request, apiID string) {
	switch r.Method {
	case http.MethodGet:
		serveList(w, func() ([]driver.Deployment, string, error) {
			return h.ag.GetDeployments(r.Context(), apiID, pageInput(r))
		}, toDeploymentResponse)
	case http.MethodPost:
		h.createDeployment(w, r, apiID)
	default:
		writeMethodNotAllowed(w)
	}
}

func (h *Handler) createDeployment(w http.ResponseWriter, r *http.Request, apiID string) {
	var req deploymentRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	d, err := h.ag.CreateDeployment(r.Context(), apiID, &driver.CreateDeploymentInput{
		Description: req.Description, StageName: req.StageName,
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, toDeploymentResponse(d))
}

// serveDeploymentItem handles /v2/apis/{apiId}/deployments/{deploymentId}:
// GET, PATCH, DELETE.
func (h *Handler) serveDeploymentItem(w http.ResponseWriter, r *http.Request, apiID, deploymentID string) {
	serveItem(w, r,
		func() (*driver.Deployment, error) { return h.ag.GetDeployment(r.Context(), apiID, deploymentID) },
		toDeploymentResponse,
		func() { h.updateDeployment(w, r, apiID, deploymentID) },
		func() error { return h.ag.DeleteDeployment(r.Context(), apiID, deploymentID) },
	)
}

func (h *Handler) updateDeployment(w http.ResponseWriter, r *http.Request, apiID, deploymentID string) {
	var req updateDeploymentRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	d, err := h.ag.UpdateDeployment(r.Context(), apiID, deploymentID, &driver.UpdateDeploymentInput{
		Description: req.Description,
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toDeploymentResponse(d))
}
