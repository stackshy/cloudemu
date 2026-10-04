package iam

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/iam/driver"
)

// assumeRoleAction is the action a trust policy must allow for a principal to
// assume the role.
const assumeRoleAction = "sts:AssumeRole"

// trustPolicyDoc mirrors policyDoc but carries the Principal field a role trust
// policy uses in place of Resource. The identity-policy evaluatePolicy engine
// matches on Action+Resource, so trust evaluation reuses its Action matching
// (matchesAction) but resolves the trusted party through Principal here.
type trustPolicyDoc struct {
	Statement []trustStatement `json:"Statement"`
}

type trustStatement struct {
	Effect       string                    `json:"Effect"`
	Action       any                       `json:"Action"`
	Principal    any                       `json:"Principal"`
	NotPrincipal any                       `json:"NotPrincipal"`
	Condition    map[string]map[string]any `json:"Condition"`
}

// EvaluateTrust evaluates the trust policy of req.RoleName for a real caller.
// It implements driver.TrustEvaluator.
//
// Only an "AWS" principal (or the string "*") can match a caller that signed
// with SigV4; "Service", "Federated" and "CanonicalUser" never do. Within
// "AWS", "*" matches anyone, the account root ARN or a bare account id
// matches any caller in that account, and an exact caller ARN names the
// caller directly. ARNs are not wildcard-matched. A Deny with NotPrincipal
// applies to every caller it does not list (see notPrincipalExcludes).
// Conditions are evaluated against req.Context plus the role's tags as
// aws:ResourceTag/<key>.
//
// The decision between trust and identity policies is left to the caller,
// which needs NamedDirectly for it: within one account a trust policy that
// names the caller's ARN is enough on its own, while one that names the
// account still needs an identity-based allow.
func (m *Mock) EvaluateTrust(_ context.Context, req *driver.TrustRequest) driver.TrustResult {
	r, ok := m.roles.Get(req.RoleName)
	if !ok {
		return driver.TrustResult{}
	}

	res := driver.TrustResult{RoleExists: true}

	var pd trustPolicyDoc
	if err := json.Unmarshal([]byte(r.AssumeRolePolicyDoc), &pd); err != nil {
		return res
	}

	cctx := trustConditionContext(req.Context, r.Tags)

	for i := range pd.Statement {
		stmt := &pd.Statement[i]
		if !matchesAction(toStringSlice(stmt.Action), req.Action) {
			continue
		}

		deny := strings.EqualFold(stmt.Effect, "Deny")

		matched, named := stmt.callerMatches(req, deny)
		if !matched || !evaluateConditions(stmt.Condition, cctx) {
			continue
		}

		switch {
		case deny:
			res.ExplicitDeny = true
		case strings.EqualFold(stmt.Effect, "Allow"):
			res.Allow = true
			res.NamedDirectly = res.NamedDirectly || named
		}
	}

	return res
}

// trustConditionContext is the request context plus the role's tags.
func trustConditionContext(reqCtx, roleTags map[string]string) ConditionContext {
	cctx := make(ConditionContext, len(reqCtx)+len(roleTags))

	for k, v := range roleTags {
		cctx["aws:ResourceTag/"+k] = v
	}

	for k, v := range reqCtx {
		cctx[k] = v
	}

	return cctx
}

// callerMatches reports whether the statement applies to the caller, and
// whether it names one of the caller's ARNs exactly. NotPrincipal is only
// honored on a Deny; an Allow with NotPrincipal grants nothing.
func (s *trustStatement) callerMatches(req *driver.TrustRequest, deny bool) (matched, named bool) {
	if s.Principal != nil {
		return awsPrincipalMatches(s.Principal, req)
	}

	if deny && s.NotPrincipal != nil {
		return !notPrincipalExcludes(s.NotPrincipal, req), false
	}

	return false, false
}

// awsPrincipalMatches matches a Principal element against a SigV4 caller.
func awsPrincipalMatches(principal any, req *driver.TrustRequest) (matched, named bool) {
	switch p := principal.(type) {
	case string:
		return p == "*", false
	case map[string]any:
		for _, entry := range toStringSlice(p["AWS"]) {
			switch {
			case slices.Contains(req.CallerARNs, entry):
				return true, true
			case entry == "*" || namesAccount(entry, req.CallerAccount):
				matched = true
			}
		}
	}

	return matched, false
}

// namesAccount reports whether entry is account, or its root ARN in any
// partition.
func namesAccount(entry, account string) bool {
	if account == "" {
		return false
	}

	if entry == account {
		return true
	}

	rest, ok := strings.CutPrefix(entry, "arn:")
	if !ok {
		return false
	}

	_, tail, ok := strings.Cut(rest, ":")

	return ok && tail == "iam::"+account+":root"
}

// notPrincipalExcludes reports whether a NotPrincipal element spares the
// caller. As in AWS, it must list every principal in the caller's chain: each
// of the caller's ARNs (a role session's role and session) and the account.
// Listing only some of them leaves the caller subject to the Deny.
func notPrincipalExcludes(notPrincipal any, req *driver.TrustRequest) bool {
	var listed []string

	switch p := notPrincipal.(type) {
	case string:
		return p == "*"
	case map[string]any:
		listed = toStringSlice(p["AWS"])
	}

	if slices.Contains(listed, "*") {
		return true
	}

	accountListed := slices.ContainsFunc(listed, func(e string) bool { return namesAccount(e, req.CallerAccount) })
	if !accountListed || len(req.CallerARNs) == 0 {
		return false
	}

	for _, arn := range req.CallerARNs {
		if !slices.Contains(listed, arn) && !namesAccount(arn, req.CallerAccount) {
			return false
		}
	}

	return true
}

// EvaluateAssumeRoleTrust reports whether callerPrincipal may assume the role
// named roleName under the role's trust policy (STS AssumeRole). roleExists is
// false when the role does not exist; the caller maps both a missing role and a
// disallowing trust policy to AccessDenied. An explicit Deny overrides any
// Allow. AWS-only; not part of the portable IAM driver.
func (m *Mock) EvaluateAssumeRoleTrust(_ context.Context, roleName, callerPrincipal string) (roleExists, allowed bool) {
	r, ok := m.roles.Get(roleName)
	if !ok {
		return false, false
	}

	allow, deny := evaluateTrustPolicy(r.AssumeRolePolicyDoc, callerPrincipal)

	return true, allow && !deny
}

// evaluateTrustPolicy evaluates a role trust document for sts:AssumeRole against
// callerPrincipal, returning whether any statement allows and whether any
// statement denies. A malformed document allows nothing.
func evaluateTrustPolicy(doc, callerPrincipal string) (allow, deny bool) {
	var pd trustPolicyDoc
	if err := json.Unmarshal([]byte(doc), &pd); err != nil {
		return false, false
	}

	for _, stmt := range pd.Statement {
		if !matchesAction(toStringSlice(stmt.Action), assumeRoleAction) {
			continue
		}

		if !trustPrincipalMatches(stmt.Principal, callerPrincipal) {
			continue
		}

		if strings.EqualFold(stmt.Effect, "Deny") {
			deny = true
		} else if strings.EqualFold(stmt.Effect, "Allow") {
			allow = true
		}
	}

	return allow, deny
}

// trustPrincipalMatches reports whether the caller matches a statement's
// Principal. Principal may be the string "*", or an object keyed by principal
// type ("AWS", "Service", "Federated") whose values are a string or a list.
// Since cloudemu does not verify SigV4, the caller is the account-root identity
// derived in the STS handler; an entry matches when it is "*", the account root
// ARN, or a wildcard match of the caller.
func trustPrincipalMatches(principal any, caller string) bool {
	switch p := principal.(type) {
	case string:
		return principalEntryMatches(p, caller)
	case map[string]any:
		for _, v := range p {
			for _, entry := range toStringSlice(v) {
				if principalEntryMatches(entry, caller) {
					return true
				}
			}
		}
	}

	return false
}

// principalEntryMatches matches a single principal entry against the caller.
func principalEntryMatches(entry, caller string) bool {
	if entry == "*" {
		return true
	}

	if entry == caller {
		return true
	}

	// A trust policy that names the account root trusts every principal in that
	// account; the caller is the account root, so an exact match already covers
	// it. Fall back to wildcard matching for patterns like "arn:...:role/*".
	return globMatch(entry, caller)
}
