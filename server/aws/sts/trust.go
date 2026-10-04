package sts

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/authctx"
	"github.com/stackshy/cloudemu/v2/server/wire/awsauthz"
	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
)

// The trust actions an AssumeRole call can need.
const (
	trustAssumeRole        = "sts:AssumeRole"
	trustTagSession        = "sts:TagSession"
	trustSetSourceIdentity = "sts:SetSourceIdentity"
)

// maxSessionTags is the most session tags AssumeRole accepts. Reading stops
// there, so a long form cannot make the handler loop.
const maxSessionTags = 50

// callerTrusted decides an AssumeRole call under EnforceAuth for the caller
// the gate authenticated, from the role's trust policy and the identity
// decisions the gate recorded in ev.
//
// Each trust action the call needs (sts:AssumeRole, plus sts:TagSession when
// it passes tags and sts:SetSourceIdentity when it passes a source identity)
// must be allowed by the trust policy, must not be explicitly denied by the
// trust or identity policies, and must be allowed by an identity policy unless
// the trust policy names the caller directly. Within one account a trust
// policy that names the caller's ARN is enough on its own, while one that
// names the account still needs an identity-based allow (IAM User Guide,
// "Policy evaluation logic", and "How AWS enforcement code logic evaluates
// requests to allow or deny access": role trust policies must explicitly allow
// the principal).
//
// RoleArn must be the ARN of an existing role in this account (STS-X2).
func (h *Handler) callerTrusted(r *http.Request, ev *awsauthz.Evaluation, roleArn, roleName string) bool {
	if h.trustEval == nil {
		return false
	}

	role, ok := h.requestedRole(r, roleArn, roleName)
	if !ok {
		return false
	}

	ctx := r.Context()
	callers, account := h.trustCallers(ctx, &ev.Principal)
	cctx := trustContext(r, ev, callers)

	for _, action := range trustActions(r) {
		res := h.trustEval.EvaluateTrust(ctx, &iamdriver.TrustRequest{
			RoleName: role.Name, Action: action, CallerARNs: callers, CallerAccount: account, Context: cctx,
		})

		identity := identityDecision(ev, action)

		switch {
		case !res.RoleExists, !res.Allow, res.ExplicitDeny, identity == awsauthz.ExplicitDeny:
			return false
		case identity != awsauthz.Allowed && !res.NamedDirectly:
			return false
		}
	}

	return true
}

// requestedRole returns the role RoleArn names. It must be a role of this
// IAM, with RoleArn equal to its ARN: a RoleArn for another account, or with
// a different path, names a different role, which does not exist here.
func (h *Handler) requestedRole(r *http.Request, roleArn, roleName string) (*iamdriver.RoleInfo, bool) {
	if h.roles == nil {
		return nil, false
	}

	role, err := h.roles.GetRole(r.Context(), roleName)
	if err != nil || role == nil || role.ARN != roleArn {
		return nil, false
	}

	return role, true
}

// trustCallers returns the ARNs the caller is known by in a trust policy,
// and its account. A role session is known by its role's ARN and by its
// assumed-role ARN; an IAM user, a GetSessionToken session (which acts as its
// user) and a federated user by their own ARN; the account root by the root
// ARN.
func (h *Handler) trustCallers(ctx context.Context, p *authctx.Principal) (arns []string, account string) {
	account = p.AccountID
	if account == "" {
		account = h.accountID
	}

	if p.ARN == "" || strings.HasSuffix(p.ARN, ":root") {
		return []string{"arn:aws:iam::" + account + ":root"}, account
	}

	if h.sessions != nil {
		if sess, ok := h.sessions.Lookup(p.AccessKeyID); ok && sess.Owner.Kind == KindRole && h.roles != nil {
			if role, err := h.roles.GetRole(ctx, sess.Owner.PolicyEntity); err == nil && role != nil {
				return []string{role.ARN, p.ARN}, account
			}
		}
	}

	return []string{p.ARN}, account
}

// trustActions lists the trust actions an AssumeRole request needs.
func trustActions(r *http.Request) []string {
	actions := []string{trustAssumeRole}

	keys, _ := requestTags(r)
	if len(keys) > 0 || r.Form.Get("TransitiveTagKeys.member.1") != "" {
		actions = append(actions, trustTagSession)
	}

	if r.Form.Get("SourceIdentity") != "" {
		actions = append(actions, trustSetSourceIdentity)
	}

	return actions
}

// requestTags reads the session tags of an AssumeRole request
// (Tags.member.N.Key / .Value), in order.
func requestTags(r *http.Request) (keys []string, tags map[string]string) {
	tags = map[string]string{}

	for i := 1; i <= maxSessionTags; i++ {
		prefix := "Tags.member." + strconv.Itoa(i)

		key := r.Form.Get(prefix + ".Key")
		if key == "" {
			break
		}

		keys = append(keys, key)
		tags[key] = r.Form.Get(prefix + ".Value")
	}

	return keys, tags
}

// trustContext is the condition context a trust policy is evaluated with: the
// gate's context, plus the STS request keys. A role session's aws:PrincipalArn
// is its role's ARN, as in AWS.
func trustContext(r *http.Request, ev *awsauthz.Evaluation, callers []string) map[string]string {
	cctx := make(map[string]string, len(ev.CondCtx))
	for k, v := range ev.CondCtx {
		cctx[k] = v
	}

	set := func(key, value string) {
		if value != "" {
			cctx[key] = value
		}
	}

	if len(callers) > 1 {
		cctx["aws:PrincipalArn"] = callers[0]
	}

	set("sts:RoleSessionName", r.Form.Get("RoleSessionName"))
	set("sts:ExternalId", r.Form.Get("ExternalId"))
	set("sts:SourceIdentity", r.Form.Get("SourceIdentity"))

	keys, tags := requestTags(r)
	for k, v := range tags {
		cctx["aws:RequestTag/"+k] = v
	}

	set("aws:TagKeys", strings.Join(keys, iamdriver.ConditionValueSeparator))
	set("sts:TransitiveTagKeys", strings.Join(memberList(r, "TransitiveTagKeys"), iamdriver.ConditionValueSeparator))

	return cctx
}

// memberList reads a query-protocol list parameter (<name>.member.N).
func memberList(r *http.Request, name string) []string {
	var out []string

	for i := 1; i <= maxSessionTags; i++ {
		v := r.Form.Get(name + ".member." + strconv.Itoa(i))
		if v == "" {
			break
		}

		out = append(out, v)
	}

	return out
}

// identityDecision is the identity decision the gate recorded for action. A
// missing decision is an implicit deny.
func identityDecision(ev *awsauthz.Evaluation, action string) awsauthz.Decision {
	for c, d := range ev.Decisions {
		if c.Action == action {
			return d
		}
	}

	return awsauthz.ImplicitDeny
}

// assumeDeniedMessage is the AccessDenied message of a refused AssumeRole. It
// names the RoleArn as sent and reads the same whatever the reason, so it does
// not reveal whether the role exists. Without EnforceAuth the caller is
// unknown and the message keeps its old form.
func assumeDeniedMessage(r *http.Request, roleArn string) string {
	ev, enforced := awsauthz.EvaluationFrom(r.Context())
	if !enforced {
		return "User is not authorized to perform sts:AssumeRole on " + roleArn
	}

	caller := ev.Principal.ARN
	if caller == "" {
		caller = ev.Principal.UserName
	}

	return "User: " + caller + " is not authorized to perform: " + trustAssumeRole + " on resource: " + roleArn
}

// refusedUnderEnforceAuth refuses a signed AssumeRoleWithWebIdentity or
// AssumeRoleWithSAML under EnforceAuth: the token or assertion is not
// validated, so no caller may use it to get a role's credentials. It writes
// the 403 and reports true when it refused.
func refusedUnderEnforceAuth(w http.ResponseWriter, r *http.Request, action, until string) bool {
	if _, enforced := awsauthz.EvaluationFrom(r.Context()); !enforced {
		return false
	}

	awsquery.WriteXMLError(w, http.StatusForbidden, "AccessDenied",
		action+" is not available under --enforce-auth until "+until)

	return true
}
