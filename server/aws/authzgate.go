package aws

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/authctx"
	"github.com/stackshy/cloudemu/v2/server/wire"
	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
)

// ec2Service is the SigV4 credential-scope service name for the EC2 query
// endpoint (also used for VPC/Auto Scaling, which share it). EC2 reports an
// authorization failure as UnauthorizedOperation rather than AccessDenied.
const ec2Service = "ec2"

// authorize is the authorization step layered on top of the SigV4
// authentication gate. It runs only when EnforceAuth is on and after a request
// has been authenticated, so p is a verified IAM principal (a user resolved from
// a long-term access key). It derives the IAM action for the request, and for a
// real IAM principal that has policies defined it gates the action through
// CheckPermission. It returns proceed=false only when the action is denied, in
// which case it has already written the 403.
//
// Authorization is enforced only for the query and JSON-RPC protocols, where the
// action is derivable from the request before dispatch (the SigV4 scope service
// plus the Action parameter or the X-Amz-Target operation). REST services (S3,
// Lambda, Route53, EFS, EKS, …) carry no pre-dispatch action here, so they are
// authenticated only; resource-level and REST authorization are a follow-up.
func authorize(
	w http.ResponseWriter, r *http.Request, body []byte, p authctx.Principal, iamDriver iamdriver.IAM,
) bool {
	service, action, ok := deriveAction(r, body)
	if !ok {
		return true // REST / non-derivable action: authenticate only.
	}

	if isAdminPrincipal(p) {
		return true // account root / bootstrap admin identity: full access.
	}

	if !principalHasPolicies(r, p, iamDriver) {
		return true // no policies defined: unrestricted (dev-friendly bootstrap).
	}

	allowed, err := iamDriver.CheckPermission(r.Context(), p.UserName, action, "*")
	if err == nil && allowed {
		return true
	}

	writeAuthzDenied(w, r, service, p, action)

	return false
}

// deriveAction maps an incoming request to its IAM action (e.g. ec2:RunInstances,
// dynamodb:PutItem). It returns ok=false for requests whose action is not
// derivable before dispatch — every REST service. The service name comes from
// the SigV4 credential scope, which is authoritative and unambiguous (the JSON-RPC
// X-Amz-Target prefix, e.g. "TrentService" for KMS, is not).
func deriveAction(r *http.Request, body []byte) (service, action string, ok bool) {
	service = awsquery.CredentialScopeService(r.Header.Get("Authorization"))
	if service == "" {
		return "", "", false
	}

	if target := r.Header.Get("X-Amz-Target"); target != "" {
		op := target[strings.LastIndexByte(target, '.')+1:]
		if op == "" {
			return "", "", false
		}

		return service, service + ":" + op, true
	}

	if op := queryActionParam(r, body); op != "" {
		return service, service + ":" + op, true
	}

	return "", "", false
}

// queryActionParam reads the AWS query-protocol Action parameter without
// consuming the request body the downstream handler will re-parse: it prefers
// the query string (GET) and falls back to parsing the buffered form-encoded
// body (POST).
func queryActionParam(r *http.Request, body []byte) string {
	if v := r.URL.Query().Get("Action"); v != "" {
		return v
	}

	if vals, err := url.ParseQuery(string(body)); err == nil {
		return vals.Get("Action")
	}

	return ""
}

// isAdminPrincipal reports whether p is the account-root / bootstrap admin
// identity, which is always allowed (mirroring real IAM, where root has full
// access). A verified long-term key always resolves to a named IAM user, so in
// practice this guards only an explicit root identity and the defensive
// empty-name case; STS temporary (ASIA) credentials are allowed earlier, in the
// authentication gate, without reaching here.
func isAdminPrincipal(p authctx.Principal) bool {
	return p.UserName == "" || p.UserName == "root" || strings.HasSuffix(p.ARN, ":root")
}

// principalHasPolicies reports whether the IAM driver can see any policies in
// effect for p. When the driver cannot be inspected, or the principal has no
// policies at all, authorization is left unenforced so a freshly created
// key-only user is not locked out before any policy is written.
func principalHasPolicies(r *http.Request, p authctx.Principal, iamDriver iamdriver.IAM) bool {
	inspector, ok := iamDriver.(iamdriver.PolicyInspector)
	if !ok {
		return false
	}

	return inspector.PrincipalHasPolicies(r.Context(), p.UserName)
}

// writeAuthzDenied renders a 403 authorization failure in the wire shape of the
// target protocol: AccessDeniedException (JSON) for JSON-RPC, UnauthorizedOperation
// (XML) for EC2, and AccessDenied (XML) for other query-protocol services.
func writeAuthzDenied(w http.ResponseWriter, r *http.Request, service string, p authctx.Principal, action string) {
	msg := "User: " + principalARN(p) + " is not authorized to perform: " + action

	if r.Header.Get("X-Amz-Target") != "" {
		wire.WriteJSONError(w, http.StatusForbidden, "AccessDeniedException", msg)
		return
	}

	code := "AccessDenied"
	if service == ec2Service {
		code = "UnauthorizedOperation"
	}

	awsquery.WriteXMLError(w, http.StatusForbidden, code, msg)
}

// principalARN returns a stable identifier for the caller in an error message,
// preferring the resolved ARN and falling back to the user name.
func principalARN(p authctx.Principal) string {
	if p.ARN != "" {
		return p.ARN
	}

	return p.UserName
}
