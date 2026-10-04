package aws

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/stackshy/cloudemu/v2/server"
	"github.com/stackshy/cloudemu/v2/server/authctx"
	"github.com/stackshy/cloudemu/v2/server/wire"
	"github.com/stackshy/cloudemu/v2/server/wire/awsauthz"
	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
)

// jsonRPCServiceByTarget maps a JSON-RPC X-Amz-Target prefix (the part before
// the operation, e.g. "DynamoDB_20120810." or "TrentService.") to the IAM
// service the operation belongs to. It is read only for handlers registered
// as JSON-RPC handlers (gateConfig.jsonRPC), which route on that header, and
// the service it gives must equal the handler's own IAMService. Every
// JSON-RPC service the wire server serves must appear here; an unmapped
// target fails closed.
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
	"OpenSearchServerless.":                 "aoss",
	"AWSKendraFrontendService.":             "kendra",
	"AmazonAthena.":                         "athena",
	"AWSCognitoIdentityProviderService.":    "cognito-idp",
	"StarlingDoveService.":                  "config",
	"AWSWAF_20190729.":                      "wafv2",
	"AmazonEC2ContainerServiceV20141113.":   "ecs",
	"AmazonEC2ContainerRegistry_V20150921.": "ecr",
	"Route53Resolver.":                      "route53resolver",
	"AWSEvents.":                            "events",
	"Logs_20140328.":                        "logs",
	"GraniteServiceVersion20100801.":        "cloudwatch",
	"SageMaker.":                            "sagemaker",
	"secretsmanager.":                       "secretsmanager",
	"KeyspacesService.":                     "cassandra",
	"AmazonMemoryDB.":                       "memorydb",
	"NetworkFirewall_20201112.":             "network-firewall",
	"ResourceGroupsTaggingAPI_20170126.":    "tag",
	"TransferService.":                      "transfer",
	"Timestream_20181101.":                  "timestream",
	"HealthLake.":                           "healthlake",
	"AppRunner.":                            "apprunner",
	"GlobalAccelerator_V20180706.":          "globalaccelerator",
	"AWSInsightsIndexService.":              "ce",
	"ServiceQuotasV20190624.":               "servicequotas",
	"ElasticMapReduce.":                     "elasticmapreduce",
}

// servicePrefix is the shape of an IAM service prefix ("s3",
// "resource-explorer-2"). A service-wide plan is only built for a name of
// this shape, so an empty or malformed IAMService can never be evaluated as
// a wildcard.
var servicePrefix = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// planKind is how the gate authorizes one request, chosen from the handler
// dispatch will run.
type planKind int

const (
	// planNoHandler: no handler serves the request; dispatch answers 501.
	planNoHandler planKind = iota
	// planChecks: the handler (or the JSON-RPC table) named the IAM checks.
	planChecks
	// planUnknownOp: the handler cannot name the operation and will answer
	// with an error, so only unrestricted callers are let through to it.
	planUnknownOp
	// planJSONDeny: a JSON-RPC target the table does not bind to the handler.
	planJSONDeny
	// planServiceWide: a handler that only names its IAM service. Only a
	// grant covering every action of the service allows it.
	planServiceWide
	// planAuthnOnly: a handler IAM does not govern.
	planAuthnOnly
	// planUnmapped: anything else, including a request whose query string or
	// form body does not parse. Fails closed for restricted callers.
	planUnmapped
)

// authzPlan is the resolved authorization for one request.
type authzPlan struct {
	kind planKind
	// req is the probe the plan was resolved on, or nil when the request did
	// not parse. A deny is rendered from it, since its form is already parsed.
	req    *http.Request
	checks []awsauthz.Check
	// action names the operation in a deny message for the plans without
	// checks (the service-wide action for planServiceWide).
	action string
}

// resolvePlan picks the plan from the handler dispatch will run (h, found on
// probe by probeRoute). probed=false means the request did not parse.
func (g *gateConfig) resolvePlan(probe *http.Request, h server.Handler, probed bool) authzPlan {
	if !probed {
		return authzPlan{kind: planUnmapped}
	}

	if h == nil {
		return authzPlan{kind: planNoHandler}
	}

	if res, ok := h.(awsauthz.Resolver); ok {
		checks, known := res.IAMChecks(probe, g.scope)
		if !known {
			return authzPlan{kind: planUnknownOp, req: probe, action: iamService(h) + ":" + rawOperation(probe)}
		}

		return authzPlan{kind: planChecks, req: probe, checks: checks}
	}

	if g.jsonRPC[h] {
		return g.jsonRPCPlan(probe, h)
	}

	if svc := iamService(h); servicePrefix.MatchString(svc) {
		return authzPlan{kind: planServiceWide, req: probe, action: svc + ":*"}
	}

	if g.authnOnly[h] {
		return authzPlan{kind: planAuthnOnly}
	}

	return authzPlan{kind: planUnmapped, req: probe}
}

// jsonRPCPlan binds a JSON-RPC request to its action through the target
// table. The service the header names must be the handler's own, or the
// request fails closed. The resource is unknown: a JSON-RPC handler that can
// name its resources does so as a Resolver.
func (*gateConfig) jsonRPCPlan(probe *http.Request, h server.Handler) authzPlan {
	service, op, ok := jsonRPCTarget(probe)
	if !ok || service != iamService(h) {
		return authzPlan{kind: planJSONDeny, req: probe, action: service + ":" + op}
	}

	return authzPlan{kind: planChecks, req: probe, checks: awsauthz.Single(service+":"+op, "")}
}

// denyTarget is the request a deny is rendered from: the probe when there is
// one, else the original request.
func (p authzPlan) denyTarget(r *http.Request) *http.Request {
	if p.req != nil {
		return p.req
	}

	return r
}

// iamService returns the IAM service prefix a handler declares, or "".
func iamService(h server.Handler) string {
	if n, ok := h.(awsauthz.ServiceNamer); ok {
		return n.IAMService()
	}

	return ""
}

// rawOperation is the form Action of a request, for the deny message of an
// operation the handler cannot name.
func rawOperation(probe *http.Request) string {
	if probe.Form != nil {
		if a := probe.Form.Get("Action"); a != "" {
			return a
		}
	}

	return "UnknownOperation"
}

// authorize applies plan to the authenticated caller p. On allow it returns
// the request carrying the principal and the gate's evaluation; on deny it
// has written the 403.
//
// strict is set for an STS role session. Its principal is the role, which is
// evaluated on its policies alone: the root and no-policies bootstrap
// shortcuts that apply to IAM users do not apply.
func (g *gateConfig) authorize(
	w http.ResponseWriter, r *http.Request, h server.Handler, plan authzPlan, p *authctx.Principal, strict bool,
) (*http.Request, bool) {
	if plan.kind == planNoHandler || plan.kind == planAuthnOnly {
		return withPrincipal(r, *p), true
	}

	if plan.kind == planJSONDeny {
		writeAccessDenied(w, plan.denyTarget(r), h, denyMessage(p, plan.action, "", false))
		return r, false
	}

	ev := awsauthz.Evaluation{Principal: *p, CondCtx: awsauthz.ConditionContext(r, p, g.scope)}
	shortcut := !strict && (isAdminPrincipal(*p) || !principalHasPolicies(r, *p, g.iam))

	if msg := g.decide(r, p, plan, &ev, shortcut); msg != "" {
		writeAccessDenied(w, plan.denyTarget(r), h, msg)
		return r, false
	}

	r = withPrincipal(r, *p)

	return r.WithContext(awsauthz.WithEvaluation(r.Context(), &ev)), true
}

// decide evaluates the plans that depend on the caller's policies. It
// returns a deny message, or "" to allow, and records the ResourcePolicy
// decisions in ev.
func (g *gateConfig) decide(r *http.Request, p *authctx.Principal, plan authzPlan, ev *awsauthz.Evaluation, shortcut bool) string {
	var msg string

	switch plan.kind {
	case planChecks:
		ev.Decisions, msg = g.evaluateChecks(r, p, plan.checks, ev.CondCtx, shortcut)
	case planServiceWide:
		msg = g.evaluateServiceWide(r, p, plan.action, ev.CondCtx, shortcut)
	case planUnknownOp, planUnmapped:
		if !shortcut {
			msg = denyMessage(p, plan.action, "", false)
		}
	case planNoHandler, planAuthnOnly, planJSONDeny: // decided before the policies are read
	}

	return msg
}

// evaluateChecks evaluates each check against the caller's identity policies.
// It returns the decisions recorded for ResourcePolicy checks, and a deny
// message when a check denies the request.
func (g *gateConfig) evaluateChecks(
	r *http.Request, p *authctx.Principal, checks []awsauthz.Check, cctx map[string]string, shortcut bool,
) (decisions map[awsauthz.Check]awsauthz.Decision, denied string) {
	decisions = map[awsauthz.Check]awsauthz.Decision{}

	for _, c := range checks {
		d := awsauthz.Allowed
		if !shortcut {
			d = g.evaluate(r, p, c, cctx)
		}

		switch {
		case d == awsauthz.ExplicitDeny:
			return nil, denyMessage(p, c.Action, messageResource(c), true)
		case d == awsauthz.ImplicitDeny && c.Mode == awsauthz.Required:
			return nil, denyMessage(p, c.Action, messageResource(c), false)
		}

		if c.Mode == awsauthz.ResourcePolicy {
			decisions[c] = d
		}
	}

	return decisions, ""
}

// messageResource is the resource a deny message names for c.
func messageResource(c awsauthz.Check) string {
	if c.MessageResource != "" {
		return c.MessageResource
	}

	return c.Resource
}

// evaluateServiceWide allows the caller only when its policies grant every
// action of the service on every resource. action is "<svc>:*".
func (g *gateConfig) evaluateServiceWide(
	r *http.Request, p *authctx.Principal, action string, cctx map[string]string, shortcut bool,
) string {
	if shortcut {
		return ""
	}

	pe, ok := g.iam.(iamdriver.PermissionEvaluator)
	if !ok {
		return denyMessage(p, action, "*", false)
	}

	d := pe.EvaluateServiceWide(r.Context(), p.UserName, strings.TrimSuffix(action, ":*"), cctx)
	if d == iamdriver.DecisionAllowed {
		return ""
	}

	return denyMessage(p, action, "*", d == iamdriver.DecisionExplicitDeny)
}

// evaluate returns the identity decision for one check. An empty Resource is
// evaluated as an unknown resource.
func (g *gateConfig) evaluate(r *http.Request, p *authctx.Principal, c awsauthz.Check, cctx map[string]string) awsauthz.Decision {
	if pe, ok := g.iam.(iamdriver.PermissionEvaluator); ok {
		return awsauthz.Decision(pe.EvaluatePermission(r.Context(), iamdriver.EvalRequest{
			Principal: p.UserName, Action: c.Action, Resource: c.Resource, ResourceKnown: c.Resource != "", Context: cctx,
		}))
	}

	resource := c.Resource
	if resource == "" {
		resource = "*"
	}

	if checkPermission(r, p, g.iam, c.Action, resource, cctx) {
		return awsauthz.Allowed
	}

	return awsauthz.ImplicitDeny
}

// checkPermission is the fallback for an IAM driver without the tri-state
// PermissionEvaluator: a plain allow/deny for one action and resource.
func checkPermission(
	r *http.Request, p *authctx.Principal, iamDriver iamdriver.IAM, action, resource string, cctx map[string]string,
) bool {
	if ca, ok := iamDriver.(iamdriver.ContextualAuthorizer); ok {
		allowed, err := ca.CheckPermissionWithContext(r.Context(), p.UserName, action, resource, cctx)
		return err == nil && allowed
	}

	allowed, err := iamDriver.CheckPermission(r.Context(), p.UserName, action, resource)

	return err == nil && allowed
}

// jsonRPCTarget splits X-Amz-Target into the IAM service (through
// jsonRPCServiceByTarget) and the operation. ok=false when the header is
// missing, names no operation, or its prefix is not in the table.
func jsonRPCTarget(r *http.Request) (service, op string, ok bool) {
	target := r.Header.Get("X-Amz-Target")
	if target == "" {
		return "", "", false
	}

	dot := strings.LastIndexByte(target, '.')
	op = target[dot+1:]
	service, ok = jsonRPCServiceByTarget[target[:dot+1]]

	return service, op, ok && op != ""
}

// isAdminPrincipal reports whether p is the account-root / bootstrap admin
// identity, which is always allowed (mirroring real IAM, where root has full
// access). A verified long-term key always resolves to a named IAM user, so in
// practice this guards an explicit root identity and the defensive empty-name
// case.
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

// denyMessage is the AWS AccessDenied message for action on resource. An
// unknown resource is shown as "*"; with no action (a request the gate could
// not bind to any IAM service) the message names none.
func denyMessage(p *authctx.Principal, action, resource string, explicit bool) string {
	if action == "" {
		return "User: " + principalARN(p) + " is not authorized to perform this request"
	}

	if resource == "" {
		resource = "*"
	}

	msg := "User: " + principalARN(p) + " is not authorized to perform: " + action + " on resource: " + resource

	if explicit {
		return msg + " with an explicit deny in an identity-based policy"
	}

	return msg + " because no identity-based policy allows the " + action + " action"
}

// writeAccessDenied renders an authorization 403. A handler with its own 403
// shape writes it; otherwise the shape follows the request: the query
// protocol gets the XML AccessDenied error, everything else the JSON
// AccessDeniedException (with X-Amzn-Errortype).
func writeAccessDenied(w http.ResponseWriter, r *http.Request, h server.Handler, msg string) {
	if dw, ok := h.(awsauthz.DenyWriter); ok {
		dw.WriteAccessDenied(w, r, msg)
		return
	}

	if isQueryShaped(r) {
		awsquery.WriteXMLError(w, http.StatusForbidden, "AccessDenied", msg)
		return
	}

	wire.WriteJSONError(w, http.StatusForbidden, "AccessDeniedException", msg)
}

// isQueryShaped reports whether r is an AWS query-protocol request: no
// X-Amz-Target, and a form body or an Action in the query string.
func isQueryShaped(r *http.Request) bool {
	if r.Header.Get("X-Amz-Target") != "" {
		return false
	}

	return strings.HasPrefix(r.Header.Get("Content-Type"), urlEncodedForm) || r.URL.Query().Get("Action") != ""
}

// principalARN returns a stable identifier for the caller in an error message,
// preferring the resolved ARN and falling back to the user name.
func principalARN(p *authctx.Principal) string {
	if p.ARN != "" {
		return p.ARN
	}

	return p.UserName
}
