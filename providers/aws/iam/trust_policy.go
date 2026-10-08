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
// caller. ARNs are not wildcard-matched. A user or role ARN only matches the
// entity the policy was saved against: IAM resolves it to the entity's unique
// id, so a user deleted and created again under the same name is no longer
// trusted (see resolveTrustPrincipals). A Deny with NotPrincipal applies to
// every caller it does not list (see notPrincipalExcludes). Conditions are
// evaluated against req.Context plus the role's tags as
// aws:ResourceTag/<key>.
//
// The decision between trust and identity policies is left to the caller,
// which needs NamedDirectly and NamedRole for it (see driver.TrustResult).
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

	tm := trustMatcher{req: req, ids: r.TrustPrincipalIDs, currentID: m.entityIDForARN}
	cctx := trustConditionContext(req.Context, r.Tags)

	for i := range pd.Statement {
		tm.apply(&pd.Statement[i], cctx, &res)
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

// The IAM entity types parsePrincipalARN reports for users and roles.
const (
	entityUser = "user"
	entityRole = "role"
)

// naming is how a matching principal entry names the caller.
type naming int

const (
	// namedNone: the entry matched the caller's account or "*".
	namedNone naming = iota
	// namedRoleARN: the entry is the IAM role ARN of a role session caller.
	// A grant to it is still limited by the role's permissions boundary.
	namedRoleARN
	// namedPrincipal: the entry is the caller's own IAM user, role session or
	// federated user ARN.
	namedPrincipal
)

// trustMatcher matches trust policy principals against one caller.
type trustMatcher struct {
	req *driver.TrustRequest
	// ids holds the unique id each user or role ARN in the policy resolved
	// to when the policy was saved.
	ids map[string]string
	// currentID resolves a user or role ARN to its entity's unique id now.
	currentID func(arn string) string
}

// apply folds one trust statement into res when it applies to the request.
func (tm *trustMatcher) apply(stmt *trustStatement, cctx ConditionContext, res *driver.TrustResult) {
	if !matchesAction(toStringSlice(stmt.Action), tm.req.Action) {
		return
	}

	deny := strings.EqualFold(stmt.Effect, "Deny")

	matched, named := tm.statementMatches(stmt, deny)
	if !matched || !evaluateConditions(stmt.Condition, cctx) {
		return
	}

	switch {
	case deny:
		res.ExplicitDeny = true
	case strings.EqualFold(stmt.Effect, "Allow"):
		res.Allow = true
		res.NamedDirectly = res.NamedDirectly || named == namedPrincipal
		res.NamedRole = res.NamedRole || named == namedRoleARN
	}
}

// statementMatches reports whether the statement applies to the caller, and
// how it names the caller. NotPrincipal is only honored on a Deny; an Allow
// with NotPrincipal grants nothing.
func (tm *trustMatcher) statementMatches(s *trustStatement, deny bool) (matched bool, named naming) {
	if s.Principal != nil {
		return tm.principalMatches(s.Principal)
	}

	if deny && s.NotPrincipal != nil {
		return !tm.notPrincipalExcludes(s.NotPrincipal), namedNone
	}

	return false, namedNone
}

// principalMatches matches a Principal element against a SigV4 caller.
func (tm *trustMatcher) principalMatches(principal any) (matched bool, named naming) {
	switch p := principal.(type) {
	case string:
		return p == "*", namedNone
	case map[string]any:
		for _, entry := range toStringSlice(p["AWS"]) {
			switch {
			case tm.isCaller(entry):
				if n := callerNaming(entry); n > named {
					named = n
				}

				matched = true
			case entry == "*" || namesAccount(entry, tm.req.CallerAccount):
				matched = true
			}
		}
	}

	return matched, named
}

// isCaller reports whether entry is one of the caller's ARNs, and still names
// the same entity it named when the policy was saved.
func (tm *trustMatcher) isCaller(entry string) bool {
	if !slices.Contains(tm.req.CallerARNs, entry) {
		return false
	}

	saved, ok := tm.ids[entry]

	return !ok || tm.currentID(entry) == saved
}

// callerNaming classifies a caller ARN named by a trust policy.
func callerNaming(arn string) naming {
	if t, _ := parsePrincipalARN(arn); t == entityRole && strings.Contains(arn, ":iam::") {
		return namedRoleARN
	}

	return namedPrincipal
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
func (tm *trustMatcher) notPrincipalExcludes(notPrincipal any) bool {
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

	account := tm.req.CallerAccount

	accountListed := slices.ContainsFunc(listed, func(e string) bool { return namesAccount(e, account) })
	if !accountListed || len(tm.req.CallerARNs) == 0 {
		return false
	}

	for _, arn := range tm.req.CallerARNs {
		if namesAccount(arn, account) {
			continue
		}

		if !slices.Contains(listed, arn) || !tm.isCaller(arn) {
			return false
		}
	}

	return true
}

// resolveTrustPrincipals maps each IAM user or role ARN that a trust policy
// names in an "AWS" principal (or NotPrincipal) to the unique id of the
// entity it names now. IAM does this when the policy is saved, so the policy
// keeps naming that entity and not a later one with the same name. ARNs that
// name no existing entity are left out and match by ARN.
func (m *Mock) resolveTrustPrincipals(doc string) map[string]string {
	var pd trustPolicyDoc
	if err := json.Unmarshal([]byte(doc), &pd); err != nil {
		return nil
	}

	ids := map[string]string{}

	for i := range pd.Statement {
		for _, p := range []any{pd.Statement[i].Principal, pd.Statement[i].NotPrincipal} {
			pm, ok := p.(map[string]any)
			if !ok {
				continue
			}

			for _, entry := range toStringSlice(pm["AWS"]) {
				if id := m.entityIDForARN(entry); id != "" {
					ids[entry] = id
				}
			}
		}
	}

	if len(ids) == 0 {
		return nil
	}

	return ids
}

// entityIDForARN returns the unique id of the IAM user or role whose ARN is
// exactly arn, or "".
func (m *Mock) entityIDForARN(arn string) string {
	switch t, name := parsePrincipalARN(arn); t {
	case entityUser:
		if u, ok := m.users.Get(name); ok && u.ARN == arn {
			return u.ID
		}
	case entityRole:
		if r, ok := m.roles.Get(name); ok && r.ARN == arn {
			return r.ID
		}
	}

	return ""
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
