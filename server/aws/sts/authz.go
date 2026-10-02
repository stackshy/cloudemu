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
//     assumed. The role is resolved the way the operation resolves it (by the
//     last path segment of RoleArn), and its stored ARN is the resource, so a
//     RoleArn with a different path or account cannot borrow another role's
//     grant. An unknown role leaves the resource unknown.
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
	case actionAssumeRole, actionAssumeRoleWithWebIdentity, actionAssumeRoleWithSAML:
		return awsauthz.Single(action, h.assumedRoleARN(r)), true
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

// assumedRoleARN is the stored ARN of the role named by the request's
// RoleArn, or "" when that role does not exist.
func (h *Handler) assumedRoleARN(r *http.Request) string {
	if h.roles == nil {
		return ""
	}

	role, err := h.roles.GetRole(r.Context(), roleNameFromArn(r.Form.Get("RoleArn")))
	if err != nil || role == nil {
		return ""
	}

	return role.ARN
}
