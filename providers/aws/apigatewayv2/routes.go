package apigatewayv2

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/apigatewayv2/driver"
)

// CreateRoute creates a Route on an API.
func (m *Mock) CreateRoute(_ context.Context, apiID string, in *driver.CreateRouteInput) (*driver.Route, error) {
	ad, err := m.getAPI(apiID)
	if err != nil {
		return nil, err
	}

	if in.RouteKey == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "RouteKey is required")
	}

	ad.mu.Lock()
	defer ad.mu.Unlock()

	rt := &driver.Route{
		RouteID: genID(), RouteKey: in.RouteKey, Target: in.Target,
		AuthorizationType:   orDefault(in.AuthorizationType, defaultAuthorizationType),
		APIKeyRequired:      in.APIKeyRequired,
		AuthorizerID:        in.AuthorizerID,
		AuthorizationScopes: append([]string(nil), in.AuthorizationScopes...),
		OperationName:       in.OperationName,
	}
	ad.routes[rt.RouteID] = rt

	out := copyRoute(rt)

	return &out, nil
}

// GetRoute returns a single Route.
func (m *Mock) GetRoute(_ context.Context, apiID, routeID string) (*driver.Route, error) {
	ad, err := m.getAPI(apiID)
	if err != nil {
		return nil, err
	}

	ad.mu.RLock()
	defer ad.mu.RUnlock()

	rt, ok := ad.routes[routeID]
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "Invalid route identifier specified %s", routeID)
	}

	out := copyRoute(rt)

	return &out, nil
}

// GetRoutes lists an API's Routes.
func (m *Mock) GetRoutes(_ context.Context, apiID string) ([]driver.Route, error) {
	ad, err := m.getAPI(apiID)
	if err != nil {
		return nil, err
	}

	ad.mu.RLock()
	defer ad.mu.RUnlock()

	out := make([]driver.Route, 0, len(ad.routes))
	for _, rt := range ad.routes {
		out = append(out, copyRoute(rt))
	}

	return out, nil
}

// UpdateRoute applies the non-nil fields of in to a stored Route (PATCH).
func (m *Mock) UpdateRoute(_ context.Context, apiID, routeID string, in *driver.UpdateRouteInput) (*driver.Route, error) {
	ad, err := m.getAPI(apiID)
	if err != nil {
		return nil, err
	}

	ad.mu.Lock()
	defer ad.mu.Unlock()

	rt, ok := ad.routes[routeID]
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "Invalid route identifier specified %s", routeID)
	}

	setString(&rt.RouteKey, in.RouteKey)
	setString(&rt.Target, in.Target)
	setString(&rt.AuthorizationType, in.AuthorizationType)
	setString(&rt.AuthorizerID, in.AuthorizerID)
	setString(&rt.OperationName, in.OperationName)
	setBool(&rt.APIKeyRequired, in.APIKeyRequired)

	out := copyRoute(rt)

	return &out, nil
}

// DeleteRoute removes a Route.
func (m *Mock) DeleteRoute(_ context.Context, apiID, routeID string) error {
	ad, err := m.getAPI(apiID)
	if err != nil {
		return err
	}

	ad.mu.Lock()
	defer ad.mu.Unlock()

	if _, ok := ad.routes[routeID]; !ok {
		return cerrors.Newf(cerrors.NotFound, "Invalid route identifier specified %s", routeID)
	}

	delete(ad.routes, routeID)

	return nil
}

// copyRoute returns a deep copy of a Route.
func copyRoute(r *driver.Route) driver.Route {
	out := *r
	out.AuthorizationScopes = append([]string(nil), r.AuthorizationScopes...)

	return out
}
