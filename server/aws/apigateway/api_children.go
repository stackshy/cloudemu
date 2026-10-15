package apigateway

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/apigateway/driver"
)

// serveAuthorizers handles /restapis/{id}/authorizers[/{authorizerId}]. A POST
// on an item is TestInvokeAuthorizer.
func (h *Handler) serveAuthorizers(w http.ResponseWriter, r *http.Request, id, item string) {
	svc, ok := h.ag.(driver.Authorizers)
	if !ok {
		notImplemented(w)
		return
	}

	if item == "" {
		h.serveAuthorizerCollection(w, r, svc, id)
		return
	}

	if r.Method == http.MethodPost {
		h.testInvokeAuthorizer(w, r, id, item)
		return
	}

	serveItem(w, r,
		func(ops []driver.PatchOperation) (*driver.Authorizer, error) {
			return svc.UpdateAuthorizer(r.Context(), id, item, ops)
		},
		func() (*driver.Authorizer, error) { return svc.GetAuthorizer(r.Context(), id, item) },
		func() error { return svc.DeleteAuthorizer(r.Context(), id, item) },
		toAuthorizerResponse,
	)
}

func (*Handler) serveAuthorizerCollection(w http.ResponseWriter, r *http.Request, svc driver.Authorizers, id string) {
	switch r.Method {
	case http.MethodGet:
		serveListOf(w, r, func(page driver.PageInput) ([]driver.Authorizer, string, error) {
			res, err := svc.GetAuthorizers(r.Context(), id, page)
			if err != nil {
				return nil, "", err
			}

			return res.Items, res.Position, nil
		}, toAuthorizerResponse)
	case http.MethodPost:
		serveCreateOf(w, r, func(req *authorizerRequest) (*driver.Authorizer, error) {
			return svc.CreateAuthorizer(r.Context(), id, &driver.CreateAuthorizerInput{
				Name: req.Name, Type: req.Type, ProviderARNs: req.ProviderARNs, AuthType: req.AuthType,
				AuthorizerURI: req.AuthorizerURI, AuthorizerCredentials: req.AuthorizerCredentials,
				IdentitySource: req.IdentitySource, IdentityValidationExpression: req.IdentityValidationExpression,
				AuthorizerResultTTLInSeconds: req.AuthorizerResultTTLInSeconds,
			})
		}, toAuthorizerResponse)
	default:
		writeMethodNotAllowed(w)
	}
}

func (h *Handler) testInvokeAuthorizer(w http.ResponseWriter, r *http.Request, id, authorizerID string) {
	ti, ok := h.ag.(driver.TestInvoker)
	if !ok {
		notImplemented(w)
		return
	}

	var req testInvokeAuthorizerRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	out, err := ti.TestInvokeAuthorizer(r.Context(), &driver.TestInvokeAuthorizerInput{
		RestAPIID: id, AuthorizerID: authorizerID, Headers: req.Headers, MultiValueHeaders: req.MultiValueHeaders,
		PathWithQuery: req.PathWithQueryString, Body: req.Body, StageVariables: req.StageVariables,
		AdditionalContext: req.AdditionalContext,
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, testInvokeAuthorizerResponse{
		ClientStatus: out.ClientStatus, Log: out.Log, Latency: out.LatencyMillis, Policy: out.Policy,
		PrincipalID: out.PrincipalID, Authorization: out.Authorization, Claims: out.Claims,
	})
}

// testInvokeMethod handles POST on a method: TestInvokeMethod.
func (h *Handler) testInvokeMethod(w http.ResponseWriter, r *http.Request, segs []string) {
	ti, ok := h.ag.(driver.TestInvoker)
	if !ok {
		notImplemented(w)
		return
	}

	var req testInvokeMethodRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	out, err := ti.TestInvokeMethod(r.Context(), &driver.TestInvokeMethodInput{
		RestAPIID: segs[0], ResourceID: segs[2], HTTPMethod: segs[4], PathWithQuery: req.PathWithQueryString,
		Body: req.Body, Headers: req.Headers, MultiValueHeaders: req.MultiValueHeaders,
		StageVariables: req.StageVariables, ClientCertificateID: req.ClientCertificateID,
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, testInvokeMethodResponse{
		Status: out.Status, Body: out.Body, Headers: out.Headers, MultiValueHeaders: out.MultiValueHeaders,
		Log: out.Log, Latency: out.LatencyMillis,
	})
}

// serveModels handles /restapis/{id}/models[/{modelName}].
func (h *Handler) serveModels(w http.ResponseWriter, r *http.Request, id, item string) {
	svc, ok := h.ag.(driver.Models)
	if !ok {
		notImplemented(w)
		return
	}

	if item != "" {
		serveItem(w, r,
			func(ops []driver.PatchOperation) (*driver.Model, error) {
				return svc.UpdateModel(r.Context(), id, item, ops)
			},
			func() (*driver.Model, error) { return svc.GetModel(r.Context(), id, item) },
			func() error { return svc.DeleteModel(r.Context(), id, item) },
			toModelResponse,
		)

		return
	}

	switch r.Method {
	case http.MethodGet:
		serveListOf(w, r, func(page driver.PageInput) ([]driver.Model, string, error) {
			res, err := svc.GetModels(r.Context(), id, page)
			if err != nil {
				return nil, "", err
			}

			return res.Items, res.Position, nil
		}, toModelResponse)
	case http.MethodPost:
		serveCreateOf(w, r, func(req *modelRequest) (*driver.Model, error) {
			return svc.CreateModel(r.Context(), id, &driver.CreateModelInput{
				Name: req.Name, Description: req.Description, Schema: req.Schema, ContentType: req.ContentType,
			})
		}, toModelResponse)
	default:
		writeMethodNotAllowed(w)
	}
}

// serveValidators handles /restapis/{id}/requestvalidators[/{validatorId}].
func (h *Handler) serveValidators(w http.ResponseWriter, r *http.Request, id, item string) {
	svc, ok := h.ag.(driver.RequestValidators)
	if !ok {
		notImplemented(w)
		return
	}

	if item != "" {
		serveItem(w, r,
			func(ops []driver.PatchOperation) (*driver.RequestValidator, error) {
				return svc.UpdateRequestValidator(r.Context(), id, item, ops)
			},
			func() (*driver.RequestValidator, error) { return svc.GetRequestValidator(r.Context(), id, item) },
			func() error { return svc.DeleteRequestValidator(r.Context(), id, item) },
			toRequestValidatorResponse,
		)

		return
	}

	switch r.Method {
	case http.MethodGet:
		serveListOf(w, r, func(page driver.PageInput) ([]driver.RequestValidator, string, error) {
			res, err := svc.GetRequestValidators(r.Context(), id, page)
			if err != nil {
				return nil, "", err
			}

			return res.Items, res.Position, nil
		}, toRequestValidatorResponse)
	case http.MethodPost:
		serveCreateOf(w, r, func(req *requestValidatorRequest) (*driver.RequestValidator, error) {
			return svc.CreateRequestValidator(r.Context(), id, &driver.CreateRequestValidatorInput{
				Name: req.Name, ValidateRequestBody: req.ValidateRequestBody,
				ValidateRequestParameters: req.ValidateRequestParameters,
			})
		}, toRequestValidatorResponse)
	default:
		writeMethodNotAllowed(w)
	}
}

// serveGatewayResponses handles /restapis/{id}/gatewayresponses[/{responseType}].
func (h *Handler) serveGatewayResponses(w http.ResponseWriter, r *http.Request, id, item string) {
	svc, ok := h.ag.(driver.GatewayResponses)
	if !ok {
		notImplemented(w)
		return
	}

	if item == "" {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w)
			return
		}

		serveListOf(w, r, func(page driver.PageInput) ([]driver.GatewayResponse, string, error) {
			res, err := svc.GetGatewayResponses(r.Context(), id, page)
			if err != nil {
				return nil, "", err
			}

			return res.Items, res.Position, nil
		}, toGatewayResponseResponse)

		return
	}

	if r.Method == http.MethodPut {
		var req gatewayResponseRequest
		if !decodeJSON(w, r, &req) {
			return
		}

		gr, err := svc.PutGatewayResponse(r.Context(), id, item, &driver.PutGatewayResponseInput{
			StatusCode: req.StatusCode, ResponseParameters: req.ResponseParameters, ResponseTemplates: req.ResponseTemplates,
		})
		if err != nil {
			writeErr(w, err)
			return
		}

		writeJSON(w, http.StatusCreated, toGatewayResponseResponse(gr))

		return
	}

	serveItem(w, r,
		func(ops []driver.PatchOperation) (*driver.GatewayResponse, error) {
			return svc.UpdateGatewayResponse(r.Context(), id, item, ops)
		},
		func() (*driver.GatewayResponse, error) { return svc.GetGatewayResponse(r.Context(), id, item) },
		func() error { return svc.DeleteGatewayResponse(r.Context(), id, item) },
		toGatewayResponseResponse,
	)
}
