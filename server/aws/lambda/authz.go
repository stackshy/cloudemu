package lambda

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/authctx"
	"github.com/stackshy/cloudemu/v2/server/wire/awsauthz"
	sdrv "github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

// serviceName is the IAM service prefix of every Lambda action, and the
// service field of Lambda ARNs.
const serviceName = "lambda"

// Function URL auth types.
const urlAuthNone = "NONE"

const (
	// bodyMessage is the message field of a Lambda error body.
	bodyMessage = "Message"
	// bodyType is the error type field of a Lambda error body.
	bodyType = "Type"
	// condTrue is the value of a true Bool condition key.
	condTrue = "true"
)

// functionURLForbidden is the body Lambda returns when a call through a
// function URL is not authorized.
const functionURLForbidden = "Forbidden. For troubleshooting Function URL authorization issues, see: " +
	"https://docs.aws.amazon.com/lambda/latest/dg/urls-auth.html"

// policyStatementLister reads the statements of a function's resource-based
// policy (AWS-only, asserted like policyManager).
type policyStatementLister interface {
	PolicyStatements(ctx context.Context, functionName, qualifier string) (string, []sdrv.PermissionStatement, error)
}

// WithEnforceAuth tells the handler the server authenticates and authorizes
// requests (EnforceAuth). The handler then also enforces the parts of Lambda
// authorization only it can decide: the public-access grant a function URL
// with AuthType NONE needs in the function's resource-based policy.
func WithEnforceAuth(on bool) Option {
	return func(h *Handler) { h.enforce = on }
}

// IAMService returns the IAM service prefix of the operations this handler
// serves.
func (*Handler) IAMService() string { return serviceName }

// PublicRequest reports whether r is a call through a function URL whose
// AuthType is NONE. Lambda does not authenticate those calls (Lambda
// Developer Guide, "Control access to Lambda function URLs"), so the gate lets
// them through unsigned; the handler then requires the public grant.
func (h *Handler) PublicRequest(r *http.Request) bool {
	if op, _ := classify(r); op != opInvokeFunctionURL {
		return false
	}

	cfg, ok := h.resolveURL(r)

	return ok && cfg.AuthType == urlAuthNone
}

// WriteAccessDenied writes the 403 Lambda returns when IAM denies a call:
// AccessDeniedException with a {"Type":"User","Message":...} body, or the
// function URL Forbidden response for a call through a function URL.
func (*Handler) WriteAccessDenied(w http.ResponseWriter, r *http.Request, msg string) {
	if isFunctionURLHost(r.Host) {
		writeFunctionURLForbidden(w)
		return
	}

	writeAccessDenied(w, msg)
}

func writeAccessDenied(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.Header().Set("X-Amzn-Errortype", "AccessDeniedException")
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(map[string]string{bodyType: "User", bodyMessage: msg})
}

func writeFunctionURLForbidden(w http.ResponseWriter) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.Header().Set("X-Amzn-Errortype", "AccessDeniedException")
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(map[string]string{bodyMessage: functionURLForbidden})
}

// resolveURL returns the function URL config r's host addresses.
func (h *Handler) resolveURL(r *http.Request) (*sdrv.FunctionURLConfig, bool) {
	resolver, ok := h.fn.(functionURLResolver)
	if !ok {
		return nil, false
	}

	cfg, err := resolver.ResolveFunctionURL(r.Context(), requestHost(r.Host))

	return cfg, err == nil && cfg != nil
}

// functionPolicy is a function's resource-based policy for one qualifier.
type functionPolicy struct {
	statements []sdrv.PermissionStatement
}

// policyStatements reads the resource-based policy of name for qualifier.
func (h *Handler) policyStatements(r *http.Request, name, qualifier string) (functionPolicy, bool) {
	lister, ok := h.fn.(policyStatementLister)
	if !ok {
		return functionPolicy{}, false
	}

	_, stmts, err := lister.PolicyStatements(r.Context(), name, qualifier)
	if err != nil {
		return functionPolicy{}, false
	}

	return functionPolicy{statements: stmts}, true
}

// authorizeInvoke finishes the lambda:InvokeFunction check of a direct
// Invoke. Within one account a call is allowed when either the caller's
// identity policies or the function's resource-based policy allow it, and
// neither denies it (IAM User Guide, "Policy evaluation logic"). The gate has
// already refused an explicit identity deny, and recorded the identity
// decision; an implicit deny is overturned only by a resource-policy
// statement that names the caller. It returns the deny message, or "".
func (h *Handler) authorizeInvoke(r *http.Request, name, qualifier string) string {
	ev, ok := awsauthz.EvaluationFrom(r.Context())
	if !ok {
		return ""
	}

	return h.finishResourcePolicy(r, &ev, name, qualifier, actionInvokeFunction)
}

// authorizeURLInvoke decides a call through a function URL. An AWS_IAM URL
// needs lambda:InvokeFunctionUrl and lambda:InvokeFunction, each from the
// caller's identity policies or the function's resource-based policy. A NONE
// URL is not authenticated, so under EnforceAuth both actions must be granted
// to everyone ("*") by the resource-based policy; since October 2025 Lambda
// requires both for new function URLs.
func (h *Handler) authorizeURLInvoke(r *http.Request, cfg *sdrv.FunctionURLConfig) bool {
	if cfg.AuthType == urlAuthNone {
		if !h.enforce {
			return true
		}

		return h.publicURLGrant(r, cfg)
	}

	ev, ok := awsauthz.EvaluationFrom(r.Context())
	if !ok {
		return !h.enforce
	}

	for _, act := range []string{actionInvokeFunctionURL, actionInvokeFunction} {
		if h.finishResourcePolicy(r, &ev, cfg.FunctionName, cfg.Qualifier, act) != "" {
			return false
		}
	}

	return true
}

// publicURLGrant reports whether the resource-based policy for the URL's
// qualifier grants lambda:InvokeFunctionUrl and lambda:InvokeFunction to
// everyone, under the context of a call through a NONE function URL.
func (h *Handler) publicURLGrant(r *http.Request, cfg *sdrv.FunctionURLConfig) bool {
	pol, ok := h.policyStatements(r, cfg.FunctionName, cfg.Qualifier)
	if !ok {
		return false
	}

	cctx := map[string]string{condFunctionURLAuth: urlAuthNone, condInvokedViaURL: condTrue}

	for _, act := range []string{actionInvokeFunctionURL, actionInvokeFunction} {
		if !pol.grants(act, nil, cctx) {
			return false
		}
	}

	return true
}

// finishResourcePolicy returns "" when the identity decision the gate
// recorded for act allows the call, or the function's resource-based policy
// grants act to the caller; otherwise the deny message.
func (h *Handler) finishResourcePolicy(r *http.Request, ev *awsauthz.Evaluation, name, qualifier, act string) string {
	for c, d := range ev.Decisions {
		if c.Action != act {
			continue
		}

		switch d {
		case awsauthz.Allowed:
			return ""
		case awsauthz.ExplicitDeny:
			return denyMessage(&ev.Principal, act, c.Resource, true)
		case awsauthz.ImplicitDeny:
			if pol, ok := h.policyStatements(r, name, qualifier); ok && pol.grants(act, &ev.Principal, ev.CondCtx) {
				return ""
			}

			return denyMessage(&ev.Principal, act, c.Resource, false)
		}
	}

	return denyMessage(&ev.Principal, act, "", false)
}

// denyMessage is the AWS AccessDenied message for act on resource.
func denyMessage(p *authctx.Principal, act, resource string, explicit bool) string {
	if resource == "" {
		resource = "*"
	}

	msg := "User: " + p.ARN + " is not authorized to perform: " + act + " on resource: " + resource
	if explicit {
		return msg + " with an explicit deny in an identity-based policy"
	}

	return msg + " because no identity-based policy allows the " + act + " action"
}

// grants reports whether a statement of the policy allows act for caller
// (nil for an anonymous call through a NONE function URL) under cctx.
func (p functionPolicy) grants(act string, caller *authctx.Principal, cctx map[string]string) bool {
	for i := range p.statements {
		st := &p.statements[i]
		if actionMatches(st.Action, act) && principalGranted(st.Principal, caller) && conditionsHold(st, cctx) {
			return true
		}
	}

	return false
}

// actionMatches reports whether a statement Action (AddPermission accepts
// "lambda:<Action>", "lambda:*" or "*") covers act.
func actionMatches(stmtAction, act string) bool {
	return stmtAction == "*" || strings.EqualFold(stmtAction, serviceName+":*") || strings.EqualFold(stmtAction, act)
}

// principalGranted reports whether a statement Principal grants access to
// caller on its own. "*" grants everyone, and the exact ARN of an IAM user or
// a role session grants that caller. A grant to the account (an account id or
// root ARN) only delegates to the account's IAM policies, and a service
// principal never matches a signed IAM caller, so neither grants anything
// here. A grant to a role ARN is limited by the role's permissions boundary,
// which this handler cannot read, so it is not applied (the caller then needs
// an identity-based allow). A nil caller (an anonymous call) only matches "*".
func principalGranted(principal string, caller *authctx.Principal) bool {
	if principal == "*" {
		return true
	}

	return caller != nil && caller.ARN != "" && strings.HasPrefix(principal, "arn:") && principal == caller.ARN
}

// conditionsHold evaluates the Condition AddPermission attached to st. A key
// the request does not carry fails its condition, as in IAM: a signed caller
// has no aws:SourceArn, aws:SourceAccount, aws:PrincipalOrgID or
// lambda:EventSourceToken, so a grant scoped by them never applies to it.
func conditionsHold(st *sdrv.PermissionStatement, cctx map[string]string) bool {
	required := map[string]string{
		"aws:SourceArn":           st.SourceARN,
		"aws:SourceAccount":       st.SourceAccount,
		"aws:PrincipalOrgID":      st.PrincipalOrgID,
		"lambda:EventSourceToken": st.EventSourceToken,
		condFunctionURLAuth:       st.FunctionURLAuthType,
	}

	if st.InvokedViaFunctionURL {
		required[condInvokedViaURL] = condTrue
	}

	for key, want := range required {
		if want == "" {
			continue
		}

		if got, ok := cctx[key]; !ok || !strings.EqualFold(got, want) {
			return false
		}
	}

	return true
}
