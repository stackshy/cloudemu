package iam

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/iam/driver"
)

// IAM policy simulation is AWS-only, so these methods are not part of the
// portable driver; the wire layer reaches them via a type assertion on the
// Mock. The evaluation reuses the same wildcard matcher CheckPermission uses.

// Policy-evaluation decisions. These are the AWS SimulatePolicy EvalDecision
// values and are shared by CheckPermission so authorization matches simulation.
const (
	decisionAllowed      = "allowed"
	decisionExplicitDeny = "explicitDeny"
	decisionImplicitDeny = "implicitDeny"
)

// SimulatePrincipalPolicy evaluates the policies attached to the principal
// named by policySourceARN (plus any extra policy documents) against each
// action/resource pair (IAM SimulatePrincipalPolicy).
func (m *Mock) SimulatePrincipalPolicy(
	_ context.Context, policySourceARN string, actions, resourceARNs, extraPolicies []string, condCtx map[string]string,
) ([]driver.SimulationResult, error) {
	entityType, name := parsePrincipalARN(policySourceARN)
	docs := m.gatherPrincipalDocs(entityType, name)
	docs = append(docs, extraPolicies...)

	return simulate(docs, actions, resourceARNs, ConditionContext(condCtx)), nil
}

// SimulateCustomPolicy evaluates a set of standalone policy documents against
// each action/resource pair (IAM SimulateCustomPolicy).
func (*Mock) SimulateCustomPolicy(
	_ context.Context, policyDocs, actions, resourceARNs []string, condCtx map[string]string,
) ([]driver.SimulationResult, error) {
	return simulate(policyDocs, actions, resourceARNs, ConditionContext(condCtx)), nil
}

// simulate evaluates every action against every resource (defaulting to "*"
// when no resources are given) and reports the resulting decision, applying the
// supplied request condition context to each statement's Condition block.
func simulate(docs, actions, resourceARNs []string, cctx ConditionContext) []driver.SimulationResult {
	resources := resourceARNs
	if len(resources) == 0 {
		resources = []string{"*"}
	}

	results := make([]driver.SimulationResult, 0, len(actions)*len(resources))

	for _, action := range actions {
		for _, resource := range resources {
			results = append(results, driver.SimulationResult{
				ActionName:   action,
				ResourceName: resource,
				Decision:     decide(docs, action, resource, cctx),
			})
		}
	}

	return results
}

// evalMode selects how much the evaluator knows about the request.
type evalMode int

const (
	// evalKnownResource evaluates one action against one concrete resource with
	// real IAM semantics.
	evalKnownResource evalMode = iota
	// evalUnknownResource evaluates one action when the caller cannot name the
	// resource. The answer is never more permissive than it would be for any
	// concrete resource.
	evalUnknownResource
	// evalServiceWide asks whether every action of one service is allowed on
	// every resource.
	evalServiceWide
)

// evalRequest is one question for decideWith. action and resource are used by
// the per-action modes, service by evalServiceWide.
type evalRequest struct {
	action   string
	resource string
	service  string
	cctx     ConditionContext
}

// decide reduces a set of policy documents to a single simulation decision for
// one action/resource: an explicit Deny wins, then any Allow, else the default
// implicit deny.
func decide(docs []string, action, resource string, cctx ConditionContext) string {
	return decideWith(docs, evalRequest{action: action, resource: resource, cctx: cctx}, evalKnownResource)
}

// decideWith is decide for any evalMode. Known-resource mode is plain IAM
// evaluation; the other modes use evaluatePolicyConservative.
func decideWith(docs []string, req evalRequest, mode evalMode) string {
	allow, deny := false, false

	for _, doc := range docs {
		var a, d bool
		if mode == evalKnownResource {
			a, d = evaluatePolicy(doc, req.action, req.resource, req.cctx)
		} else {
			a, d = evaluatePolicyConservative(doc, req, mode)
		}

		if d {
			deny = true
		}

		if a {
			allow = true
		}
	}

	switch {
	case deny:
		return decisionExplicitDeny
	case allow:
		return decisionAllowed
	default:
		return decisionImplicitDeny
	}
}

// evaluatePolicyConservative evaluates one document when the resource is not
// known (evalUnknownResource or evalServiceWide). An Allow counts only when it
// surely covers the request: its Resource list holds "*", and every condition
// key it tests is present and satisfied. A Deny counts whenever it might apply:
// its resource is ignored, and a condition key missing from the context is
// taken as satisfied.
//
// The Deny rule for a missing key deliberately differs from real IAM, where a
// plain operator on a missing key is false for Deny too. With the resource
// unknown we cannot prove the Deny would not apply to the real request, so we
// fail closed (AUTHZ-X1 design, section 1.2). Do not "fix" this toward
// real IAM: it would turn into a Deny bypass. Known-resource mode keeps real
// semantics.
func evaluatePolicyConservative(doc string, req evalRequest, mode evalMode) (allow, deny bool) {
	var pd policyDoc
	if err := json.Unmarshal([]byte(doc), &pd); err != nil {
		return false, false
	}

	for i := range pd.Statement {
		s := &pd.Statement[i]

		switch {
		case strings.EqualFold(s.Effect, "Deny"):
			if s.denyMayApply(req, mode) {
				deny = true
			}
		case strings.EqualFold(s.Effect, "Allow"):
			if s.allowSurelyApplies(req, mode) {
				allow = true
			}
		}
	}

	return allow, deny
}

// denyMayApply reports whether a Deny statement could match some request the
// query describes. Resource and NotResource are ignored.
func (s *policyStatement) denyMayApply(req evalRequest, mode evalMode) bool {
	if mode == evalServiceWide {
		switch {
		case s.Action != nil:
			if !anyCouldMatchService(toStringSlice(s.Action), req.service) {
				return false
			}
		case s.NotAction != nil:
			// NotAction applies to everything it does not list, so the Deny
			// misses the service only when one entry covers all of svc:*.
			if anyCoversService(toStringSlice(s.NotAction), req.service) {
				return false
			}
		default:
			return false
		}
	} else if !s.actionMatches(req.action) {
		return false
	}

	return evaluateConditionsWith(s.Condition, req.cctx, absentKeyMatches)
}

// allowSurelyApplies reports whether an Allow statement matches every request
// the query describes. It needs Resource "*" (NotResource never qualifies).
func (s *policyStatement) allowSurelyApplies(req evalRequest, mode evalMode) bool {
	if s.Resource == nil || !containsStar(toStringSlice(s.Resource)) {
		return false
	}

	if mode == evalServiceWide {
		switch {
		case s.Action != nil:
			if !anyCoversService(toStringSlice(s.Action), req.service) {
				return false
			}
		case s.NotAction != nil:
			if anyCouldMatchService(toStringSlice(s.NotAction), req.service) {
				return false
			}
		default:
			return false
		}
	} else if !s.actionMatches(req.action) {
		return false
	}

	return evaluateConditionsWith(s.Condition, req.cctx, absentKeyFails)
}

func containsStar(resources []string) bool {
	for _, r := range resources {
		if r == "*" {
			return true
		}
	}

	return false
}

// anyCoversService reports whether one pattern matches every action of svc.
func anyCoversService(patterns []string, svc string) bool {
	for _, p := range patterns {
		if coversService(p, svc) {
			return true
		}
	}

	return false
}

// coversService reports whether pattern matches every "svc:Action". It holds
// when the pattern is some head followed only by '*'s and the head matches a
// prefix of "svc:": the trailing stars then take the rest of any action. So "*",
// "s3:*" and "s*" cover s3 while "s3:Get*" and "s3:?" do not. It may say no for
// an odd pattern that does cover the service ("s3:?*"), never the reverse,
// which is the safe direction for both callers.
func coversService(pattern, svc string) bool {
	pattern = strings.ToLower(pattern)
	prefix := strings.ToLower(svc) + ":"

	for i := len(pattern) - 1; i >= 0 && pattern[i] == '*'; i-- {
		head := pattern[:i]

		for k := 0; k <= len(prefix); k++ {
			if globMatch(head, prefix[:k]) {
				return true
			}
		}
	}

	return false
}

func anyCouldMatchService(patterns []string, svc string) bool {
	for _, p := range patterns {
		if couldMatchService(p, svc) {
			return true
		}
	}

	return false
}

// couldMatchService reports whether pattern might match some "svc:Action". It
// may say yes for a pattern that cannot really match, never the reverse, which
// is the safe direction for both of its callers.
func couldMatchService(pattern, svc string) bool {
	pattern, svc = strings.ToLower(pattern), strings.ToLower(svc)

	if head, _, ok := strings.Cut(pattern, ":"); ok {
		// Actions carry exactly one colon, so the pattern's first colon lines up
		// with it and the head must match the service name.
		return globMatch(head, svc)
	}

	// With no colon, only a wildcard can stand in for the separator, so the
	// text before the first '*' or '?' must be a prefix of the service name.
	wild := strings.IndexAny(pattern, "*?")
	if wild < 0 {
		return false
	}

	return strings.HasPrefix(svc, pattern[:wild])
}

// EvaluatePermission reports the tri-state decision for one action. With
// req.ResourceKnown it is exactly CheckPermissionWithContext's evaluation;
// without it the resource is treated as unknown (see evaluatePolicyConservative).
// It implements driver.PermissionEvaluator.
func (m *Mock) EvaluatePermission(_ context.Context, req driver.EvalRequest) driver.Decision {
	mode := evalUnknownResource
	if req.ResourceKnown {
		mode = evalKnownResource
	}

	q := evalRequest{action: req.Action, resource: req.Resource, cctx: ConditionContext(req.Context)}

	return driver.Decision(m.evaluatePrincipal(req.Principal, q, mode))
}

// EvaluateBoundary reports the decision of req.Principal's permissions
// boundary alone, allowed when it has none. It implements
// driver.BoundaryEvaluator.
func (m *Mock) EvaluateBoundary(_ context.Context, req driver.EvalRequest) driver.Decision {
	doc, ok := m.permissionsBoundaryDoc(m.principalEntityType(req.Principal), req.Principal)
	if !ok {
		return driver.DecisionAllowed
	}

	mode := evalUnknownResource
	if req.ResourceKnown {
		mode = evalKnownResource
	}

	q := evalRequest{action: req.Action, resource: req.Resource, cctx: ConditionContext(req.Context)}

	return driver.Decision(decideWith([]string{doc}, q, mode))
}

// EvaluateServiceWide reports whether principal may perform every action of
// service on every resource. It implements driver.PermissionEvaluator.
func (m *Mock) EvaluateServiceWide(
	_ context.Context, principal, service string, condCtx map[string]string,
) driver.Decision {
	// A name that is not a service prefix ("", "a:b", "s*") would let a
	// wildcard Allow match it, so it is never allowed.
	if !isServicePrefix(service) {
		return driver.DecisionImplicitDeny
	}

	q := evalRequest{service: service, cctx: ConditionContext(condCtx)}

	return driver.Decision(m.evaluatePrincipal(principal, q, evalServiceWide))
}

// isServicePrefix reports whether s has the shape of an IAM service prefix:
// lower-case letters, digits and hyphens, starting with a letter or digit.
func isServicePrefix(s string) bool {
	if s == "" || s[0] == '-' {
		return false
	}

	for _, c := range s {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return false
		}
	}

	return true
}

// evaluatePrincipal combines a principal's identity policies with its
// permissions boundary, both evaluated in the same mode. An explicit Deny in
// either wins; otherwise both must allow.
func (m *Mock) evaluatePrincipal(principal string, req evalRequest, mode evalMode) string {
	entityType := m.principalEntityType(principal)

	identity := decideWith(m.gatherPrincipalDocs(entityType, principal), req, mode)
	if identity == decisionExplicitDeny {
		return identity
	}

	boundary := decisionAllowed
	if doc, ok := m.permissionsBoundaryDoc(entityType, principal); ok {
		boundary = decideWith([]string{doc}, req, mode)
	}

	switch {
	case boundary == decisionExplicitDeny:
		return decisionExplicitDeny
	case identity == decisionAllowed && boundary == decisionAllowed:
		return decisionAllowed
	default:
		return decisionImplicitDeny
	}
}

// parsePrincipalARN extracts the entity type ("user", "role", or "group") and
// friendly name from an IAM principal ARN, tolerating an embedded path.
func parsePrincipalARN(arn string) (entityType, name string) {
	for _, t := range []string{"user", "role", "group"} {
		marker := ":" + t + "/"

		idx := strings.Index(arn, marker)
		if idx < 0 {
			continue
		}

		rest := arn[idx+len(marker):]
		if s := strings.LastIndexByte(rest, '/'); s >= 0 {
			rest = rest[s+1:]
		}

		return t, rest
	}

	return "", ""
}

// gatherPrincipalDocs returns every policy document in effect for a principal:
// its attached managed policies and inline policies, plus (for a user) the
// policies of every group it belongs to.
func (m *Mock) gatherPrincipalDocs(entityType, name string) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var docs []string

	switch entityType {
	case "user":
		docs = append(docs, m.managedDocsLocked(m.userPolicies[name])...)
		for _, d := range m.userInlinePolicies[name] {
			docs = append(docs, d)
		}

		for groupName, members := range m.groupUsers {
			if members[name] {
				docs = append(docs, m.groupDocsLocked(groupName)...)
			}
		}
	case "role":
		docs = append(docs, m.managedDocsLocked(m.rolePolicies[name])...)

		if rd, ok := m.roles.Get(name); ok {
			for _, d := range rd.inlinePolicies {
				docs = append(docs, d)
			}
		}
	case "group":
		docs = append(docs, m.groupDocsLocked(name)...)
	}

	return docs
}

// groupDocsLocked returns a group's managed and inline policy documents. The
// caller must hold m.mu.
func (m *Mock) groupDocsLocked(name string) []string {
	docs := m.managedDocsLocked(m.groupPolicies[name])
	for _, d := range m.groupInlinePolicies[name] {
		docs = append(docs, d)
	}

	return docs
}

// managedDocsLocked resolves a set of managed-policy ARNs to their default
// policy documents. The caller must hold m.mu.
func (m *Mock) managedDocsLocked(arns map[string]bool) []string {
	docs := make([]string, 0, len(arns))

	for arn := range arns {
		if p, ok := m.policies.Get(arn); ok && p.PolicyDocument != "" {
			docs = append(docs, p.PolicyDocument)
		}
	}

	return docs
}
