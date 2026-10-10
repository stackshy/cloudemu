// Package awsauthz is the contract between the AWS auth gate and the service
// handlers for IAM authorization. A handler says which IAM actions a request
// needs (Resolver), or at least which IAM service it belongs to
// (ServiceNamer), and may render the 403 in its own wire format (DenyWriter).
// The gate does the policy evaluation; this package holds only the shared
// types and helpers, so handlers can depend on it without importing the gate.
package awsauthz

import (
	"context"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/server/authctx"
)

// CheckMode says how an implicit deny on a Check is treated.
type CheckMode int

const (
	// Required makes an implicit deny final. It is the default.
	Required CheckMode = iota
	// DenyOnly lets the request through unless a policy explicitly denies it,
	// for operations AWS documents as needing no permission but still subject
	// to an explicit Deny (sts:GetSessionToken).
	DenyOnly
	// ResourcePolicy leaves an implicit deny to the handler, which finishes the
	// decision with the resource's own policy (for example a role trust policy).
	// The gate records the identity decision in the Evaluation.
	ResourcePolicy
)

// Check is one IAM permission a request needs.
type Check struct {
	// Action is the IAM action, such as "s3:PutObject".
	Action string
	// Resource is the full ARN, "*" for actions that take no resource, or ""
	// when the handler cannot name it yet. An unknown resource is evaluated
	// conservatively: never more permissive than any concrete resource.
	Resource string
	Mode     CheckMode
	// MessageResource, when set, is the resource a deny message names instead
	// of Resource. AWS names the resource the caller asked for, so a handler
	// that evaluates a resolved ARN keeps the message free of anything the
	// caller did not send (such as whether that resource exists).
	MessageResource string
}

// Scope is the account, region and partition the server runs in. It is
// fixed by the server, never taken from the request's signing scope.
type Scope struct {
	AccountID, Region, Partition string
}

// Resolver is implemented by handlers that can name the IAM actions of a
// request (tier 1). ok=false means the handler cannot name the operation, and
// it then guarantees ServeHTTP writes an error without side effects. The
// checks must be derived from the same signal dispatch uses to pick the
// operation, never from a separate parse.
type Resolver interface {
	IAMChecks(r *http.Request, s Scope) (checks []Check, ok bool)
}

// ContextResolver is implemented by a Resolver whose operations have
// service-specific condition keys (such as lambda:FunctionUrlAuthType or
// aws:RequestTag/*). The gate calls it instead of IAMChecks and adds the keys
// it returns to the request's condition context, through MergeContext.
type ContextResolver interface {
	IAMChecksWithContext(r *http.Request, s Scope) (checks []Check, cond map[string]string, ok bool)
}

// MergeContext copies into dst the keys of extra a handler of service may
// set: its own "<service>:" keys and the request and resource tag keys. Any
// other key is dropped and a key already in dst is kept, so a handler can
// never replace a global key the gate derived (aws:PrincipalArn,
// aws:SourceIp, ...).
func MergeContext(dst, extra map[string]string, service string) {
	for k, v := range extra {
		if _, taken := dst[k]; taken || !handlerKey(k, service) {
			continue
		}

		dst[k] = v
	}
}

// handlerKey reports whether a handler of service may set condition key k.
func handlerKey(k, service string) bool {
	switch {
	case service != "" && strings.HasPrefix(k, service+":"):
		return true
	case k == "aws:TagKeys":
		return true
	default:
		return strings.HasPrefix(k, "aws:RequestTag/") || strings.HasPrefix(k, "aws:ResourceTag/")
	}
}

// ServiceNamer is implemented by every AWS handler. It returns the IAM
// service prefix (such as "s3" or "elasticfilesystem") of the operations the
// handler serves. A handler that is not a Resolver is authorized at service
// level: only a grant covering every action of the service lets it through.
type ServiceNamer interface {
	IAMService() string
}

// DenyWriter is implemented by handlers whose 403 has a service-specific
// shape (EC2 UnauthorizedOperation, the S3 and Route 53 XML errors).
type DenyWriter interface {
	WriteAccessDenied(w http.ResponseWriter, r *http.Request, msg string)
}

// Decision is the outcome of evaluating one Check against the caller's
// identity policies.
type Decision string

// The identity decisions, matching the IAM simulator's strings.
const (
	Allowed      Decision = "allowed"
	ImplicitDeny Decision = "implicitDeny"
	ExplicitDeny Decision = "explicitDeny"
)

// Evaluation carries the gate's identity decisions to the handler, so a
// handler can finish a ResourcePolicy check with the resource's own policy.
type Evaluation struct {
	Principal authctx.Principal
	CondCtx   map[string]string
	Decisions map[Check]Decision
}

type evaluationKey struct{}

// WithEvaluation returns ctx carrying e.
func WithEvaluation(ctx context.Context, e *Evaluation) context.Context {
	return context.WithValue(ctx, evaluationKey{}, *e)
}

// EvaluationFrom returns the Evaluation the gate attached, and ok=false when
// the request was not authorized by the gate (EnforceAuth off).
func EvaluationFrom(ctx context.Context) (Evaluation, bool) {
	e, ok := ctx.Value(evaluationKey{}).(Evaluation)

	return e, ok
}

// Single returns the one-check list for action on resource.
func Single(action, resource string) []Check {
	return []Check{{Action: action, Resource: resource}}
}

// QueryChecks names the IAM action of a query-protocol request as
// "<service>:<Action>", with an unknown resource. It reads the form Action
// exactly as the query handlers' dispatch does (r.Form.Get, so the first body
// value wins over the query string). A form that does not parse, or a
// missing Action, returns ok=false.
func QueryChecks(r *http.Request, service string) ([]Check, bool) {
	if err := r.ParseForm(); err != nil {
		return nil, false
	}

	action := r.Form.Get("Action")
	if action == "" {
		return nil, false
	}

	return Single(service+":"+action, ""), true
}

// ConditionContext gathers the AWS global condition keys known from the
// request and the verified caller. Keys that cannot be known are left out,
// so a policy that references one follows IAM's missing-key rules.
func ConditionContext(r *http.Request, p *authctx.Principal, s Scope) map[string]string {
	ctx := map[string]string{
		"aws:CurrentTime":     time.Now().UTC().Format(time.RFC3339),
		"aws:SecureTransport": strconv.FormatBool(r.TLS != nil),
	}

	set := func(key, value string) {
		if value != "" {
			ctx[key] = value
		}
	}

	set("aws:SourceIp", clientIP(r))
	set("aws:PrincipalArn", p.ARN)
	set("aws:username", p.UserName)
	set("aws:userid", p.UserID)
	set("aws:PrincipalAccount", p.AccountID)
	set("aws:PrincipalType", principalType(p.ARN))
	set("aws:RequestedRegion", s.Region)

	return ctx
}

// principalType maps a caller ARN to the aws:PrincipalType value.
func principalType(arn string) string {
	switch {
	case arn == "":
		return ""
	case strings.HasSuffix(arn, ":root"):
		return "Account"
	case strings.Contains(arn, ":assumed-role/"):
		return "AssumedRole"
	case strings.Contains(arn, ":federated-user/"):
		return "FederatedUser"
	default:
		return "User"
	}
}

// clientIP is the caller's address for aws:SourceIp. It uses only the
// connection's RemoteAddr, never X-Forwarded-For: no trusted proxy sits in
// front of the server, so that header would let a client pick its own IP.
func clientIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}

	return r.RemoteAddr
}
