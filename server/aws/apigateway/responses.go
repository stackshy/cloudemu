package apigateway

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/apigateway/driver"
)

// Path segments of the method and integration response routes.
const (
	segMethods     = "methods"
	segIntegration = "integration"
	segResponses   = "responses"
)

// serveMethodResponse handles
// /restapis/{id}/resources/{rid}/methods/{httpMethod}/responses/{statusCode}:
// PUT=PutMethodResponse, GET=GetMethodResponse, PATCH=UpdateMethodResponse,
// DELETE=DeleteMethodResponse.
func (h *Handler) serveMethodResponse(w http.ResponseWriter, r *http.Request, segs []string) {
	if segs[3] != segMethods || segs[5] != segResponses {
		writeError(w, http.StatusNotFound, "NotFoundException", "unsupported API Gateway path")
		return
	}

	id, resourceID, httpMethod, code := segs[0], segs[2], segs[4], segs[6]
	ctx := r.Context()

	if r.Method == http.MethodPut {
		var req putMethodResponseRequest
		if !decodeJSON(w, r, &req) {
			return
		}

		mr, err := h.ag.PutMethodResponse(ctx, id, resourceID, httpMethod, code, driver.PutMethodResponseInput{
			ResponseParameters: req.ResponseParameters, ResponseModels: req.ResponseModels,
		})
		if err != nil {
			writeErr(w, err)
			return
		}

		writeJSON(w, http.StatusCreated, toMethodResponseObject(mr))

		return
	}

	serveItem(w, r,
		func(ops []driver.PatchOperation) (*driver.MethodResponse, error) {
			return h.ag.UpdateMethodResponse(ctx, id, resourceID, httpMethod, code, ops)
		},
		func() (*driver.MethodResponse, error) {
			return h.ag.GetMethodResponse(ctx, id, resourceID, httpMethod, code)
		},
		func() error { return h.ag.DeleteMethodResponse(ctx, id, resourceID, httpMethod, code) },
		toMethodResponseObject,
	)
}

// serveIntegrationResponse handles
// /restapis/{id}/resources/{rid}/methods/{httpMethod}/integration/responses/{statusCode}:
// PUT=PutIntegrationResponse, GET=GetIntegrationResponse,
// PATCH=UpdateIntegrationResponse, DELETE=DeleteIntegrationResponse.
func (h *Handler) serveIntegrationResponse(w http.ResponseWriter, r *http.Request, segs []string) {
	if segs[3] != segMethods || segs[5] != segIntegration || segs[6] != segResponses {
		writeError(w, http.StatusNotFound, "NotFoundException", "unsupported API Gateway path")
		return
	}

	id, resourceID, httpMethod, code := segs[0], segs[2], segs[4], segs[7]
	ctx := r.Context()

	if r.Method == http.MethodPut {
		var req putIntegrationResponseRequest
		if !decodeJSON(w, r, &req) {
			return
		}

		ir, err := h.ag.PutIntegrationResponse(ctx, id, resourceID, httpMethod, code, driver.PutIntegrationResponseInput{
			SelectionPattern: req.SelectionPattern, ResponseParameters: req.ResponseParameters,
			ResponseTemplates: req.ResponseTemplates, ContentHandling: req.ContentHandling,
		})
		if err != nil {
			writeErr(w, err)
			return
		}

		writeJSON(w, http.StatusCreated, toIntegrationResponseObject(ir))

		return
	}

	serveItem(w, r,
		func(ops []driver.PatchOperation) (*driver.IntegrationResponse, error) {
			return h.ag.UpdateIntegrationResponse(ctx, id, resourceID, httpMethod, code, ops)
		},
		func() (*driver.IntegrationResponse, error) {
			return h.ag.GetIntegrationResponse(ctx, id, resourceID, httpMethod, code)
		},
		func() error { return h.ag.DeleteIntegrationResponse(ctx, id, resourceID, httpMethod, code) },
		toIntegrationResponseObject,
	)
}
