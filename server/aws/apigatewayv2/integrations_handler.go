package apigatewayv2

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/apigatewayv2/driver"
)

// serveIntegrations handles /v2/apis/{apiId}/integrations: GET=GetIntegrations,
// POST=CreateIntegration.
func (h *Handler) serveIntegrations(w http.ResponseWriter, r *http.Request, apiID string) {
	switch r.Method {
	case http.MethodGet:
		serveList(w,
			func() ([]driver.Integration, error) { return h.ag.GetIntegrations(r.Context(), apiID) },
			toIntegrationResponse)
	case http.MethodPost:
		h.createIntegration(w, r, apiID)
	default:
		writeMethodNotAllowed(w)
	}
}

func (h *Handler) createIntegration(w http.ResponseWriter, r *http.Request, apiID string) {
	var req integrationRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	ig, err := h.ag.CreateIntegration(r.Context(), apiID, &driver.CreateIntegrationInput{
		IntegrationType: req.IntegrationType, IntegrationURI: req.IntegrationURI,
		IntegrationMethod: req.IntegrationMethod, ConnectionType: req.ConnectionType,
		PayloadFormatVersion: req.PayloadFormatVersion, TimeoutInMillis: req.TimeoutInMillis,
		Description: req.Description, RequestParameters: req.RequestParameters,
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, toIntegrationResponse(ig))
}

// serveIntegrationItem handles /v2/apis/{apiId}/integrations/{integrationId}:
// GET, PATCH, DELETE.
func (h *Handler) serveIntegrationItem(w http.ResponseWriter, r *http.Request, apiID, integrationID string) {
	serveItem(w, r,
		func() (*driver.Integration, error) { return h.ag.GetIntegration(r.Context(), apiID, integrationID) },
		toIntegrationResponse,
		func() { h.updateIntegration(w, r, apiID, integrationID) },
		func() error { return h.ag.DeleteIntegration(r.Context(), apiID, integrationID) },
	)
}

func (h *Handler) updateIntegration(w http.ResponseWriter, r *http.Request, apiID, integrationID string) {
	var req updateIntegrationRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	ig, err := h.ag.UpdateIntegration(r.Context(), apiID, integrationID, &driver.UpdateIntegrationInput{
		IntegrationType: req.IntegrationType, IntegrationURI: req.IntegrationURI,
		IntegrationMethod: req.IntegrationMethod, ConnectionType: req.ConnectionType,
		PayloadFormatVersion: req.PayloadFormatVersion, TimeoutInMillis: req.TimeoutInMillis,
		Description: req.Description, RequestParameters: req.RequestParameters,
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toIntegrationResponse(ig))
}
