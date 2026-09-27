package aws

import (
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/stackshy/cloudemu/v2/server"
	apigatewaysrv "github.com/stackshy/cloudemu/v2/server/aws/apigateway"
	appsyncsrv "github.com/stackshy/cloudemu/v2/server/aws/appsync"
	cognitosrv "github.com/stackshy/cloudemu/v2/server/aws/cognito"
	stssrv "github.com/stackshy/cloudemu/v2/server/aws/sts"
)

// Some AWS operations are called without SigV4 credentials: a user signs in to
// Cognito before it has any AWS credentials, a web-identity token is traded for
// credentials, and API Gateway / AppSync endpoints are hit by browsers. The
// operation tables below are the operations whose Smithy model carries
// "auth": ["smithy.api#noAuth"] (botocore: "authtype": "none"), taken from the
// botocore service models (awscli 2.31.19, botocore/data/<service>/*/service-2.json).
// TestPublicOpsMatchSDKModels re-derives the same sets from the generated
// aws-sdk-go-v2 auth resolvers and fails when a table drifts from the model.
// Of the other services with noAuth operations, sso and sso-oidc are not served.

// cognitoIDPPublicOps are the cognito-idp (AWSCognitoIdentityProviderService)
// noAuth operations. They authenticate with an access token, a session, or a
// client secret hash inside the request, never with SigV4.
//
//nolint:gochecknoglobals // static protocol lookup table
var cognitoIDPPublicOps = map[string]struct{}{
	"AssociateSoftwareToken":           {},
	"ChangePassword":                   {},
	"CompleteWebAuthnRegistration":     {},
	"ConfirmDevice":                    {},
	"ConfirmForgotPassword":            {},
	"ConfirmSignUp":                    {},
	"DeleteUser":                       {},
	"DeleteUserAttributes":             {},
	"DeleteWebAuthnCredential":         {},
	"ForgetDevice":                     {},
	"ForgotPassword":                   {},
	"GetDevice":                        {},
	"GetTokensFromRefreshToken":        {},
	"GetUser":                          {},
	"GetUserAttributeVerificationCode": {},
	"GetUserAuthFactors":               {},
	"GlobalSignOut":                    {},
	"InitiateAuth":                     {},
	"ListDevices":                      {},
	"ListWebAuthnCredentials":          {},
	"ResendConfirmationCode":           {},
	"RespondToAuthChallenge":           {},
	"RevokeToken":                      {},
	"SetUserMFAPreference":             {},
	"SetUserSettings":                  {},
	"SignUp":                           {},
	"StartWebAuthnRegistration":        {},
	"UpdateAuthEventFeedback":          {},
	"UpdateDeviceStatus":               {},
	"UpdateUserAttributes":             {},
	"VerifySoftwareToken":              {},
	"VerifyUserAttribute":              {},
}

// cognitoIdentityPublicOps are the cognito-identity (AWSCognitoIdentityService)
// noAuth operations, which authenticate with the identity-pool logins map.
//
//nolint:gochecknoglobals // static protocol lookup table
var cognitoIdentityPublicOps = map[string]struct{}{
	"GetCredentialsForIdentity": {},
	"GetId":                     {},
	"GetOpenIdToken":            {},
	"UnlinkIdentity":            {},
}

// stsPublicActions are the STS noAuth actions: the caller presents a web
// identity token or a SAML assertion instead of AWS credentials.
//
//nolint:gochecknoglobals // static protocol lookup table
var stsPublicActions = map[string]struct{}{
	"AssumeRoleWithSAML":        {},
	"AssumeRoleWithWebIdentity": {},
}

const (
	cognitoIDPTargetPrefix      = "AWSCognitoIdentityProviderService."
	cognitoIdentityTargetPrefix = "AWSCognitoIdentityService."
	wellKnownSegment            = "/.well-known/"
	hostedUIPathPrefix          = "/_cognito/"
	restAPIsPrefix              = "/restapis/"
	userRequestSegment          = "_user_request_"
	userRequestSegmentIndex     = 2 // {apiId}/{stage}/_user_request_/...
	userRequestSplitParts       = 4 // the three leading segments plus the rest
	graphQLPath                 = "/graphql"
	urlEncodedForm              = "application/x-www-form-urlencoded"
)

// userPoolIDPattern is the Cognito user pool id shape ({region}_{suffix}). The
// underscore makes it an illegal S3 bucket name, so a .well-known path under it
// never names a bucket.
var userPoolIDPattern = regexp.MustCompile(`^[a-z]{2}(-[a-z]+)+-\d_[0-9A-Za-z]+$`)

// publicRoute is one family of requests AWS serves without SigV4. match decides
// from the request shape alone. owner reports whether the handler that would
// actually serve the request is the one that owns this public surface; nil
// means no served handler owns it yet.
type publicRoute struct {
	match func(r *http.Request, body []byte) bool
	owner func(server.Handler) bool
}

// publicRoutes lists every public surface. A route whose owner handler does not
// claim the request yet (the Cognito JWKS and hosted UI, the AppSync GraphQL
// endpoint) stays gated. It opens once that handler starts claiming it.
//
//nolint:gochecknoglobals // static route table
var publicRoutes = []publicRoute{
	{match: targetIn(cognitoIDPTargetPrefix, cognitoIDPPublicOps), owner: ownedBy[*cognitosrv.Handler]},
	{match: targetIn(cognitoIdentityTargetPrefix, cognitoIdentityPublicOps)},
	{match: isUserPoolWellKnown, owner: ownedBy[*cognitosrv.Handler]},
	{match: isHostedUI, owner: ownedBy[*cognitosrv.Handler]},
	{match: isPublicSTSAction, owner: ownedBy[*stssrv.Handler]},
	{match: isExecuteAPI, owner: ownedBy[*apigatewaysrv.Handler]},
	{match: isUnsignedGraphQL, owner: ownedBy[*appsyncsrv.Handler]},
}

// exemptPublic reports whether r may skip the SigV4 gate. The request must have
// the shape of a public operation, and the handler that would serve it (per
// match, the dispatcher's own first-match lookup) must be that operation's
// owner. Binding to the dispatch target stops a request from borrowing a public
// marker, such as an execute-api Host or a public Action, while being routed to
// a different service. When no handler would serve the request it is exempt too,
// since the dispatcher then answers 501 and touches no state.
//
// Matches may parse the body, so the caller restores it afterwards. Exempt
// requests skip authorization as well: IAM does not govern noAuth operations.
func exemptPublic(r *http.Request, body []byte, match func(*http.Request) server.Handler) bool {
	rt := publicRouteFor(r, body)
	if rt == nil {
		return false
	}

	h := match(r)
	if h == nil {
		return true
	}

	return rt.owner != nil && rt.owner(h)
}

// publicRouteFor returns the public route r's shape matches, or nil.
func publicRouteFor(r *http.Request, body []byte) *publicRoute {
	for i := range publicRoutes {
		if publicRoutes[i].match(r, body) {
			return &publicRoutes[i]
		}
	}

	return nil
}

func ownedBy[T server.Handler](h server.Handler) bool {
	_, ok := h.(T)
	return ok
}

// targetIn matches a JSON-RPC request whose X-Amz-Target is prefix+op for an
// op in ops.
func targetIn(prefix string, ops map[string]struct{}) func(*http.Request, []byte) bool {
	return func(r *http.Request, _ []byte) bool {
		op, ok := strings.CutPrefix(r.Header.Get("X-Amz-Target"), prefix)
		if !ok {
			return false
		}

		_, public := ops[op]

		return public
	}
}

// isPublicSTSAction matches a query-protocol request whose Action (form body
// first, then the query string, the order net/http's ParseForm gives) is a
// public STS action.
func isPublicSTSAction(r *http.Request, body []byte) bool {
	if r.Header.Get("X-Amz-Target") != "" {
		return false
	}

	action := ""

	if strings.HasPrefix(r.Header.Get("Content-Type"), urlEncodedForm) {
		if form, err := url.ParseQuery(string(body)); err == nil {
			action = form.Get("Action")
		}
	}

	if action == "" {
		action = r.URL.Query().Get("Action")
	}

	_, public := stsPublicActions[action]

	return public
}

// isUserPoolWellKnown matches GET /{userPoolId}/.well-known/... (jwks.json and
// openid-configuration).
func isUserPoolWellKnown(r *http.Request, _ []byte) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}

	pool, rest, ok := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if !ok {
		return false
	}

	return strings.HasPrefix("/"+rest, wellKnownSegment) && userPoolIDPattern.MatchString(pool)
}

// isHostedUI matches the Cognito hosted domain ({domain}.auth.{region}.amazoncognito.com,
// or {domain}.auth.localhost locally) and its path fallback /_cognito/{domain}/...
func isHostedUI(r *http.Request, _ []byte) bool {
	if strings.HasPrefix(r.URL.Path, hostedUIPathPrefix) {
		return true
	}

	host := hostOnly(r.Host)

	return strings.Contains(host, ".auth.") &&
		(strings.HasSuffix(host, ".amazoncognito.com") || strings.HasSuffix(host, ".auth.localhost"))
}

// isExecuteAPI matches an API Gateway invocation: an execute-api host, or the
// path form /restapis/{apiId}/{stage}/_user_request_/...
func isExecuteAPI(r *http.Request, _ []byte) bool {
	if strings.Contains(r.Host, ".execute-api.") {
		return true
	}

	rest, ok := strings.CutPrefix(r.URL.Path, restAPIsPrefix)
	if !ok {
		return false
	}

	segs := strings.SplitN(rest, "/", userRequestSplitParts)

	return len(segs) > userRequestSegmentIndex && segs[userRequestSegmentIndex] == userRequestSegment
}

// isUnsignedGraphQL matches an AppSync GraphQL call that carries no SigV4
// Authorization header (API key, Cognito or OIDC auth modes). A SigV4-signed
// call uses the IAM auth mode and goes through the gate.
func isUnsignedGraphQL(r *http.Request, _ []byte) bool {
	if r.Header.Get("Authorization") != "" {
		return false
	}

	if strings.Contains(r.Host, ".appsync-api.") {
		return true
	}

	return r.URL.Path == graphQLPath || strings.HasPrefix(r.URL.Path, graphQLPath+"/")
}

func hostOnly(hostport string) string {
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		return h
	}

	return hostport
}
