package iam

import (
	"context"
	"encoding/json"
	"strings"
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
	Effect    string `json:"Effect"`
	Action    any    `json:"Action"`
	Principal any    `json:"Principal"`
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
	return wildcardMatch(entry, caller)
}
