package apigatewayv2

import (
	"regexp"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/apigatewayv2/driver"
)

// Well-known keys and expressions.
const (
	defaultKey            = "$default"
	integrationsPrefix    = "integrations/"
	usageIdentifierKeyExp = "$context.authorizer.usageIdentifierKey"
	httpRouteSelectionAlt = "${request.method} ${request.path}"
	apiKeyHeaderAlt       = "${request.header.x-api-key}"
	usageIdentifierAlt    = "${context.authorizer.usageIdentifierKey}"
	payloadFormatV2       = "2.0"
)

// Length limits from the apigatewayv2 model.
const (
	maxNameLen        = 128
	maxDescriptionLen = 1024
	minTimeoutMillis  = 50
)

// Tag limits.
const (
	maxTags        = 50
	maxTagKeyLen   = 128
	maxTagValueLen = 256
	reservedTagPfx = "aws:"
)

// stageNameRe is the character set a stage name may use; "$default" is the
// only other accepted value.
var stageNameRe = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// httpRouteMethods are the methods an HTTP API route key may start with.
//
//nolint:gochecknoglobals // read-only lookup table
var httpRouteMethods = map[string]bool{
	"ANY": true, "DELETE": true, "GET": true, "HEAD": true,
	"OPTIONS": true, "PATCH": true, "POST": true, "PUT": true,
}

// integrationTypes is every integration type the service knows.
//
//nolint:gochecknoglobals // read-only lookup table
var integrationTypes = map[string]bool{
	driver.IntegrationAWS: true, driver.IntegrationAWSProxy: true,
	driver.IntegrationHTTP: true, driver.IntegrationHTTPProxy: true, driver.IntegrationMock: true,
}

// authorizationTypes lists the authorization types each protocol accepts.
//
//nolint:gochecknoglobals // read-only lookup table
var authorizationTypes = map[string]map[string]bool{
	driver.ProtocolHTTP:      {"NONE": true, "AWS_IAM": true, "CUSTOM": true, "JWT": true},
	driver.ProtocolWebSocket: {"NONE": true, "AWS_IAM": true, "CUSTOM": true},
}

func badRequest(format string, args ...any) error {
	return cerrors.Newf(cerrors.InvalidArgument, format, args...)
}

// validateAPIFields checks the name, description and selection expressions an
// API ends up with after a create or update.
func validateAPIFields(a *driver.API) error {
	if a.Name == "" || len(a.Name) > maxNameLen {
		return badRequest("Name must be between 1 and %d characters", maxNameLen)
	}

	if len(a.Description) > maxDescriptionLen {
		return badRequest("Description must be at most %d characters", maxDescriptionLen)
	}

	switch a.APIKeySelectionExpression {
	case defaultAPIKeySelectionExpr, apiKeyHeaderAlt, usageIdentifierKeyExp, usageIdentifierAlt:
	default:
		return badRequest("Invalid API key selection expression specified: %s", a.APIKeySelectionExpression)
	}

	if a.ProtocolType == driver.ProtocolWebSocket && a.CorsConfiguration != nil {
		return badRequest("CORS configuration is not supported for WEBSOCKET protocol")
	}

	return validateRouteSelection(a.ProtocolType, a.RouteSelectionExpression)
}

// validateRouteSelection enforces the route selection expression rules: HTTP
// APIs accept only the method-and-path expression (with or without braces),
// and WebSocket APIs need a $request expression.
func validateRouteSelection(protocol, expr string) error {
	if protocol == driver.ProtocolHTTP {
		if expr != defaultRouteSelectionExpr && expr != httpRouteSelectionAlt {
			return badRequest("Only %s is supported for HTTP APIs", defaultRouteSelectionExpr)
		}

		return nil
	}

	if !strings.HasPrefix(expr, "$request.") && !strings.HasPrefix(expr, "${request.") {
		return badRequest("Invalid route selection expression specified: %s", expr)
	}

	return nil
}

// validateRouteKey checks a route key against its API's protocol. HTTP APIs
// take "$default" or "METHOD /path"; WebSocket APIs take any non-empty key.
func validateRouteKey(protocol, key string) error {
	if key == "" {
		return badRequest("RouteKey is required")
	}

	if protocol != driver.ProtocolHTTP || key == defaultKey {
		return nil
	}

	method, path, ok := strings.Cut(key, " ")
	if !ok || !httpRouteMethods[method] || !strings.HasPrefix(path, "/") || strings.ContainsAny(path, " \t") {
		return badRequest(`The provided route key is not formatted properly for HTTP protocol. ` +
			`Format should be "<HTTP METHOD> /<RESOURCE PATH>" or "$default"`)
	}

	return nil
}

// validateRouteTarget checks that a route target names an integration on the
// API. ad must be held.
func validateRouteTarget(ad *apiData, target string) error {
	if target == "" {
		return nil
	}

	id, ok := strings.CutPrefix(target, integrationsPrefix)
	if !ok {
		return badRequest("Invalid Integration identifier specified")
	}

	if _, ok := ad.integrations[id]; !ok {
		return badRequest("Invalid Integration identifier specified")
	}

	return nil
}

// validateAuthorizationType checks a route's authorization type against its
// API's protocol.
func validateAuthorizationType(protocol, authType string) error {
	if !authorizationTypes[protocol][authType] {
		return badRequest("Invalid authorization type specified: %s", authType)
	}

	return nil
}

// checkRouteKeyFree returns a Conflict error when another route on the API
// already uses key. ad must be held.
func checkRouteKeyFree(ad *apiData, key, selfID string) error {
	for id, rt := range ad.routes {
		if id != selfID && rt.RouteKey == key {
			return cerrors.Newf(cerrors.AlreadyExists, "Route with key %s already exists for this API", key)
		}
	}

	return nil
}

// validateIntegration checks an integration's type, payload format version
// and timeout against its API's protocol.
func validateIntegration(protocol string, ig *driver.Integration) error {
	if ig.IntegrationType == "" {
		return badRequest("IntegrationType is required")
	}

	if !integrationTypes[ig.IntegrationType] {
		return badRequest("Invalid integration type specified: %s", ig.IntegrationType)
	}

	if len(ig.Description) > maxDescriptionLen {
		return badRequest("Description must be at most %d characters", maxDescriptionLen)
	}

	if err := validatePayloadFormat(protocol, ig); err != nil {
		return err
	}

	maxTimeout := defaultHTTPTimeoutMillis
	if protocol == driver.ProtocolWebSocket {
		maxTimeout = defaultWebSocketTimeoutMillis
	}

	if ig.TimeoutInMillis < minTimeoutMillis || ig.TimeoutInMillis > maxTimeout {
		return badRequest("TimeoutInMillis must be between %d and %d", minTimeoutMillis, maxTimeout)
	}

	return nil
}

// validatePayloadFormat applies the per-protocol payload format rules: HTTP
// APIs support only the two proxy types and 2.0 only on AWS_PROXY; WebSocket
// APIs support only 1.0.
func validatePayloadFormat(protocol string, ig *driver.Integration) error {
	v := ig.PayloadFormatVersion
	if v != defaultPayloadFormat && v != payloadFormatV2 {
		return badRequest("Invalid payload format version specified: %s", v)
	}

	if protocol == driver.ProtocolWebSocket {
		if v != defaultPayloadFormat {
			return badRequest("Payload format version %s is not supported for WEBSOCKET APIs", v)
		}

		return nil
	}

	if ig.IntegrationType != driver.IntegrationAWSProxy && ig.IntegrationType != driver.IntegrationHTTPProxy {
		return badRequest("Integration type %s is not supported for HTTP APIs", ig.IntegrationType)
	}

	if v == payloadFormatV2 && ig.IntegrationType != driver.IntegrationAWSProxy {
		return badRequest("Payload format version 2.0 is only supported for AWS_PROXY integrations")
	}

	return nil
}

// validateStageName accepts "$default" or a name drawn from a-zA-Z0-9._-.
func validateStageName(name string) error {
	if name == "" {
		return badRequest("StageName is required")
	}

	if name != defaultKey && (len(name) > maxNameLen || !stageNameRe.MatchString(name)) {
		return badRequest("Stage name only allows a-zA-Z0-9_- or $default")
	}

	return nil
}

// validateTags applies the tag count, length and reserved-prefix rules.
func validateTags(tags map[string]string) error {
	if len(tags) > maxTags {
		return badRequest("A resource can have at most %d tags", maxTags)
	}

	for k, v := range tags {
		if strings.HasPrefix(strings.ToLower(k), reservedTagPfx) {
			return badRequest("Tag keys cannot start with the reserved prefix %s", reservedTagPfx)
		}

		if k == "" || len(k) > maxTagKeyLen || len(v) > maxTagValueLen {
			return badRequest("Tag keys must be 1 to %d characters and values at most %d", maxTagKeyLen, maxTagValueLen)
		}
	}

	return nil
}
