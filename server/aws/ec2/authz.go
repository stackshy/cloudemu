package ec2

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/awsauthz"
	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
)

// autoScalingService is the IAM prefix of the Auto Scaling actions this
// handler also serves.
const autoScalingService = "autoscaling"

// IAMChecks names the IAM action from the form Action that ServeHTTP
// dispatches on. Actions in autoScalingRoutes are autoscaling:, the rest
// ec2:. An Action the handler does not know is authorized as ec2:<Action> and
// then answered with InvalidAction, so nothing runs.
func (h *Handler) IAMChecks(r *http.Request, _ awsauthz.Scope) ([]awsauthz.Check, bool) {
	checks, ok := awsauthz.QueryChecks(r, h.IAMService())
	if !ok {
		return nil, false
	}

	if action := r.Form.Get("Action"); autoScalingRoutes[action] != nil {
		return awsauthz.Single(autoScalingService+":"+action, ""), true
	}

	return checks, true
}

// WriteAccessDenied writes the 403 for a call IAM denies: EC2's
// UnauthorizedOperation, or AccessDenied for an Auto Scaling action, in the
// query error envelope.
func (*Handler) WriteAccessDenied(w http.ResponseWriter, r *http.Request, msg string) {
	// The gate hands over the request IAMChecks already parsed.
	if r.Form != nil && autoScalingRoutes[r.Form.Get("Action")] != nil {
		awsquery.WriteXMLError(w, http.StatusForbidden, "AccessDenied", msg)
		return
	}

	awsquery.WriteXMLError(w, http.StatusForbidden, "UnauthorizedOperation",
		"You are not authorized to perform this operation. "+msg)
}
