package apigatewayv2

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/apigatewayv2/driver"
)

// serveRoutes handles /v2/apis/{apiId}/routes: GET=GetRoutes, POST=CreateRoute.
func (h *Handler) serveRoutes(w http.ResponseWriter, r *http.Request, apiID string) {
	switch r.Method {
	case http.MethodGet:
		serveList(w, func() ([]driver.Route, error) { return h.ag.GetRoutes(r.Context(), apiID) }, toRouteResponse)
	case http.MethodPost:
		h.createRoute(w, r, apiID)
	default:
		writeMethodNotAllowed(w)
	}
}

func (h *Handler) createRoute(w http.ResponseWriter, r *http.Request, apiID string) {
	var req routeRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	rt, err := h.ag.CreateRoute(r.Context(), apiID, &driver.CreateRouteInput{
		RouteKey: req.RouteKey, Target: req.Target,
		AuthorizationType: req.AuthorizationType, APIKeyRequired: req.APIKeyRequired,
		AuthorizerID: req.AuthorizerID, AuthorizationScopes: req.AuthorizationScopes,
		OperationName: req.OperationName,
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, toRouteResponse(rt))
}

// serveRouteItem handles /v2/apis/{apiId}/routes/{routeId}: GET, PATCH, DELETE.
func (h *Handler) serveRouteItem(w http.ResponseWriter, r *http.Request, apiID, routeID string) {
	serveItem(w, r,
		func() (*driver.Route, error) { return h.ag.GetRoute(r.Context(), apiID, routeID) },
		toRouteResponse,
		func() { h.updateRoute(w, r, apiID, routeID) },
		func() error { return h.ag.DeleteRoute(r.Context(), apiID, routeID) },
	)
}

func (h *Handler) updateRoute(w http.ResponseWriter, r *http.Request, apiID, routeID string) {
	var req updateRouteRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	rt, err := h.ag.UpdateRoute(r.Context(), apiID, routeID, &driver.UpdateRouteInput{
		RouteKey: req.RouteKey, Target: req.Target,
		AuthorizationType: req.AuthorizationType, APIKeyRequired: req.APIKeyRequired,
		AuthorizerID: req.AuthorizerID, OperationName: req.OperationName,
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toRouteResponse(rt))
}
