package aws

import (
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/server/authctx"
	"github.com/stackshy/cloudemu/v2/server/wire"
	"github.com/stackshy/cloudemu/v2/server/wire/sigv4"
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
)

// authzDecision is the outcome of deriving an IAM action for a request.
type authzDecision int

const (
	// authzSkip: the request is authenticated only (REST and other protocols
	// whose executed operation is not bound to a signal the gate can read before
	// dispatch). No authorization decision is made.
	authzSkip authzDecision = iota
	// authzEnforce: the IAM action was derived from the dispatch key; gate it
	// through CheckPermission.
	authzEnforce
	// authzDeny: the request is a JSON-RPC call whose target does not map to a
	// known served service, so the executed operation cannot be bound to an IAM
	// action. Fail closed rather than authorize on an unverifiable service.
	authzDeny
)

// jsonRPCServiceByTarget maps a JSON-RPC X-Amz-Target prefix (the part before
// the operation, e.g. "DynamoDB_20120810." or "TrentService.") to the IAM
// service the operation belongs to. The X-Amz-Target header is the value the
// dispatcher itself routes on, so a service derived from it is bound to the
// handler that actually runs — unlike the SigV4 credential scope, which the
// client controls independently of the operation. Every JSON-RPC service the
// wire server serves must appear here; an unmapped target fails closed.
//
//nolint:gochecknoglobals // static protocol lookup table
var jsonRPCServiceByTarget = map[string]string{
	"DynamoDB_20120810.":                    "dynamodb",
	"DynamoDBStreams_20120810.":             "dynamodb",
	"AmazonSQS.":                            "sqs",
	"AmazonSSM.":                            "ssm",
	"TrentService.":                         "kms",
	"CertificateManager.":                   "acm",
	"AWSStepFunctions.":                     "states",
	"Kinesis_20131202.":                     "kinesis",
	"CloudTrail_20131101.":                  "cloudtrail",
	"AWSGlue.":                              "glue",
	"StarlingDoveService.":                  "config",
	"AWSWAF_20190729.":                      "wafv2",
	"AmazonEC2ContainerServiceV20141113.":   "ecs",
	"AmazonEC2ContainerRegistry_V20150921.": "ecr",
	"Route53Resolver.":                      "route53resolver",
	"AWSEvents.":                            "events",
	"Logs_20140328.":                        "logs",
	"SageMaker.":                            "sagemaker",
	"secretsmanager.":                       "secretsmanager",
	"KeyspacesService.":                     "cassandra",
	"AmazonMemoryDB.":                       "memorydb",
	"NetworkFirewall_20201112.":             "network-firewall",
	"ResourceGroupsTaggingAPI_20170126.":    "tag",
}

// authorize is the authorization step layered on top of the SigV4 authentication
// gate. It runs only when EnforceAuth is on and after a request has been
// authenticated, so p is a verified IAM principal. It derives the IAM action for
// the request from the dispatch key and, for a real IAM principal that has
// policies defined, gates the action through CheckPermission. It returns
// proceed=false only when the action is denied, having already written the 403.
//
// Authorization is enforced for the JSON-RPC protocol, where the X-Amz-Target
// header both routes the request and names the service, so the service the gate
// authorizes is the one the handler runs. The query and REST protocols are
// authenticated only: there the executed operation's IAM service is not bound to
// any pre-dispatch signal the gate can trust — query dispatch routes on the
// action name (a single handler, e.g. EC2, serves several IAM services such as
// ec2/vpc/autoscaling), and the SigV4 credential scope is client-controlled and
// decoupled from the operation. Authorizing query/REST on that scope would let a
// caller scoped to service A run an operation that executes under service B, so
// action+resource authorization bound to the routed operation is a follow-up.
func authorize(
	w http.ResponseWriter, r *http.Request, p authctx.Principal, iamDriver iamdriver.IAM, body []byte, accountID string,
) bool {
	service, action, decision := deriveAction(r)

	if decision == authzSkip {
		return true
	}

	if decision == authzDeny {
		// JSON-RPC target that maps to no known service: fail closed.
		writeAuthzDenied(w, p, service+":"+jsonRPCTargetOperation(r))
		return false
	}

	if isAdminPrincipal(p) {
		return true // account root / bootstrap admin identity: full access.
	}

	if !principalHasPolicies(r, p, iamDriver) {
		return true // no policies defined: unrestricted (dev-friendly bootstrap).
	}

	resource := deriveResource(service, body, sigv4.Region(r), accountID)

	if checkPermission(r, p, iamDriver, action, resource) {
		return true
	}

	writeAuthzDenied(w, p, action)

	return false
}

// checkPermission evaluates the action against the derived resource for the
// caller. When the IAM driver supports the ContextualAuthorizer capability, the
// request condition context (source IP, region, principal, secure transport,
// current time) is threaded so Condition-guarded statements are honored;
// otherwise it falls back to the resource-only CheckPermission.
func checkPermission(
	r *http.Request, p authctx.Principal, iamDriver iamdriver.IAM, action, resource string,
) bool {
	if ca, ok := iamDriver.(iamdriver.ContextualAuthorizer); ok {
		allowed, err := ca.CheckPermissionWithContext(r.Context(), p.UserName, action, resource, requestConditionContext(r, p))
		return err == nil && allowed
	}

	allowed, err := iamDriver.CheckPermission(r.Context(), p.UserName, action, resource)

	return err == nil && allowed
}

// requestConditionContext gathers the AWS global condition keys the gate can
// derive from the request and the verified principal. Keys that cannot be
// determined are omitted, so a policy that references an absent key evaluates
// per IAM's absent-key rules (plain → no match, ...IfExists → match).
func requestConditionContext(r *http.Request, p authctx.Principal) map[string]string {
	ctx := map[string]string{
		"aws:CurrentTime":     time.Now().UTC().Format(time.RFC3339),
		"aws:SecureTransport": strconv.FormatBool(r.TLS != nil),
	}

	if ip := clientIP(r); ip != "" {
		ctx["aws:SourceIp"] = ip
	}

	if p.ARN != "" {
		ctx["aws:PrincipalArn"] = p.ARN
	}

	if p.UserName != "" {
		ctx["aws:username"] = p.UserName
	}

	if region := sigv4.Region(r); region != "" {
		ctx["aws:RequestedRegion"] = region
	}

	return ctx
}

// clientIP extracts the caller's source IP, preferring the first X-Forwarded-For
// hop and falling back to the connection's RemoteAddr (port stripped).
func clientIP(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		if i := strings.IndexByte(fwd, ','); i >= 0 {
			return strings.TrimSpace(fwd[:i])
		}

		return strings.TrimSpace(fwd)
	}

	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}

	return r.RemoteAddr
}

// deriveResource derives the target resource ARN for an authorized action from
// the request, so resource-scoped Allow/Deny policies apply. It covers the
// services whose JSON-RPC body names a single primary resource; where the
// resource cannot be derived it falls back to "*", which matches any
// resource-scoped statement's "*" and leaves resource-scoped statements for
// other resources non-binding (the pre-existing behavior).
func deriveResource(service string, body []byte, region, accountID string) string {
	if service == "dynamodb" {
		if name := jsonField(body, "TableName"); name != "" {
			return "arn:aws:dynamodb:" + region + ":" + accountID + ":table/" + name
		}
	}

	return "*"
}

// jsonField extracts a single top-level string field from a JSON-RPC request
// body without fully modeling the operation. It returns "" when the body is not
// an object or the field is absent or non-string.
func jsonField(body []byte, field string) string {
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return ""
	}

	if v, ok := m[field].(string); ok {
		return v
	}

	return ""
}

// deriveAction maps an incoming request to its IAM action (e.g. dynamodb:PutItem)
// using only signals bound to how the request is dispatched. For JSON-RPC it uses
// the X-Amz-Target header: the prefix selects the service via jsonRPCServiceByTarget
// (the same header the dispatcher routes on) and the suffix is the operation. A
// JSON-RPC request whose target prefix is not mapped returns authzDeny (fail
// closed). All other protocols (query, REST) return authzSkip: authenticate only.
func deriveAction(r *http.Request) (service, action string, decision authzDecision) {
	target := r.Header.Get("X-Amz-Target")
	if target == "" {
		return "", "", authzSkip
	}

	prefix := target[:strings.LastIndexByte(target, '.')+1]
	op := jsonRPCTargetOperation(r)

	svc, ok := jsonRPCServiceByTarget[prefix]
	if !ok || op == "" {
		return svc, "", authzDeny
	}

	return svc, svc + ":" + op, authzEnforce
}

// jsonRPCTargetOperation returns the operation suffix of the X-Amz-Target header
// (the part after the last "."), or "" when absent.
func jsonRPCTargetOperation(r *http.Request) string {
	target := r.Header.Get("X-Amz-Target")
	if target == "" {
		return ""
	}

	return target[strings.LastIndexByte(target, '.')+1:]
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

// writeAuthzDenied renders a 403 authorization failure. Enforced authorization is
// JSON-RPC only, so the response is always AccessDeniedException in the JSON-RPC
// error shape.
func writeAuthzDenied(w http.ResponseWriter, p authctx.Principal, action string) {
	msg := "User: " + principalARN(p) + " is not authorized to perform: " + action
	wire.WriteJSONError(w, http.StatusForbidden, "AccessDeniedException", msg)
}

// principalARN returns a stable identifier for the caller in an error message,
// preferring the resolved ARN and falling back to the user name.
func principalARN(p authctx.Principal) string {
	if p.ARN != "" {
		return p.ARN
	}

	return p.UserName
}
