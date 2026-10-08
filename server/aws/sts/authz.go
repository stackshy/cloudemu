package sts

import (
	"context"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/awsauthz"
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
)

// roleGetter looks up the role an AssumeRole-family call names.
type roleGetter interface {
	GetRole(ctx context.Context, name string) (*iamdriver.RoleInfo, error)
}

// IAMChecks names the IAM permission each STS action needs, reading the form
// Action that ServeHTTP dispatches on.
//
//   - GetCallerIdentity needs none: AWS never authorizes it.
//   - GetSessionToken needs no permission, but an explicit Deny on
//     sts:GetSessionToken still blocks it (DenyOnly).
//   - The AssumeRole family needs sts:<Action> on the role that will be
//     assumed. The resource is the role's ARN when RoleArn names an existing
//     role exactly; otherwise it is unknown (the operation refuses such a
//     RoleArn anyway). A deny names the RoleArn as sent, like AWS, so it does
//     not reveal whether the role exists.
//   - AssumeRole's checks are ResourcePolicy checks: the handler finishes the
//     decision with the role's trust policy, which can grant the call on its
//     own when it names the caller. Passing session tags adds sts:TagSession,
//     and passing a source identity adds sts:SetSourceIdentity, on the role.
//   - GetFederationToken needs sts:GetFederationToken on the federated user.
//   - GetAccessKeyInfo and DecodeAuthorizationMessage take no resource.
func (h *Handler) IAMChecks(r *http.Request, _ awsauthz.Scope) ([]awsauthz.Check, bool) {
	checks, ok := awsauthz.QueryChecks(r, h.IAMService())
	if !ok {
		return nil, false
	}

	action := checks[0].Action

	switch r.Form.Get("Action") {
	case actionGetCallerIdentity:
		return []awsauthz.Check{}, true
	case actionGetSessionToken:
		return []awsauthz.Check{{Action: action, Resource: "*", Mode: awsauthz.DenyOnly}}, true
	case actionAssumeRole:
		return h.assumeRoleChecks(r), true
	case actionAssumeRoleWithWebIdentity, actionAssumeRoleWithSAML:
		return []awsauthz.Check{{Action: action, Resource: h.assumedRoleARN(r), MessageResource: r.Form.Get("RoleArn")}}, true
	case actionGetFederationToken:
		return awsauthz.Single(action, "arn:aws:sts::"+h.accountID+":federated-user/"+r.Form.Get("Name")), true
	case actionGetAccessKeyInfo, actionDecodeAuthorizationMessage:
		return awsauthz.Single(action, "*"), true
	default:
		return checks, true
	}
}

// The STS actions IAMChecks names a permission for.
const (
	actionGetCallerIdentity          = "GetCallerIdentity"
	actionGetSessionToken            = "GetSessionToken"
	actionAssumeRole                 = "AssumeRole"
	actionAssumeRoleWithWebIdentity  = "AssumeRoleWithWebIdentity"
	actionAssumeRoleWithSAML         = "AssumeRoleWithSAML"
	actionGetFederationToken         = "GetFederationToken"
	actionGetAccessKeyInfo           = "GetAccessKeyInfo"
	actionDecodeAuthorizationMessage = "DecodeAuthorizationMessage"
)

// assumeRoleChecks are the ResourcePolicy checks of an AssumeRole request,
// one per trust action it needs.
func (h *Handler) assumeRoleChecks(r *http.Request) []awsauthz.Check {
	resource, roleArn := h.assumedRoleARN(r), r.Form.Get("RoleArn")
	actions := trustActions(r)
	checks := make([]awsauthz.Check, 0, len(actions))

	for _, action := range actions {
		checks = append(checks, awsauthz.Check{
			Action: action, Resource: resource, Mode: awsauthz.ResourcePolicy, MessageResource: roleArn,
		})
	}

	return checks
}

// assumedRoleARN is the ARN of the role the request's RoleArn names, or ""
// when no role has exactly that ARN.
func (h *Handler) assumedRoleARN(r *http.Request) string {
	roleArn := r.Form.Get("RoleArn")

	role, ok := h.requestedRole(r, roleArn, roleNameFromArn(roleArn))
	if !ok {
		return ""
	}

	return role.ARN
}
