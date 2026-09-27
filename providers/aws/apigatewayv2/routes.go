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

	ad.mu.Lock()
	defer ad.mu.Unlock()

	protocol := ad.api.ProtocolType
	authType := orDefault(in.AuthorizationType, defaultAuthorizationType)

	if err := validateRouteKey(protocol, in.RouteKey); err != nil {
		return nil, err
	}

	if err := validateAuthorizationType(protocol, authType); err != nil {
		return nil, err
	}

	if err := validateRouteTarget(ad, in.Target); err != nil {
		return nil, err
	}

	if err := checkRouteKeyFree(ad, in.RouteKey, ""); err != nil {
		return nil, err
	}

	rt := &driver.Route{
		RouteID: genID(), RouteKey: in.RouteKey, Target: in.Target,
		AuthorizationType:   authType,
		APIKeyRequired:      in.APIKeyRequired,
		AuthorizerID:        in.AuthorizerID,
		AuthorizationScopes: append([]string(nil), in.AuthorizationScopes...),
		OperationName:       in.OperationName,
	}
	ad.routes[rt.RouteID] = rt
	m.autoDeploy(ad)

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

// GetRoutes lists one page of an API's Routes, ordered by id.
func (m *Mock) GetRoutes(_ context.Context, apiID string, page *driver.PageInput) ([]driver.Route, string, error) {
	return listPage(m, apiID, func(ad *apiData) map[string]*driver.Route { return ad.routes }, copyRoute,
		func(a, b driver.Route) bool { return a.RouteID < b.RouteID }, page)
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

	next := copyRoute(rt)
	setString(&next.RouteKey, in.RouteKey)
	setString(&next.Target, in.Target)
	setString(&next.AuthorizationType, in.AuthorizationType)
	setString(&next.AuthorizerID, in.AuthorizerID)
	setString(&next.OperationName, in.OperationName)
	setBool(&next.APIKeyRequired, in.APIKeyRequired)

	if in.AuthorizationScopes != nil {
		next.AuthorizationScopes = append([]string(nil), in.AuthorizationScopes...)
	}

	if err := validateRouteUpdate(ad, rt, &next); err != nil {
		return nil, err
	}

	*rt = next

	m.autoDeploy(ad)

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
	m.autoDeploy(ad)

	return nil
}

// validateRouteUpdate checks the route an UpdateRoute would produce. A managed
// (quick-create) route keeps its route key. ad must be held.
func validateRouteUpdate(ad *apiData, cur, next *driver.Route) error {
	protocol := ad.api.ProtocolType

	if next.RouteKey != cur.RouteKey {
		if cur.APIGatewayManaged {
			return badRequest("Cannot modify the route key of a route managed by API Gateway")
		}

		if err := validateRouteKey(protocol, next.RouteKey); err != nil {
			return err
		}

		if err := checkRouteKeyFree(ad, next.RouteKey, cur.RouteID); err != nil {
			return err
		}
	}

	if err := validateAuthorizationType(protocol, next.AuthorizationType); err != nil {
		return err
	}

	if next.Target == cur.Target {
		return nil
	}

	return validateRouteTarget(ad, next.Target)
}

// copyRoute returns a deep copy of a Route.
func copyRoute(r *driver.Route) driver.Route {
	out := *r
	out.AuthorizationScopes = append([]string(nil), r.AuthorizationScopes...)

	return out
}
