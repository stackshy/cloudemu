package apigatewayv2

import (
	"strings"

	"github.com/stackshy/cloudemu/v2/services/apigatewayv2/driver"
)

// Integration methods quick create gives each target kind.
const (
	quickLambdaMethod = "POST"
	quickHTTPMethod   = "ANY"
)

// quickTarget is the integration shape a quick-create Target maps to.
type quickTarget struct {
	integrationType string
	method          string
	payloadFormat   string
}

// resolveQuickTarget maps a quick-create Target to its integration: a Lambda
// function ARN becomes AWS_PROXY with payload 2.0, and an HTTP(S) URL becomes
// HTTP_PROXY with payload 1.0.
func resolveQuickTarget(target string) (quickTarget, error) {
	switch {
	case strings.HasPrefix(target, "arn:") && strings.Contains(target, ":lambda:"):
		return quickTarget{driver.IntegrationAWSProxy, quickLambdaMethod, payloadFormatV2}, nil
	case strings.HasPrefix(target, "https://"), strings.HasPrefix(target, "http://"):
		return quickTarget{driver.IntegrationHTTPProxy, quickHTTPMethod, defaultPayloadFormat}, nil
	default:
		return quickTarget{}, badRequest("Target must be a Lambda function ARN or an HTTP(S) URL")
	}
}

// checkQuickCreate validates the quick-create fields of a CreateApi request.
func checkQuickCreate(protocol, target, routeKey, credentialsArn string) error {
	if target == "" {
		if routeKey != "" || credentialsArn != "" {
			return badRequest("RouteKey and CredentialsArn can only be specified with Target")
		}

		return nil
	}

	if protocol != driver.ProtocolHTTP {
		return badRequest("Quick create is only supported for HTTP APIs")
	}

	if _, err := resolveQuickTarget(target); err != nil {
		return err
	}

	return validateRouteKey(protocol, orDefault(routeKey, defaultKey))
}

// quickCreate builds the managed integration, route and auto-deployed $default
// stage for a validated quick-create request. ad must be held for writing.
func (m *Mock) quickCreate(ad *apiData, target, routeKey, credentialsArn string) {
	qt, _ := resolveQuickTarget(target)

	ig := &driver.Integration{
		IntegrationID: genID(), IntegrationType: qt.integrationType, IntegrationURI: target,
		IntegrationMethod: qt.method, ConnectionType: defaultConnectionType,
		PayloadFormatVersion: qt.payloadFormat, TimeoutInMillis: defaultHTTPTimeoutMillis,
		CredentialsArn: credentialsArn, APIGatewayManaged: true,
	}
	ad.integrations[ig.IntegrationID] = ig

	rt := &driver.Route{
		RouteID: genID(), RouteKey: orDefault(routeKey, defaultKey),
		Target:            integrationsPrefix + ig.IntegrationID,
		AuthorizationType: defaultAuthorizationType, APIGatewayManaged: true,
	}
	ad.routes[rt.RouteID] = rt

	if _, ok := ad.stages[defaultKey]; !ok {
		now := m.now()
		ad.stages[defaultKey] = &driver.Stage{
			StageName: defaultKey, AutoDeploy: true, APIGatewayManaged: true,
			CreatedDate: now, LastUpdatedDate: now,
		}
	}

	m.autoDeploy(ad)
}

// managedResources returns the quick-create integration and route of an API,
// or nils when it was not quick created. ad must be held.
func managedResources(ad *apiData) (*driver.Integration, *driver.Route) {
	var ig *driver.Integration

	for _, i := range ad.integrations {
		if i.APIGatewayManaged {
			ig = i
		}
	}

	var rt *driver.Route

	for _, r := range ad.routes {
		if r.APIGatewayManaged {
			rt = r
		}
	}

	return ig, rt
}

// updateQuickCreate applies UpdateApi's Target, RouteKey and CredentialsArn to
// the managed integration and route, creating them when the API was not quick
// created yet. ad must be held for writing.
func (m *Mock) updateQuickCreate(ad *apiData, in *driver.UpdateAPIInput) error {
	if in.Target == nil && in.RouteKey == nil && in.CredentialsArn == nil {
		return nil
	}

	ig, rt := managedResources(ad)
	if ig == nil || rt == nil {
		err := checkQuickCreate(ad.api.ProtocolType, deref(in.Target), deref(in.RouteKey), deref(in.CredentialsArn))
		if err != nil {
			return err
		}

		if err := checkRouteKeyFree(ad, orDefault(deref(in.RouteKey), defaultKey), ""); err != nil {
			return err
		}

		if in.Target != nil {
			m.quickCreate(ad, *in.Target, deref(in.RouteKey), deref(in.CredentialsArn))
		}

		return nil
	}

	if err := applyQuickCreate(ad, ig, rt, in); err != nil {
		return err
	}

	m.autoDeploy(ad)

	return nil
}

// applyQuickCreate validates and applies the quick-create updates to the
// managed integration and route. ad must be held for writing.
func applyQuickCreate(ad *apiData, ig *driver.Integration, rt *driver.Route, in *driver.UpdateAPIInput) error {
	var qt quickTarget

	if in.Target != nil {
		var err error
		if qt, err = resolveQuickTarget(*in.Target); err != nil {
			return err
		}
	}

	if in.RouteKey != nil {
		if err := validateRouteKey(ad.api.ProtocolType, *in.RouteKey); err != nil {
			return err
		}

		if err := checkRouteKeyFree(ad, *in.RouteKey, rt.RouteID); err != nil {
			return err
		}

		rt.RouteKey = *in.RouteKey
	}

	if in.Target != nil {
		ig.IntegrationType, ig.IntegrationURI = qt.integrationType, *in.Target
		ig.IntegrationMethod, ig.PayloadFormatVersion = qt.method, qt.payloadFormat
	}

	setString(&ig.CredentialsArn, in.CredentialsArn)

	return nil
}

// deref returns *s, or "" for a nil pointer.
func deref(s *string) string {
	if s == nil {
		return ""
	}

	return *s
}
