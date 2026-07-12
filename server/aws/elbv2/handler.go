// Package elbv2 implements the AWS Elastic Load Balancing v2 (ALB/NLB)
// query-protocol as a server.Handler. Point the real aws-sdk-go-v2
// elasticloadbalancingv2 client at a Server registered with this handler and
// LoadBalancer / TargetGroup / Listener / Rule / target-health operations work
// against an in-memory loadbalancer driver.
//
// ELBv2 shares the AWS query wire shape with EC2 and RDS (POST + form-encoded
// body, XML response). To keep dispatch unambiguous, this handler's Matches
// predicate parses the form body once and only claims requests whose Action is
// one of the known ELBv2 operations. The EC2 handler is the catch-all for all
// other query-protocol actions, so this handler MUST register before EC2. Its
// action set is disjoint from RDS/IAM/Redshift, so ordering relative to those
// is unconstrained.
package elbv2

import (
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	lbdriver "github.com/stackshy/cloudemu/v2/services/loadbalancer/driver"
)

// Namespace is the XML namespace for AWS ELBv2 responses.
const Namespace = "http://elasticloadbalancing.amazonaws.com/doc/2015-12-01/"

const (
	formContentType  = "application/x-www-form-urlencoded"
	maxFormBodyBytes = 1 << 20
)

// elbActions is the set of Action values this handler recognizes. Matches uses
// it to decide whether to claim a request.
var elbActions = map[string]struct{}{ //nolint:gochecknoglobals // static lookup table
	"CreateLoadBalancer":    {},
	"DescribeLoadBalancers": {},
	"DeleteLoadBalancer":    {},
	"CreateTargetGroup":     {},
	"DescribeTargetGroups":  {},
	"DeleteTargetGroup":     {},
	"CreateListener":        {},
	"DescribeListeners":     {},
	"DeleteListener":        {},
	"CreateRule":            {},
	"DescribeRules":         {},
	"DeleteRule":            {},
	"RegisterTargets":       {},
	"DeregisterTargets":     {},
	"DescribeTargetHealth":  {},
}

// Handler serves ELBv2 query-protocol requests.
type Handler struct {
	lb lbdriver.LoadBalancer
}

// New returns an ELBv2 handler backed by lb.
func New(lb lbdriver.LoadBalancer) *Handler {
	return &Handler{lb: lb}
}

// Matches returns true if the request looks like an AWS ELBv2 query-protocol
// call (POST + form-encoded body whose Action is one of the known ELBv2
// operations). Calling ParseForm here caches the parsed form on the request so
// ServeHTTP can use it without re-reading the body.
func (*Handler) Matches(r *http.Request) bool {
	if r.Header.Get("X-Amz-Target") != "" {
		return false
	}

	if r.Method != http.MethodPost {
		return false
	}

	if !strings.HasPrefix(r.Header.Get("Content-Type"), formContentType) {
		return false
	}

	r.Body = http.MaxBytesReader(nil, r.Body, maxFormBodyBytes)
	if err := r.ParseForm(); err != nil {
		return false
	}

	_, ok := elbActions[r.Form.Get("Action")]

	return ok
}

// ServeHTTP dispatches on Action. The form has already been parsed by Matches.
//
//nolint:gocyclo // 15 cases for one-shot dispatch; splitting into sub-routers would be more complex than the switch.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	action := r.Form.Get("Action")

	switch action {
	case "CreateLoadBalancer":
		h.createLoadBalancer(w, r)
	case "DescribeLoadBalancers":
		h.describeLoadBalancers(w, r)
	case "DeleteLoadBalancer":
		h.deleteLoadBalancer(w, r)
	case "CreateTargetGroup":
		h.createTargetGroup(w, r)
	case "DescribeTargetGroups":
		h.describeTargetGroups(w, r)
	case "DeleteTargetGroup":
		h.deleteTargetGroup(w, r)
	case "CreateListener":
		h.createListener(w, r)
	case "DescribeListeners":
		h.describeListeners(w, r)
	case "DeleteListener":
		h.deleteListener(w, r)
	case "CreateRule":
		h.createRule(w, r)
	case "DescribeRules":
		h.describeRules(w, r)
	case "DeleteRule":
		h.deleteRule(w, r)
	case "RegisterTargets":
		h.registerTargets(w, r)
	case "DeregisterTargets":
		h.deregisterTargets(w, r)
	case "DescribeTargetHealth":
		h.describeTargetHealth(w, r)
	default:
		awsquery.WriteXMLError(w, http.StatusBadRequest,
			"InvalidAction", "unknown ELBv2 action: "+action)
	}
}

// writeErr maps cloudemu errors to ELBv2 XML error responses.
func writeErr(w http.ResponseWriter, err error) {
	switch {
	case cerrors.IsNotFound(err):
		awsquery.WriteXMLError(w, http.StatusBadRequest, notFoundCode(err), err.Error())
	case cerrors.IsAlreadyExists(err):
		awsquery.WriteXMLError(w, http.StatusBadRequest, "DuplicateLoadBalancerName", err.Error())
	case cerrors.IsInvalidArgument(err):
		awsquery.WriteXMLError(w, http.StatusBadRequest, "ValidationError", err.Error())
	case cerrors.IsFailedPrecondition(err):
		awsquery.WriteXMLError(w, http.StatusBadRequest, "ResourceInUse", err.Error())
	default:
		awsquery.WriteXMLError(w, http.StatusInternalServerError, "InternalFailure", err.Error())
	}
}

// notFoundCode picks the AWS-shaped error code from the error message. The
// message always carries the resource keyword.
func notFoundCode(err error) string {
	// Order matters: a rule's not-found message embeds the parent listener ARN
	// (which contains "listener"), so "rule" must be checked before "listener"
	// or a RuleNotFound would be misclassified as ListenerNotFound. Likewise
	// "target group" before "target".
	msg := err.Error()

	switch {
	case strings.Contains(msg, "load balancer"):
		return "LoadBalancerNotFound"
	case strings.Contains(msg, "target group"):
		return "TargetGroupNotFound"
	case strings.Contains(msg, "rule"):
		return "RuleNotFound"
	case strings.Contains(msg, "listener"):
		return "ListenerNotFound"
	case strings.Contains(msg, "target"):
		return "TargetGroupNotFound"
	default:
		return "ResourceNotFound"
	}
}
