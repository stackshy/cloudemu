// Package apigateway implements the Amazon API Gateway REST API (v1) protocol
// as a server.Handler. It serves both the control plane (restJson1 under
// /restapis: RestApi -> Resource -> Method -> Integration -> Deployment ->
// Stage) and the data plane: a request to a deployed API's execute-api URL is
// routed by method+path to the matching integration, which invokes the target
// Lambda and returns its response.
//
// The data plane is reachable two ways, mirroring how clients address a
// deployed REST API:
//
//   - path form (LocalStack-style):
//     /restapis/{apiId}/{stage}/_user_request_/{resourcePath}
//   - host form: a Host of "{apiId}.execute-api.<...>" with path
//     /{stage}/{resourcePath}
//
// The "_user_request_" marker (and the execute-api host) disambiguate a data-
// plane request from the /restapis control plane. It MUST register before the
// S3 REST catch-all.
package apigateway

import (
	"encoding/json"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/apigateway/driver"
)

const (
	controlPrefix    = "/restapis"
	userRequestMark  = "_user_request_"
	executeAPIMarker = ".execute-api."
	contentTypeJSON  = "application/json"
	maxBodyBytes     = 6 << 20
)

// Sub-resource path segments under /restapis/{id}.
const (
	subResources   = "resources"
	subDeployments = "deployments"
	subStages      = "stages"
)

// Control-plane path segment counts (after the /restapis prefix is stripped).
const (
	segsAPI         = 1 // {id}
	segsAPISub      = 2 // {id}/{resources|deployments|stages}
	segsAPISubItem  = 3 // {id}/{resources|stages}/{item}
	segsMethod      = 5 // {id}/resources/{rid}/methods/{httpMethod}
	segsIntegration = 6 // {id}/resources/{rid}/methods/{httpMethod}/integration
)

// Handler serves API Gateway requests against a driver.
type Handler struct {
	ag driver.APIGateway
}

// New returns an API Gateway handler backed by d.
func New(d driver.APIGateway) *Handler {
	return &Handler{ag: d}
}

// Matches claims control-plane requests under /restapis and data-plane requests
// addressed to an execute-api host. It must register before S3's REST catch-all;
// an S3 bucket literally named "restapis" would be shadowed (documented, and not
// a real bucket name).
func (*Handler) Matches(r *http.Request) bool {
	return strings.HasPrefix(r.URL.Path, controlPrefix) || strings.Contains(r.Host, executeAPIMarker)
}

// ServeHTTP dispatches to the data plane (execute-api host or a _user_request_
// path) or the control plane.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.Contains(r.Host, executeAPIMarker) {
		h.serveHostDataPlane(w, r)
		return
	}

	if strings.Contains(r.URL.Path, "/"+userRequestMark) {
		h.servePathDataPlane(w, r)
		return
	}

	h.serveControlPlane(w, r)
}

// serveControlPlane routes the restJson1 management API under /restapis.
func (h *Handler) serveControlPlane(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, controlPrefix)
	rest = strings.Trim(rest, "/")

	if rest == "" {
		h.serveCollection(w, r)
		return
	}

	segs := strings.Split(rest, "/")
	switch len(segs) {
	case segsAPI:
		h.serveAPI(w, r, segs[0])
	case segsAPISub:
		h.serveAPISub(w, r, segs[0], segs[1])
	case segsAPISubItem:
		h.serveAPISubItem(w, r, segs)
	case segsMethod:
		h.serveMethod(w, r, segs)
	case segsIntegration:
		h.serveIntegration(w, r, segs)
	default:
		writeError(w, http.StatusNotFound, "NotFoundException", "unsupported API Gateway path")
	}
}

// serveCollection handles /restapis: GET=GetRestApis, POST=CreateRestApi.
func (h *Handler) serveCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		apis, err := h.ag.GetRestAPIs(r.Context())
		if err != nil {
			writeErr(w, err)
			return
		}

		out := listRestAPIsResponse{Item: make([]restAPIResponse, 0, len(apis))}
		for i := range apis {
			out.Item = append(out.Item, toRestAPIResponse(&apis[i]))
		}

		writeJSON(w, http.StatusOK, out)
	case http.MethodPost:
		h.createRestAPI(w, r)
	default:
		writeMethodNotAllowed(w)
	}
}

func (h *Handler) createRestAPI(w http.ResponseWriter, r *http.Request) {
	var req createRestAPIRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	in := driver.CreateRestAPIInput{
		Name: req.Name, Description: req.Description, Version: req.Version,
		APIKeySource: req.APIKeySource, Tags: req.Tags, BinaryMediaTypes: req.BinaryMediaTypes,
	}
	if req.EndpointConfiguration != nil {
		in.EndpointConfigurationTypes = req.EndpointConfiguration.Types
	}

	api, err := h.ag.CreateRestAPI(r.Context(), &in)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, toRestAPIResponse(api))
}

// serveAPI handles /restapis/{id}: GET=GetRestApi, DELETE=DeleteRestApi.
func (h *Handler) serveAPI(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		api, err := h.ag.GetRestAPI(r.Context(), id)
		if err != nil {
			writeErr(w, err)
			return
		}

		writeJSON(w, http.StatusOK, toRestAPIResponse(api))
	case http.MethodDelete:
		if err := h.ag.DeleteRestAPI(r.Context(), id); err != nil {
			writeErr(w, err)
			return
		}

		w.WriteHeader(http.StatusAccepted)
	default:
		writeMethodNotAllowed(w)
	}
}

// serveAPISub handles /restapis/{id}/{resources|deployments|stages}.
func (h *Handler) serveAPISub(w http.ResponseWriter, r *http.Request, id, sub string) {
	switch sub {
	case subResources:
		h.getResources(w, r, id)
	case subDeployments:
		h.createDeployment(w, r, id)
	case subStages:
		h.createStage(w, r, id)
	default:
		writeError(w, http.StatusNotFound, "NotFoundException", "unsupported API Gateway path")
	}
}

// serveAPISubItem handles /restapis/{id}/resources/{resourceId} and
// /restapis/{id}/stages/{stageName}.
func (h *Handler) serveAPISubItem(w http.ResponseWriter, r *http.Request, segs []string) {
	id, sub, item := segs[0], segs[1], segs[2]

	switch sub {
	case subResources:
		h.serveResourceItem(w, r, id, item)
	case subStages:
		h.getStage(w, r, id, item)
	default:
		writeError(w, http.StatusNotFound, "NotFoundException", "unsupported API Gateway path")
	}
}

func (h *Handler) getResources(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}

	resources, err := h.ag.GetResources(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := listResourcesResponse{Item: make([]resourceResponse, 0, len(resources))}
	for i := range resources {
		out.Item = append(out.Item, toResourceResponse(&resources[i]))
	}

	writeJSON(w, http.StatusOK, out)
}

// serveResourceItem handles /restapis/{id}/resources/{resourceId}:
// GET=GetResource, POST=CreateResource (child under {resourceId}).
func (h *Handler) serveResourceItem(w http.ResponseWriter, r *http.Request, id, resourceID string) {
	switch r.Method {
	case http.MethodGet:
		res, err := h.ag.GetResource(r.Context(), id, resourceID)
		if err != nil {
			writeErr(w, err)
			return
		}

		writeJSON(w, http.StatusOK, toResourceResponse(res))
	case http.MethodPost:
		var req createResourceRequest
		if !decodeJSON(w, r, &req) {
			return
		}

		res, err := h.ag.CreateResource(r.Context(), id, resourceID, req.PathPart)
		if err != nil {
			writeErr(w, err)
			return
		}

		writeJSON(w, http.StatusCreated, toResourceResponse(res))
	default:
		writeMethodNotAllowed(w)
	}
}

// serveMethod handles /restapis/{id}/resources/{rid}/methods/{httpMethod}:
// PUT=PutMethod, GET=GetMethod.
func (h *Handler) serveMethod(w http.ResponseWriter, r *http.Request, segs []string) {
	id, resourceID, httpMethod := segs[0], segs[2], segs[4]

	switch r.Method {
	case http.MethodPut:
		var req putMethodRequest
		if !decodeJSON(w, r, &req) {
			return
		}

		mth, err := h.ag.PutMethod(r.Context(), id, resourceID, httpMethod, driver.PutMethodInput{
			AuthorizationType: req.AuthorizationType, APIKeyRequired: req.APIKeyRequired,
		})
		if err != nil {
			writeErr(w, err)
			return
		}

		writeJSON(w, http.StatusCreated, toMethodResponse(mth))
	case http.MethodGet:
		mth, err := h.ag.GetMethod(r.Context(), id, resourceID, httpMethod)
		if err != nil {
			writeErr(w, err)
			return
		}

		writeJSON(w, http.StatusOK, toMethodResponse(mth))
	default:
		writeMethodNotAllowed(w)
	}
}

// serveIntegration handles
// /restapis/{id}/resources/{rid}/methods/{httpMethod}/integration:
// PUT=PutIntegration, GET=GetIntegration.
func (h *Handler) serveIntegration(w http.ResponseWriter, r *http.Request, segs []string) {
	if segs[5] != "integration" {
		writeError(w, http.StatusNotFound, "NotFoundException", "unsupported API Gateway path")
		return
	}

	id, resourceID, httpMethod := segs[0], segs[2], segs[4]

	switch r.Method {
	case http.MethodPut:
		var req putIntegrationRequest
		if !decodeJSON(w, r, &req) {
			return
		}

		ig, err := h.ag.PutIntegration(r.Context(), id, resourceID, httpMethod, driver.PutIntegrationInput{
			Type: req.Type, IntegrationHTTPMethod: req.IntegrationHTTPMethod,
			URI: req.URI, PassthroughBehavior: req.PassthroughBehavior,
		})
		if err != nil {
			writeErr(w, err)
			return
		}

		writeJSON(w, http.StatusCreated, toIntegrationResponse(ig))
	case http.MethodGet:
		ig, err := h.ag.GetIntegration(r.Context(), id, resourceID, httpMethod)
		if err != nil {
			writeErr(w, err)
			return
		}

		writeJSON(w, http.StatusOK, toIntegrationResponse(ig))
	default:
		writeMethodNotAllowed(w)
	}
}

func (h *Handler) createDeployment(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w)
		return
	}

	var req createDeploymentRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	dep, err := h.ag.CreateDeployment(r.Context(), id, driver.CreateDeploymentInput{
		StageName: req.StageName, Description: req.Description,
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, deploymentResponse{
		ID: dep.ID, Description: dep.Description, CreatedDate: dep.CreatedDate,
	})
}

func (h *Handler) createStage(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w)
		return
	}

	var req createStageRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	st, err := h.ag.CreateStage(r.Context(), id, driver.CreateStageInput{
		StageName: req.StageName, DeploymentID: req.DeploymentID,
		Description: req.Description, Variables: req.Variables,
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, toStageResponse(st))
}

func (h *Handler) getStage(w http.ResponseWriter, r *http.Request, id, stageName string) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}

	st, err := h.ag.GetStage(r.Context(), id, stageName)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toStageResponse(st))
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "BadRequestException", err.Error())
		return false
	}

	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeMethodNotAllowed(w http.ResponseWriter) {
	writeError(w, http.StatusMethodNotAllowed, "BadRequestException", "method not allowed")
}

func writeError(w http.ResponseWriter, status int, errType, msg string) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.Header().Set("X-Amzn-Errortype", errType)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"message": msg, "Message": msg})
}

func writeErr(w http.ResponseWriter, err error) {
	msg := cerrors.Message(err)

	switch {
	case cerrors.IsNotFound(err):
		writeError(w, http.StatusNotFound, "NotFoundException", msg)
	case cerrors.IsAlreadyExists(err):
		writeError(w, http.StatusConflict, "ConflictException", msg)
	case cerrors.IsInvalidArgument(err):
		writeError(w, http.StatusBadRequest, "BadRequestException", msg)
	default:
		writeError(w, http.StatusInternalServerError, "ApiGatewayException", msg)
	}
}
