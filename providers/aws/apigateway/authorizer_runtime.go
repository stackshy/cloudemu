package apigateway

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/services/apigateway/driver"
)

// authKind classifies why an authorizer run did not produce a decision.
type authKind int

const (
	authOK authKind = iota
	authUnauthorized
	authConfig
	authFailure
)

// authDecision is the outcome of one authorizer run, cached per identity.
type authDecision struct {
	allowed     bool
	principalID string
	context     map[string]any
	usageKey    string
	policy      string
	expires     time.Time
}

const (
	msgForbiddenDeny     = "User is not authorized to access this resource with an explicit deny"
	msgForbiddenImplicit = "User is not authorized to access this resource"
	msgInternal          = "Internal server error"
	msgUnauthorized      = "Unauthorized"
	statusUnauthorized   = 401
)

// authorize runs the method's authorizer: a Lambda authorizer for CUSTOM, a
// Cognito token check for COGNITO_USER_POOLS. AWS_IAM and NONE pass.
func (m *Mock) authorize(ctx context.Context, req *driver.ProxyRequest, route *resolvedRoute, reqID string) *driver.ProxyResponse {
	switch route.method.AuthorizationType {
	case authTypeCustom:
		if route.authorizer == nil || route.authorizer.Type == driver.AuthorizerCognito {
			return m.gatewayResponse(route, req, reqID, respAuthorizerCfg, msgInternal)
		}

		return m.runLambdaAuthorizer(ctx, req, route, reqID)
	case authTypeCognito:
		if route.authorizer == nil || route.authorizer.Type != driver.AuthorizerCognito {
			return m.gatewayResponse(route, req, reqID, respAuthorizerCfg, msgInternal)
		}

		return m.runCognitoAuthorizer(req, route, reqID)
	default:
		return nil
	}
}

// identityValues reads each source of an identity source expression
// (method.request.header.X, method.request.querystring.X, context.X,
// stageVariables.X). All must be present and non-empty.
func identityValues(source string, req *driver.ProxyRequest, route *resolvedRoute) ([]string, bool) {
	sources := splitList(source)
	out := make([]string, 0, len(sources))

	for _, src := range sources {
		v := identityValue(src, req, route)
		if v == "" {
			return nil, false
		}

		out = append(out, v)
	}

	return out, len(out) > 0
}

func identityValue(src string, req *driver.ProxyRequest, route *resolvedRoute) string {
	switch {
	case strings.HasPrefix(src, "method.request.header."):
		return headerValue(req.Headers, strings.TrimPrefix(src, "method.request.header."))
	case strings.HasPrefix(src, "method.request.querystring."):
		return req.Query[strings.TrimPrefix(src, "method.request.querystring.")]
	case strings.HasPrefix(src, "stageVariables."):
		return route.stageVariables[strings.TrimPrefix(src, "stageVariables.")]
	case src == "context.stage":
		return req.StageName
	case src == "context.httpMethod":
		return req.HTTPMethod
	case src == "context.path":
		return "/" + req.StageName + req.Path
	default:
		return ""
	}
}

// runLambdaAuthorizer evaluates a TOKEN or REQUEST authorizer, reusing a cached
// decision for the same identity while its TTL lasts.
func (m *Mock) runLambdaAuthorizer(
	ctx context.Context, req *driver.ProxyRequest, route *resolvedRoute, reqID string,
) *driver.ProxyResponse {
	az := route.authorizer

	idents, ok := identityValues(az.IdentitySource, req, route)
	if !ok {
		return m.gatewayResponse(route, req, reqID, respUnauthorized, msgUnauthorized)
	}

	if az.Type == driver.AuthorizerToken && !tokenMatches(az.IdentityValidationExpression, idents[0]) {
		return m.gatewayResponse(route, req, reqID, respUnauthorized, msgUnauthorized)
	}

	dec, kind := m.cachedOrRunAuthorizer(ctx, req, route, idents)

	switch kind {
	case authOK:
		// A decision was made; the policy is evaluated below.
	case authUnauthorized:
		return m.gatewayResponse(route, req, reqID, respUnauthorized, msgUnauthorized)
	case authConfig:
		return m.gatewayResponse(route, req, reqID, respAuthorizerCfg, msgInternal)
	case authFailure:
		return m.gatewayResponse(route, req, reqID, respAuthorizerFail, msgInternal)
	}

	if !dec.allowed {
		return m.gatewayResponse(route, req, reqID, respAccessDenied, msgForbiddenDeny)
	}

	route.principalID, route.authContext, route.usageKey = dec.principalID, dec.context, dec.usageKey

	return nil
}

func tokenMatches(expr, token string) bool {
	if expr == "" {
		return true
	}

	re, err := regexp.Compile(expr)

	return err == nil && re.MatchString(token)
}

// cachedOrRunAuthorizer returns a live cached decision or runs the authorizer
// and caches the result for the authorizer's TTL.
func (m *Mock) cachedOrRunAuthorizer(
	ctx context.Context, req *driver.ProxyRequest, route *resolvedRoute, idents []string,
) (authDecision, authKind) {
	az := route.authorizer
	key := az.ID + "|" + strings.Join(idents, "|")
	now := m.opts.Clock.Now()

	route.ad.mu.Lock()
	cached, hit := route.ad.authCache[key]
	route.ad.mu.Unlock()

	if hit && now.Before(cached.expires) {
		return cached, authOK
	}

	dec, kind := m.callAuthorizer(ctx, req, route, idents)
	if kind != authOK {
		return dec, kind
	}

	if ttl := authorizerTTL(az); ttl > 0 {
		dec.expires = now.Add(time.Duration(ttl) * time.Second)

		route.ad.mu.Lock()
		route.ad.authCache[key] = dec
		route.ad.mu.Unlock()
	}

	return dec, authOK
}

func authorizerTTL(az *driver.Authorizer) int {
	if az.AuthorizerResultTTLInSeconds == nil {
		return 0
	}

	return *az.AuthorizerResultTTLInSeconds
}

// methodARN is the execute-api ARN of the invoked method, the resource a
// policy statement is matched against.
func (m *Mock) methodARN(req *driver.ProxyRequest) string {
	return fmt.Sprintf("arn:aws:execute-api:%s:%s:%s/%s/%s/%s", m.opts.Region, m.opts.AccountID,
		req.RestAPIID, req.StageName, req.HTTPMethod, strings.TrimPrefix(req.Path, "/"))
}

// callAuthorizer invokes the authorizer function and parses its policy.
func (m *Mock) callAuthorizer(
	ctx context.Context, req *driver.ProxyRequest, route *resolvedRoute, idents []string,
) (authDecision, authKind) {
	if m.lambda == nil {
		return authDecision{}, authConfig
	}

	uri, missing := expandStageVariables(route.authorizer.AuthorizerURI, route.stageVariables)
	if missing != "" {
		return authDecision{}, authConfig
	}

	payload, err := m.authorizerEvent(req, route, idents)
	if err != nil {
		return authDecision{}, authConfig
	}

	out, fnErr, invErr := m.lambda.InvokeSync(ctx, extractLambdaTarget(uri), payload)
	if invErr != nil {
		return authDecision{}, authFailure
	}

	if fnErr != "" {
		if lambdaErrorMessage(out) == msgUnauthorized {
			return authDecision{}, authUnauthorized
		}

		return authDecision{}, authFailure
	}

	dec, ok := parsePolicy(out, m.methodARN(req))
	if !ok {
		return authDecision{}, authConfig
	}

	return dec, authOK
}

func lambdaErrorMessage(out []byte) string {
	var e struct {
		ErrorMessage string `json:"errorMessage"`
	}

	_ = json.Unmarshal(out, &e)

	return e.ErrorMessage
}

// authorizerEvent builds the TOKEN or REQUEST authorizer event.
func (m *Mock) authorizerEvent(req *driver.ProxyRequest, route *resolvedRoute, idents []string) ([]byte, error) {
	arn := m.methodARN(req)

	if route.authorizer.Type == driver.AuthorizerToken {
		return json.Marshal(map[string]string{"type": "TOKEN", "authorizationToken": idents[0], "methodArn": arn})
	}

	return json.Marshal(map[string]any{
		"type": "REQUEST", "methodArn": arn, "resource": route.resourcePath, "path": req.Path,
		"httpMethod": req.HTTPMethod, "headers": req.Headers, "multiValueHeaders": req.MultiValueHeaders,
		"queryStringParameters": emptyToNil(req.Query), "multiValueQueryStringParameters": req.MultiValueQuery,
		"pathParameters": emptyToNil(route.pathParameters), "stageVariables": emptyToNil(route.stageVariables),
		"requestContext": map[string]any{
			"accountId": m.opts.AccountID, "apiId": route.apiID, "stage": req.StageName,
			"httpMethod": req.HTTPMethod, "path": "/" + req.StageName + req.Path, "resourcePath": route.resourcePath,
			"identity": map[string]string{"sourceIp": req.SourceIP},
		},
	})
}

// policyResponse is the authorizer function's output.
type policyResponse struct {
	PrincipalID    string         `json:"principalId"`
	PolicyDocument *policyDoc     `json:"policyDocument"`
	Context        map[string]any `json:"context"`
	UsageKey       string         `json:"usageIdentifierKey"`
}

type policyDoc struct {
	Statement []policyStatement `json:"-"`
	Raw       json.RawMessage   `json:"-"`
}

type policyStatement struct {
	Effect   string
	Action   []string
	Resource []string
}

// UnmarshalJSON accepts Statement as an object or an array, and Action/Resource
// as a string or an array.
func (p *policyDoc) UnmarshalJSON(b []byte) error {
	p.Raw = append(json.RawMessage(nil), b...)

	var doc struct {
		Statement json.RawMessage `json:"Statement"`
	}

	if err := json.Unmarshal(b, &doc); err != nil {
		return err
	}

	var raws []struct {
		Effect   string          `json:"Effect"`
		Action   json.RawMessage `json:"Action"`
		Resource json.RawMessage `json:"Resource"`
	}

	if err := json.Unmarshal(doc.Statement, &raws); err != nil {
		var one struct {
			Effect   string          `json:"Effect"`
			Action   json.RawMessage `json:"Action"`
			Resource json.RawMessage `json:"Resource"`
		}

		if err := json.Unmarshal(doc.Statement, &one); err != nil {
			return err
		}

		raws = append(raws, one)
	}

	for _, r := range raws {
		p.Statement = append(p.Statement, policyStatement{
			Effect: r.Effect, Action: stringOrList(r.Action), Resource: stringOrList(r.Resource),
		})
	}

	return nil
}

func stringOrList(raw json.RawMessage) []string {
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return []string{one}
	}

	var many []string
	_ = json.Unmarshal(raw, &many)

	return many
}

// parsePolicy reads an authorizer output and evaluates its policy against the
// method ARN: an explicit Deny wins, then an Allow, otherwise the request is
// implicitly denied. ok is false when the output is not a policy at all.
func parsePolicy(out []byte, methodARN string) (authDecision, bool) {
	var pr policyResponse
	if err := json.Unmarshal(out, &pr); err != nil || pr.PolicyDocument == nil || pr.PrincipalID == "" {
		return authDecision{}, false
	}

	dec := authDecision{
		principalID: pr.PrincipalID, context: pr.Context, usageKey: pr.UsageKey, policy: string(pr.PolicyDocument.Raw),
	}

	allowed := false

	for _, st := range pr.PolicyDocument.Statement {
		if !statementMatches(&st, methodARN) {
			continue
		}

		switch st.Effect {
		case "Deny":
			return dec, true
		case "Allow":
			allowed = true
		}
	}

	dec.allowed = allowed

	return dec, true
}

func statementMatches(st *policyStatement, arn string) bool {
	actionOK := false

	for _, a := range st.Action {
		if globMatch(a, "execute-api:Invoke") {
			actionOK = true
		}
	}

	if !actionOK {
		return false
	}

	for _, r := range st.Resource {
		if globMatch(r, arn) {
			return true
		}
	}

	return false
}

// globMatch matches s against a pattern where "*" is any run and "?" any one
// character.
func globMatch(pattern, s string) bool {
	re := "^" + strings.NewReplacer(`\*`, ".*", `\?`, ".").Replace(regexp.QuoteMeta(pattern)) + "$"

	ok, err := regexp.MatchString(re, s)

	return err == nil && ok
}

// runCognitoAuthorizer checks a Cognito user pool token: it must be a JWT that
// has not expired, issued by one of the authorizer's pools. When the method
// names scopes the token must carry one of them. The signature is not verified
// (the emulator has no pool signing keys).
func (m *Mock) runCognitoAuthorizer(req *driver.ProxyRequest, route *resolvedRoute, reqID string) *driver.ProxyResponse {
	deny := func() *driver.ProxyResponse {
		return m.gatewayResponse(route, req, reqID, respUnauthorized, msgUnauthorized)
	}

	idents, ok := identityValues(route.authorizer.IdentitySource, req, route)
	if !ok {
		return deny()
	}

	claims, ok := jwtClaims(strings.TrimPrefix(idents[0], "Bearer "))
	if !ok || !claimsValid(claims, route, m.opts.Clock.Now()) {
		return deny()
	}

	if scopes := route.method.AuthorizationScopes; len(scopes) > 0 && !hasScope(claims, scopes) {
		return deny()
	}

	route.authContext = map[string]any{"claims": claimStrings(claims)}

	return nil
}

func jwtClaims(token string) (map[string]any, bool) {
	const parts = 3

	segs := strings.Split(token, ".")
	if len(segs) != parts {
		return nil, false
	}

	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(segs[1], "="))
	if err != nil {
		return nil, false
	}

	var claims map[string]any
	if err := json.Unmarshal(raw, &claims); err != nil {
		return nil, false
	}

	return claims, true
}

// claimsValid checks expiry and that the issuer is one of the authorizer's pools.
func claimsValid(claims map[string]any, route *resolvedRoute, now time.Time) bool {
	if exp, ok := claims["exp"].(float64); ok && now.Unix() >= int64(exp) {
		return false
	}

	iss, _ := claims["iss"].(string)

	for _, arn := range route.authorizer.ProviderARNs {
		if i := strings.LastIndex(arn, "userpool/"); i >= 0 && strings.HasSuffix(iss, "/"+arn[i+len("userpool/"):]) {
			return true
		}
	}

	return false
}

func hasScope(claims map[string]any, want []string) bool {
	have, _ := claims["scope"].(string)
	set := strings.Fields(have)

	for _, w := range want {
		for _, h := range set {
			if h == w {
				return true
			}
		}
	}

	return false
}

func claimStrings(claims map[string]any) map[string]string {
	out := make(map[string]string, len(claims))

	for k, v := range claims {
		switch t := v.(type) {
		case string:
			out[k] = t
		default:
			b, _ := json.Marshal(t)
			out[k] = string(b)
		}
	}

	return out
}
