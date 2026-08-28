package apigateway

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/apigateway/driver"
)

// Data-plane HTTP statuses API Gateway itself returns (before/without reaching a
// backend).
const (
	statusForbidden = 403
	statusBadGway   = 502
)

// resolvedRoute is the small, lock-free snapshot InvokeRoute pulls out from
// under the API lock so the (possibly slow, re-entrant) Lambda call runs
// without holding it.
type resolvedRoute struct {
	resourceID     string
	resourcePath   string
	integration    driver.Integration
	pathParameters map[string]string
	apiID          string
}

// InvokeRoute resolves req against the deployed stage's resource tree and, for
// an AWS_PROXY/AWS Lambda integration, invokes the target function and maps its
// response. Data-plane failures (unknown API/stage/route, missing backend,
// malformed function response) are returned as ordinary HTTP responses — the
// shape real API Gateway returns — not as Go errors.
func (m *Mock) InvokeRoute(ctx context.Context, req *driver.ProxyRequest) (*driver.ProxyResponse, error) {
	route, ok := m.resolve(req)
	if !ok {
		return forbiddenMissingToken(), nil
	}

	if !isLambdaProxy(route.integration.Type) {
		return jsonResponse(statusBadGway, `{"message": "Internal server error"}`), nil
	}

	if m.lambda == nil {
		// Nil-safe: no Lambda backend wired (library-only construction). A Lambda
		// integration whose backend is unreachable is a 502 in real API Gateway.
		return jsonResponse(statusBadGway, `{"message": "Internal server error"}`), nil
	}

	event, err := buildProxyEvent(req, &route, m.opts.AccountID)
	if err != nil {
		return jsonResponse(statusBadGway, `{"message": "Internal server error"}`), nil
	}

	target := extractLambdaTarget(route.integration.URI)

	out, fnErr, invErr := m.lambda.InvokeSync(ctx, target, event)
	if invErr != nil || fnErr != "" {
		return jsonResponse(statusBadGway, `{"message": "Internal server error"}`), nil
	}

	return mapLambdaResponse(out), nil
}

// resolve locks the API, resolves the stage and route, and returns a snapshot.
func (m *Mock) resolve(req *driver.ProxyRequest) (resolvedRoute, bool) {
	ad, err := m.getAPI(req.RestAPIID)
	if err != nil {
		return resolvedRoute{}, false
	}

	ad.mu.RLock()
	defer ad.mu.RUnlock()

	if _, ok := ad.stages[req.StageName]; !ok {
		return resolvedRoute{}, false
	}

	match, ok := matchRoute(ad.resources, req.HTTPMethod, req.Path)
	if !ok || match.method.Integration == nil {
		return resolvedRoute{}, false
	}

	return resolvedRoute{
		resourceID:     match.resource.ID,
		resourcePath:   match.resource.Path,
		integration:    *match.method.Integration,
		pathParameters: match.pathParameters,
		apiID:          req.RestAPIID,
	}, true
}

func isLambdaProxy(t string) bool {
	return t == driver.IntegrationAWSProxy || t == driver.IntegrationAWS
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

// forbiddenMissingToken is the response API Gateway returns for a request that
// matches no route (or an unknown stage/API).
func forbiddenMissingToken() *driver.ProxyResponse {
	return jsonResponse(statusForbidden, `{"message":"Missing Authentication Token"}`)
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
func mapLambdaResponse(out []byte) *driver.ProxyResponse {
	var lr struct {
		StatusCode        int                 `json:"statusCode"`
		Headers           map[string]string   `json:"headers"`
		MultiValueHeaders map[string][]string `json:"multiValueHeaders"`
		Body              string              `json:"body"`
		IsBase64Encoded   bool                `json:"isBase64Encoded"`
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
			Identity:     proxyIdentity{SourceIP: req.SourceIP},
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
	ResourceID   string        `json:"resourceId"`
	ResourcePath string        `json:"resourcePath"`
	HTTPMethod   string        `json:"httpMethod"`
	Path         string        `json:"path"`
	AccountID    string        `json:"accountId"`
	APIID        string        `json:"apiId"`
	Stage        string        `json:"stage"`
	RequestID    string        `json:"requestId"`
	DomainName   string        `json:"domainName"`
	Protocol     string        `json:"protocol"`
	Identity     proxyIdentity `json:"identity"`
}

type proxyIdentity struct {
	SourceIP string `json:"sourceIp"`
}
