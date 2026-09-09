// Package apigatewayv2 implements the Amazon API Gateway v2 (HTTP/WebSocket
// APIs) control-plane protocol as a server.Handler. It serves the restJson1
// management API rooted at /v2/apis: Api CRUD plus its Route, Integration and
// Stage sub-collections.
//
// This is a distinct service from API Gateway REST v1 (server/aws/apigateway,
// rooted at /restapis): the two share no path prefix, so registering this
// handler never shadows v1. Both must register before the S3 REST catch-all,
// which would otherwise claim /v2/apis as a bucket path.
package apigatewayv2

import (
	"encoding/json"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/apigatewayv2/driver"
)

const (
	controlPrefix   = "/v2/apis"
	contentTypeJSON = "application/json"
	maxBodyBytes    = 6 << 20
)

// Sub-collection path segments under /v2/apis/{apiId}.
const (
	subRoutes       = "routes"
	subIntegrations = "integrations"
	subStages       = "stages"
)

// Path segment counts after the /v2/apis prefix is stripped.
const (
	segsAPIItem = 1 // {apiId}
	segsSubColl = 2 // {apiId}/{routes|integrations|stages}
	segsSubItem = 3 // {apiId}/{routes|integrations|stages}/{itemId}
)

// Handler serves apigatewayv2 requests against a driver.
type Handler struct {
	ag driver.APIGatewayV2
}

// New returns an apigatewayv2 handler backed by d.
func New(d driver.APIGatewayV2) *Handler {
	return &Handler{ag: d}
}

// Matches claims control-plane requests under /v2/apis. This prefix is disjoint
// from API Gateway REST v1 (/restapis) and every other AWS handler; it must
// register before S3's permissive REST catch-all.
func (*Handler) Matches(r *http.Request) bool {
	return r.URL.Path == "/v2/apis" || strings.HasPrefix(r.URL.Path, controlPrefix+"/")
}

// ServeHTTP routes the restJson1 management API under /v2/apis by segment count.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, controlPrefix), "/")

	if rest == "" {
		h.serveCollection(w, r)
		return
	}

	segs := strings.Split(rest, "/")
	switch len(segs) {
	case segsAPIItem:
		h.serveAPIItem(w, r, segs[0])
	case segsSubColl:
		h.serveSubCollection(w, r, segs[0], segs[1])
	case segsSubItem:
		h.serveSubItem(w, r, segs[0], segs[1], segs[2])
	default:
		writeError(w, http.StatusNotFound, "NotFoundException", "unsupported apigatewayv2 path")
	}
}

// serveCollection handles /v2/apis: GET=GetApis, POST=CreateApi.
func (h *Handler) serveCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		serveList(w, func() ([]driver.API, error) { return h.ag.GetAPIs(r.Context()) }, toAPIResponse)
	case http.MethodPost:
		h.createAPI(w, r)
	default:
		writeMethodNotAllowed(w)
	}
}

func (h *Handler) createAPI(w http.ResponseWriter, r *http.Request) {
	var req createAPIRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	api, err := h.ag.CreateAPI(r.Context(), &driver.CreateAPIInput{
		Name: req.Name, ProtocolType: req.ProtocolType, Description: req.Description,
		Version:                   req.Version,
		RouteSelectionExpression:  req.RouteSelectionExpression,
		APIKeySelectionExpression: req.APIKeySelectionExpression,
		DisableExecuteAPIEndpoint: req.DisableExecuteAPIEndpoint,
		Tags:                      req.Tags,
		CorsConfiguration:         corsToDriver(req.CorsConfiguration),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, toAPIResponse(api))
}

// serveAPIItem handles /v2/apis/{apiId}: GET, PATCH=UpdateApi, DELETE.
func (h *Handler) serveAPIItem(w http.ResponseWriter, r *http.Request, apiID string) {
	switch r.Method {
	case http.MethodGet:
		api, err := h.ag.GetAPI(r.Context(), apiID)
		if err != nil {
			writeErr(w, err)
			return
		}

		writeJSON(w, http.StatusOK, toAPIResponse(api))
	case http.MethodPatch:
		h.updateAPI(w, r, apiID)
	case http.MethodDelete:
		if err := h.ag.DeleteAPI(r.Context(), apiID); err != nil {
			writeErr(w, err)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	default:
		writeMethodNotAllowed(w)
	}
}

func (h *Handler) updateAPI(w http.ResponseWriter, r *http.Request, apiID string) {
	var req updateAPIRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	api, err := h.ag.UpdateAPI(r.Context(), apiID, &driver.UpdateAPIInput{
		Name: req.Name, Description: req.Description, Version: req.Version,
		RouteSelectionExpression:  req.RouteSelectionExpression,
		APIKeySelectionExpression: req.APIKeySelectionExpression,
		DisableExecuteAPIEndpoint: req.DisableExecuteAPIEndpoint,
		CorsConfiguration:         corsToDriver(req.CorsConfiguration),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toAPIResponse(api))
}

// serveSubCollection routes /v2/apis/{apiId}/{routes|integrations|stages}.
func (h *Handler) serveSubCollection(w http.ResponseWriter, r *http.Request, apiID, sub string) {
	switch sub {
	case subRoutes:
		h.serveRoutes(w, r, apiID)
	case subIntegrations:
		h.serveIntegrations(w, r, apiID)
	case subStages:
		h.serveStages(w, r, apiID)
	default:
		writeError(w, http.StatusNotFound, "NotFoundException", "unsupported apigatewayv2 path")
	}
}

// serveSubItem routes /v2/apis/{apiId}/{routes|integrations|stages}/{itemId}.
func (h *Handler) serveSubItem(w http.ResponseWriter, r *http.Request, apiID, sub, item string) {
	switch sub {
	case subRoutes:
		h.serveRouteItem(w, r, apiID, item)
	case subIntegrations:
		h.serveIntegrationItem(w, r, apiID, item)
	case subStages:
		h.serveStageItem(w, r, apiID, item)
	default:
		writeError(w, http.StatusNotFound, "NotFoundException", "unsupported apigatewayv2 path")
	}
}

// collectionResponse is the {"items":[...]} envelope every apigatewayv2 list
// operation (GetApis/GetRoutes/GetIntegrations/GetStages) returns.
type collectionResponse[R any] struct {
	Items []R `json:"items"`
}

// serveList renders a GET list: it runs list, maps each element through render
// and writes the {"items":[...]} envelope.
func serveList[T, R any](w http.ResponseWriter, list func() ([]T, error), render func(*T) R) {
	items, err := list()
	if err != nil {
		writeErr(w, err)
		return
	}

	out := make([]R, 0, len(items))
	for i := range items {
		out = append(out, render(&items[i]))
	}

	writeJSON(w, http.StatusOK, collectionResponse[R]{Items: out})
}

// serveItem dispatches the GET/PATCH/DELETE shape shared by every apigatewayv2
// sub-resource item route (route, integration, stage): GET renders get()'s
// result through render, PATCH delegates to update, DELETE calls del() and
// returns 204.
func serveItem[T, R any](
	w http.ResponseWriter, r *http.Request,
	get func() (*T, error), render func(*T) R, update func(), del func() error,
) {
	switch r.Method {
	case http.MethodGet:
		v, err := get()
		if err != nil {
			writeErr(w, err)
			return
		}

		writeJSON(w, http.StatusOK, render(v))
	case http.MethodPatch:
		update()
	case http.MethodDelete:
		if err := del(); err != nil {
			writeErr(w, err)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	default:
		writeMethodNotAllowed(w)
	}
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
	case cerrors.IsInvalidArgument(err), cerrors.IsFailedPrecondition(err):
		writeError(w, http.StatusBadRequest, "BadRequestException", msg)
	default:
		writeError(w, http.StatusInternalServerError, "InternalServerErrorException", msg)
	}
}
