package apigateway

import (
	"context"
	"net/url"
	"strconv"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/apigateway/driver"
)

var _ driver.TestInvoker = (*Mock)(nil)

// testStage is the stage name a test invocation reports.
const testStage = "test-invoke-stage"

// TestInvokeMethod runs a method against the live (undeployed) configuration:
// the integration is called and its mapped response returned. Authorizers, API
// keys and request validators are not applied, as in API Gateway's test console.
func (m *Mock) TestInvokeMethod(ctx context.Context, in *driver.TestInvokeMethodInput) (*driver.TestInvokeMethodOutput, error) {
	ad, err := m.getAPI(in.RestAPIID)
	if err != nil {
		return nil, err
	}

	route, err := m.liveRoute(ad, in)
	if err != nil {
		return nil, err
	}

	req := testRequest(in, route)
	route.pathParameters = pathParamsFor(route.resourcePath, req.Path)

	start := m.opts.Clock.Now()
	resp, _ := m.runIntegration(ctx, req, route, idgen.UUID(), nil)
	latency := m.opts.Clock.Since(start)

	return &driver.TestInvokeMethodOutput{
		Status: resp.StatusCode, Body: resp.Body, Headers: resp.Headers, MultiValueHeaders: resp.MultiValueHeaders,
		Log:           "Execution log for request " + idgen.UUID() + "\nMethod completed with status: " + strconv.Itoa(resp.StatusCode) + "\n",
		LatencyMillis: latency.Milliseconds(),
	}, nil
}

// liveRoute builds a route from the live resource tree for a test invocation.
func (m *Mock) liveRoute(ad *apiData, in *driver.TestInvokeMethodInput) (*resolvedRoute, error) {
	ad.mu.RLock()
	defer ad.mu.RUnlock()

	res, ok := ad.resources[in.ResourceID]
	if !ok {
		return nil, cerrors.New(cerrors.NotFound, msgResourceNotFound)
	}

	mth, ok := res.Methods[normalizeMethod(in.HTTPMethod)]
	if !ok {
		return nil, cerrors.New(cerrors.NotFound, msgMethodNotFound)
	}

	if mth.Integration == nil {
		return nil, cerrors.New(cerrors.InvalidArgument, msgNoIntegration)
	}

	method := copyMethod(mth)
	route := &resolvedRoute{
		resourceID: res.ID, resourcePath: res.Path, method: method, integration: *method.Integration,
		stageVariables: copyStrMap(in.StageVariables), apiID: in.RestAPIID, apiName: apiMetricName(&ad.api),
		apiKeySource: ad.api.APIKeySource, ad: ad, stage: driver.Stage{StageName: testStage, Variables: copyStrMap(in.StageVariables)},
		overrides: copyGatewayOverrides(ad.gwResponses),
	}
	m.attachMethodContext(ad, route)

	return route, nil
}

// testRequest assembles the data-plane request a test invocation represents.
func testRequest(in *driver.TestInvokeMethodInput, route *resolvedRoute) *driver.ProxyRequest {
	path, rawQuery, _ := strings.Cut(in.PathWithQuery, "?")
	if path == "" {
		path = route.resourcePath
	}

	q, _ := url.ParseQuery(rawQuery)

	return &driver.ProxyRequest{
		RestAPIID: in.RestAPIID, StageName: testStage, HTTPMethod: in.HTTPMethod, Path: normalizeSlash(path),
		Headers: copyStrMap(in.Headers), MultiValueHeaders: in.MultiValueHeaders, Body: in.Body,
		Query: firstQuery(q), MultiValueQuery: map[string][]string(q), SourceIP: "test-invoke-source-ip",
		Host: "test-invoke-host", Protocol: "HTTP/1.1",
	}
}

func normalizeSlash(p string) string {
	if !strings.HasPrefix(p, "/") {
		return "/" + p
	}

	return p
}

func firstQuery(q url.Values) map[string]string {
	if len(q) == 0 {
		return nil
	}

	out := make(map[string]string, len(q))

	for k, v := range q {
		if len(v) > 0 {
			out[k] = v[0]
		}
	}

	return out
}

// pathParamsFor extracts the {param} values of a resource path template from a
// concrete request path.
func pathParamsFor(template, path string) map[string]string {
	params, _, ok := matchTemplate(splitPath(template), splitPath(path))
	if !ok {
		return nil
	}

	return params
}

// TestInvokeAuthorizer runs an authorizer against a sample request without
// deploying: the Lambda authorizer is invoked and its policy returned, or a
// Cognito token validated.
func (m *Mock) TestInvokeAuthorizer(
	ctx context.Context, in *driver.TestInvokeAuthorizerInput,
) (*driver.TestInvokeAuthorizerOutput, error) {
	ad, err := m.getAPI(in.RestAPIID)
	if err != nil {
		return nil, err
	}

	ad.mu.RLock()
	az, ok := ad.authorizers[in.AuthorizerID]

	var azCopy driver.Authorizer
	if ok {
		azCopy = copyAuthorizer(az)
	}

	ad.mu.RUnlock()

	if !ok {
		return nil, cerrors.New(cerrors.NotFound, msgAuthorizerNotFound)
	}

	route := &resolvedRoute{
		apiID: in.RestAPIID, authorizer: &azCopy, stageVariables: copyStrMap(in.StageVariables), ad: ad,
		stage: driver.Stage{StageName: testStage},
	}
	path, rawQuery, _ := strings.Cut(in.PathWithQuery, "?")
	q, _ := url.ParseQuery(rawQuery)
	req := &driver.ProxyRequest{
		RestAPIID: in.RestAPIID, StageName: testStage, HTTPMethod: "GET", Path: normalizeSlash(path),
		Headers: copyStrMap(in.Headers), MultiValueHeaders: in.MultiValueHeaders, Body: in.Body,
		Query: firstQuery(q), MultiValueQuery: map[string][]string(q), SourceIP: "test-invoke-source-ip",
	}

	idents, ok := identityValues(azCopy.IdentitySource, req, route)
	if !ok && !needsNoIdentity(&azCopy) {
		return nil, cerrors.New(cerrors.InvalidArgument, "The authorizer's identity source values are missing from the request")
	}

	return m.testAuthorizer(ctx, req, route, idents)
}

func (m *Mock) testAuthorizer(
	ctx context.Context, req *driver.ProxyRequest, route *resolvedRoute, idents []string,
) (*driver.TestInvokeAuthorizerOutput, error) {
	start := m.opts.Clock.Now()
	out := &driver.TestInvokeAuthorizerOutput{ClientStatus: statusOK, Authorization: map[string][]string{}}

	for _, src := range splitList(route.authorizer.IdentitySource) {
		if i := strings.LastIndex(src, "."); i >= 0 {
			out.Authorization[src[i+1:]] = []string{identityValue(src, req, route)}
		}
	}

	if route.authorizer.Type == driver.AuthorizerCognito {
		claims, ok := jwtClaims(strings.TrimPrefix(idents[0], "Bearer "))
		if !ok || !claimsValid(claims, route, m.opts.Clock.Now()) {
			out.ClientStatus = statusUnauthorized
		} else {
			out.Claims = claimStrings(claims)
		}
	} else {
		dec, kind := m.callAuthorizer(ctx, req, route, idents)
		if kind != authOK {
			return nil, cerrors.New(cerrors.InvalidArgument, "The authorizer function failed or returned an invalid policy")
		}

		out.Policy, out.PrincipalID = dec.policy, dec.principalID
	}

	out.LatencyMillis = m.opts.Clock.Since(start).Milliseconds()
	out.Log = "Execution log for authorizer " + route.authorizer.ID

	return out, nil
}
