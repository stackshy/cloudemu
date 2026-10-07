package apigateway

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/apigateway/driver"
)

// Data-plane HTTP statuses API Gateway itself returns (before/without reaching a
// backend).
const (
	statusForbidden = 403
	statusBadGway   = 502
)

// noIntegration is the integration latency reported for a request that never
// reached a backend.
const noIntegration = -1

// resolvedRoute is the small, lock-free snapshot InvokeRoute pulls out from
// under the API lock so the (possibly slow, re-entrant) backend call runs
// without holding it.
type resolvedRoute struct {
	resourceID     string
	resourcePath   string
	method         driver.Method
	integration    driver.Integration
	pathParameters map[string]string
	stageVariables map[string]string
	apiID          string

	// Request-time context captured with the route.
	apiName      string
	apiKeySource string
	stage        driver.Stage
	authorizer   *driver.Authorizer
	validator    *driver.RequestValidator
	schemas      map[string]string // content type -> request model schema
	models       map[string]string // model name -> schema, for $ref
	overrides    map[string]driver.GatewayResponse
	ad           *apiData

	// Filled in by enforcement and passed to the backend.
	principalID string
	authContext map[string]any
	apiKeyID    string
	usageKey    string
}

// InvokeRoute resolves req against the deployed stage's resource tree and runs
// the method: authorization, API key and throttling, request validation, then
// the integration. Data-plane failures (unknown API/stage/route, denied or
// throttled requests, a missing or failing backend) are returned as ordinary
// HTTP responses, the shape real API Gateway returns, not as Go errors. Every
// request to a deployed stage publishes the AWS/ApiGateway request metrics.
func (m *Mock) InvokeRoute(ctx context.Context, req *driver.ProxyRequest) (*driver.ProxyResponse, error) {
	start := m.opts.Clock.Now()
	reqID := idgen.UUID()

	route, ok := m.resolve(req)
	resp, integration := m.serveResolved(ctx, req, &route, ok, reqID)

	if route.ad != nil {
		latency := m.opts.Clock.Since(start)

		m.emitRequestMetrics(ctx, route.apiName, req.StageName, resp.StatusCode, latency, integration)
		m.emitMethodMetrics(ctx, req, &route, resp.StatusCode, latency, integration)
		m.writeAccessLog(ctx, req, &route, resp, reqID, latency)
	}

	return resp, nil
}

// serveResolved runs a resolved (or unresolved) request and returns the response
// and the backend latency, or noIntegration when no backend was reached.
func (m *Mock) serveResolved(
	ctx context.Context, req *driver.ProxyRequest, route *resolvedRoute, ok bool, reqID string,
) (*driver.ProxyResponse, time.Duration) {
	if !ok {
		return m.gatewayResponse(route, req, reqID, respMissingToken, "Missing Authentication Token"), noIntegration
	}

	lg := m.newExecLog(route, req, reqID)

	if resp := m.enforce(ctx, req, route, reqID); resp != nil {
		lg.finish(ctx, resp.StatusCode)

		return resp, noIntegration
	}

	resp, integration := m.runIntegration(ctx, req, route, reqID, lg)
	lg.finish(ctx, resp.StatusCode)

	return resp, integration
}

// runIntegration invokes the method's backend by integration type.
func (m *Mock) runIntegration(
	ctx context.Context, req *driver.ProxyRequest, route *resolvedRoute, reqID string, lg *execLog,
) (*driver.ProxyResponse, time.Duration) {
	switch route.integration.Type {
	case driver.IntegrationMock:
		return m.serveMock(ctx, req, route), 0
	case driver.IntegrationHTTP, driver.IntegrationHTTPProxy:
		return m.serveHTTP(ctx, req, route, reqID, lg)
	case driver.IntegrationAWSProxy, driver.IntegrationAWS:
		return m.serveLambda(ctx, req, route, reqID, lg)
	default:
		return m.gatewayResponse(route, req, reqID, respAPIConfigError, "Internal server error"), noIntegration
	}
}

// serveLambda invokes a Lambda proxy integration and maps its response.
func (m *Mock) serveLambda(
	ctx context.Context, req *driver.ProxyRequest, route *resolvedRoute, reqID string, lg *execLog,
) (*driver.ProxyResponse, time.Duration) {
	if m.lambda == nil {
		// Nil-safe: no Lambda backend wired (library-only construction). A Lambda
		// integration whose backend is unreachable is a 502 in real API Gateway.
		return jsonResponse(statusBadGway, `{"message": "Internal server error"}`), noIntegration
	}

	uri, missing := expandStageVariables(route.integration.URI, route.stageVariables)
	if missing != "" {
		lg.errorf("Execution failed: stage variable %q is not defined", missing)

		return m.gatewayResponse(route, req, reqID, respAPIConfigError, "Internal server error"), noIntegration
	}

	event, err := buildProxyEvent(req, route, m.opts.AccountID)
	if err != nil {
		return jsonResponse(statusBadGway, `{"message": "Internal server error"}`), noIntegration
	}

	target := extractLambdaTarget(uri)
	lg.infof("Endpoint request URI: %s", uri)

	invokeStart := m.opts.Clock.Now()
	out, fnErr, invErr := m.lambda.InvokeSync(ctx, target, event)
	integration := m.opts.Clock.Since(invokeStart)

	if invErr != nil || fnErr != "" {
		lg.errorf("Execution failed due to configuration error: Lambda invocation failed")

		return jsonResponse(statusBadGway, `{"message": "Internal server error"}`), integration
	}

	return mapLambdaResponse(out, event), integration
}

// resolve locks the API, resolves the stage and the route in the tree the
// stage's deployment captured, and returns a snapshot. Live edits made since
// that deployment are not visible here. ok is false when no route matched; the
// route still carries the API and stage context (when they exist) so the
// failure can use the API's gateway responses.
func (m *Mock) resolve(req *driver.ProxyRequest) (resolvedRoute, bool) {
	ad, err := m.getAPI(req.RestAPIID)
	if err != nil {
		return resolvedRoute{}, false
	}

	ad.mu.RLock()
	defer ad.mu.RUnlock()

	st, ok := ad.stages[req.StageName]
	if !ok {
		return resolvedRoute{}, false
	}

	route := resolvedRoute{
		apiID: req.RestAPIID, apiName: apiMetricName(&ad.api), apiKeySource: ad.api.APIKeySource,
		stage: copyStage(st), stageVariables: copyStrMap(st.Variables), ad: ad,
		overrides: copyGatewayOverrides(ad.gwResponses),
	}

	match, ok := matchRoute(ad.trees[st.DeploymentID], req.HTTPMethod, req.Path)
	if !ok || match.method.Integration == nil {
		return route, false
	}

	method := copyMethod(match.method)
	route.resourceID, route.resourcePath = match.resource.ID, match.resource.Path
	route.method, route.integration = method, *method.Integration
	route.pathParameters = match.pathParameters
	m.attachMethodContext(ad, &route)

	return route, true
}

// apiMetricName is the ApiName metrics dimension: the name, or the id when the
// name has no ASCII.
func apiMetricName(api *driver.RestAPI) string {
	if api.Name == "" {
		return api.ID
	}

	return api.Name
}

// attachMethodContext copies the authorizer, request validator and models a
// method references out from under the API lock.
func (*Mock) attachMethodContext(ad *apiData, route *resolvedRoute) {
	if az, ok := ad.authorizers[route.method.AuthorizerID]; ok {
		cp := copyAuthorizer(az)
		route.authorizer = &cp
	}

	if v, ok := ad.validators[route.method.RequestValidatorID]; ok {
		cp := *v
		route.validator = &cp
	}

	route.models = make(map[string]string, len(ad.models))
	for name, mod := range ad.models {
		route.models[name] = mod.Schema
	}

	route.schemas = make(map[string]string, len(route.method.RequestModels))
	for ct, name := range route.method.RequestModels {
		if schema, ok := route.models[name]; ok {
			route.schemas[ct] = schema
		}
	}
}

func copyGatewayOverrides(in map[string]*driver.GatewayResponse) map[string]driver.GatewayResponse {
	if len(in) == 0 {
		return nil
	}

	out := make(map[string]driver.GatewayResponse, len(in))
	for k, v := range in {
		out[k] = copyGatewayResponse(v)
	}

	return out
}

// extractLambdaTarget pulls the Lambda function ARN (or name) out of an
// integration URI of the form
// arn:aws:apigateway:<region>:lambda:path/2015-03-31/functions/<function-arn>/invocations.
// A URI without those markers is returned unchanged (InvokeSync accepts a bare
// name or ARN).
func extractLambdaTarget(uri string) string {
	const (
		marker = "/functions/"
		suffix = "/invocations"
	)

	i := strings.Index(uri, marker)
	if i < 0 {
		return uri
	}

	rest := uri[i+len(marker):]
	if j := strings.Index(rest, suffix); j >= 0 {
		return rest[:j]
	}

	return rest
}

func jsonResponse(status int, body string) *driver.ProxyResponse {
	return &driver.ProxyResponse{
		StatusCode: status,
		Headers:    map[string]string{"Content-Type": "application/json"},
		Body:       body,
	}
}

// mapLambdaResponse parses a Lambda proxy integration's JSON output
// ({statusCode,headers,body,isBase64Encoded}) into a ProxyResponse. Output that
// is not proxy-shaped (missing statusCode, invalid JSON) is a 502, matching real
// API Gateway's "malformed Lambda proxy response" handling.
func mapLambdaResponse(out, event []byte) *driver.ProxyResponse {
	var lr struct {
		StatusCode        int                 `json:"statusCode"`
		Headers           map[string]string   `json:"headers"`
		MultiValueHeaders map[string][]string `json:"multiValueHeaders"`
		Body              string              `json:"body"`
		IsBase64Encoded   bool                `json:"isBase64Encoded"`
	}

	// The Lambda mock cannot run an uploaded zip: it echoes the request payload.
	// That echo is not a proxy response, so surface it as a 200 JSON body (the
	// stub "ran" the function) rather than a malformed-response 502.
	if bytes.Equal(out, event) {
		return &driver.ProxyResponse{
			StatusCode: statusOK, Headers: map[string]string{headerContentType: contentTypeJSON, headerLambdaStub: "true"},
			Body: string(out),
		}
	}

	if err := json.Unmarshal(out, &lr); err != nil || lr.StatusCode == 0 {
		return jsonResponse(statusBadGway, `{"message": "Internal server error"}`)
	}

	return &driver.ProxyResponse{
		StatusCode:        lr.StatusCode,
		Headers:           lr.Headers,
		MultiValueHeaders: lr.MultiValueHeaders,
		Body:              lr.Body,
		IsBase64Encoded:   lr.IsBase64Encoded,
	}
}

// buildProxyEvent assembles the API Gateway REST proxy (event format 1.0)
// payload passed to a Lambda AWS_PROXY integration.
func buildProxyEvent(req *driver.ProxyRequest, route *resolvedRoute, accountID string) ([]byte, error) {
	event := proxyEvent{
		Resource:                        route.resourcePath,
		Path:                            req.Path,
		HTTPMethod:                      req.HTTPMethod,
		Headers:                         req.Headers,
		MultiValueHeaders:               req.MultiValueHeaders,
		QueryStringParameters:           emptyToNil(req.Query),
		MultiValueQueryStringParameters: req.MultiValueQuery,
		PathParameters:                  emptyToNil(route.pathParameters),
		StageVariables:                  emptyToNil(route.stageVariables),
		Body:                            req.Body,
		IsBase64Encoded:                 req.IsBase64Encoded,
		RequestContext: proxyRequestContext{
			ResourceID:   route.resourceID,
			ResourcePath: route.resourcePath,
			HTTPMethod:   req.HTTPMethod,
			Path:         "/" + req.StageName + req.Path,
			AccountID:    accountID,
			APIID:        route.apiID,
			Stage:        req.StageName,
			RequestID:    idgen.UUID(),
			DomainName:   req.Host,
			Protocol:     orDefault(req.Protocol, "HTTP/1.1"),
			Identity:     proxyIdentity{SourceIP: req.SourceIP, APIKeyID: route.apiKeyID},
			Authorizer:   authorizerContext(route),
		},
	}

	return json.Marshal(event)
}

// emptyToNil returns nil for an empty map so the event omits the field (matching
// AWS, which sends null for absent query/path parameters).
func emptyToNil(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}

	return m
}

type proxyEvent struct {
	Resource                        string              `json:"resource"`
	Path                            string              `json:"path"`
	HTTPMethod                      string              `json:"httpMethod"`
	Headers                         map[string]string   `json:"headers"`
	MultiValueHeaders               map[string][]string `json:"multiValueHeaders"`
	QueryStringParameters           map[string]string   `json:"queryStringParameters"`
	MultiValueQueryStringParameters map[string][]string `json:"multiValueQueryStringParameters"`
	PathParameters                  map[string]string   `json:"pathParameters"`
	StageVariables                  map[string]string   `json:"stageVariables"`
	RequestContext                  proxyRequestContext `json:"requestContext"`
	Body                            string              `json:"body"`
	IsBase64Encoded                 bool                `json:"isBase64Encoded"`
}

type proxyRequestContext struct {
	ResourceID   string         `json:"resourceId"`
	ResourcePath string         `json:"resourcePath"`
	HTTPMethod   string         `json:"httpMethod"`
	Path         string         `json:"path"`
	AccountID    string         `json:"accountId"`
	APIID        string         `json:"apiId"`
	Stage        string         `json:"stage"`
	RequestID    string         `json:"requestId"`
	DomainName   string         `json:"domainName"`
	Protocol     string         `json:"protocol"`
	Identity     proxyIdentity  `json:"identity"`
	Authorizer   map[string]any `json:"authorizer,omitempty"`
}

type proxyIdentity struct {
	SourceIP string `json:"sourceIp"`
	APIKeyID string `json:"apiKeyId,omitempty"`
}
