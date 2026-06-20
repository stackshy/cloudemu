// Package iam implements the AWS IAM query-protocol as a server.Handler.
// Point the real aws-sdk-go-v2 IAM client at a Server registered with this
// handler and User/Role/Policy/Group/AccessKey/InstanceProfile operations
// work against an in-memory iam driver.
//
// IAM shares the AWS query wire shape with EC2, RDS, and Redshift (POST +
// form-encoded body, XML response). To keep dispatch unambiguous, this
// handler's Matches predicate parses the form body once and only claims
// requests whose Action is one of the known IAM operations. It MUST register
// before the EC2 catch-all so iam-specific actions aren't swallowed first.
package iam

import (
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/errors"
	iamdriver "github.com/stackshy/cloudemu/iam/driver"
	"github.com/stackshy/cloudemu/server/wire/awsquery"
)

// Namespace is the XML namespace for AWS IAM responses. Matches the real
// aws-sdk-go-v2 IAM client expectations (the SDK is namespace-agnostic on
// parse but copies this value back on round-trips).
const Namespace = "https://iam.amazonaws.com/doc/2010-05-08/"

const (
	formContentType  = "application/x-www-form-urlencoded"
	maxFormBodyBytes = 1 << 20
)

// iamActions enumerates every Action this handler recognizes. Used by Matches
// to decide whether to claim a request.
var iamActions = map[string]struct{}{ //nolint:gochecknoglobals // static lookup table
	"CreateUser":                    {},
	"DeleteUser":                    {},
	"GetUser":                       {},
	"ListUsers":                     {},
	"CreateRole":                    {},
	"DeleteRole":                    {},
	"GetRole":                       {},
	"ListRoles":                     {},
	"CreatePolicy":                  {},
	"DeletePolicy":                  {},
	"GetPolicy":                     {},
	"ListPolicies":                  {},
	"CreatePolicyVersion":           {},
	"GetPolicyVersion":              {},
	"ListPolicyVersions":            {},
	"DeletePolicyVersion":           {},
	"SetDefaultPolicyVersion":       {},
	"AttachUserPolicy":              {},
	"DetachUserPolicy":              {},
	"AttachRolePolicy":              {},
	"DetachRolePolicy":              {},
	"ListAttachedUserPolicies":      {},
	"ListAttachedRolePolicies":      {},
	"CreateGroup":                   {},
	"DeleteGroup":                   {},
	"GetGroup":                      {},
	"ListGroups":                    {},
	"AddUserToGroup":                {},
	"RemoveUserFromGroup":           {},
	"ListGroupsForUser":             {},
	"CreateAccessKey":               {},
	"DeleteAccessKey":               {},
	"ListAccessKeys":                {},
	"CreateInstanceProfile":         {},
	"DeleteInstanceProfile":         {},
	"GetInstanceProfile":            {},
	"ListInstanceProfiles":          {},
	"AddRoleToInstanceProfile":      {},
	"RemoveRoleFromInstanceProfile": {},
}

// Handler serves IAM query-protocol requests.
type Handler struct {
	iam iamdriver.IAM
}

// New returns an IAM handler backed by drv.
func New(drv iamdriver.IAM) *Handler {
	return &Handler{iam: drv}
}

// Matches returns true if the request looks like an AWS IAM query-protocol
// call (POST + form-encoded body whose Action is one of the known IAM
// operations). Calling ParseForm here caches the parsed form on the request
// so ServeHTTP can use it without re-reading the body.
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

	_, ok := iamActions[r.Form.Get("Action")]

	return ok
}

// ServeHTTP dispatches on Action. The form has already been parsed by Matches.
//
//nolint:gocyclo,funlen // 34 cases for one-shot dispatch; splitting into sub-routers would add more complexity than the switch.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Form.Get("Action") {
	case "CreateUser":
		h.createUser(w, r)
	case "DeleteUser":
		h.deleteUser(w, r)
	case "GetUser":
		h.getUser(w, r)
	case "ListUsers":
		h.listUsers(w, r)
	case "CreateRole":
		h.createRole(w, r)
	case "DeleteRole":
		h.deleteRole(w, r)
	case "GetRole":
		h.getRole(w, r)
	case "ListRoles":
		h.listRoles(w, r)
	case "CreatePolicy":
		h.createPolicy(w, r)
	case "DeletePolicy":
		h.deletePolicy(w, r)
	case "GetPolicy":
		h.getPolicy(w, r)
	case "ListPolicies":
		h.listPolicies(w, r)
	case "CreatePolicyVersion":
		h.createPolicyVersion(w, r)
	case "GetPolicyVersion":
		h.getPolicyVersion(w, r)
	case "ListPolicyVersions":
		h.listPolicyVersions(w, r)
	case "DeletePolicyVersion":
		h.deletePolicyVersion(w, r)
	case "SetDefaultPolicyVersion":
		h.setDefaultPolicyVersion(w, r)
	case "AttachUserPolicy":
		h.attachUserPolicy(w, r)
	case "DetachUserPolicy":
		h.detachUserPolicy(w, r)
	case "AttachRolePolicy":
		h.attachRolePolicy(w, r)
	case "DetachRolePolicy":
		h.detachRolePolicy(w, r)
	case "ListAttachedUserPolicies":
		h.listAttachedUserPolicies(w, r)
	case "ListAttachedRolePolicies":
		h.listAttachedRolePolicies(w, r)
	case "CreateGroup":
		h.createGroup(w, r)
	case "DeleteGroup":
		h.deleteGroup(w, r)
	case "GetGroup":
		h.getGroup(w, r)
	case "ListGroups":
		h.listGroups(w, r)
	case "AddUserToGroup":
		h.addUserToGroup(w, r)
	case "RemoveUserFromGroup":
		h.removeUserFromGroup(w, r)
	case "ListGroupsForUser":
		h.listGroupsForUser(w, r)
	case "CreateAccessKey":
		h.createAccessKey(w, r)
	case "DeleteAccessKey":
		h.deleteAccessKey(w, r)
	case "ListAccessKeys":
		h.listAccessKeys(w, r)
	case "CreateInstanceProfile":
		h.createInstanceProfile(w, r)
	case "DeleteInstanceProfile":
		h.deleteInstanceProfile(w, r)
	case "GetInstanceProfile":
		h.getInstanceProfile(w, r)
	case "ListInstanceProfiles":
		h.listInstanceProfiles(w, r)
	case "AddRoleToInstanceProfile":
		h.addRoleToInstanceProfile(w, r)
	case "RemoveRoleFromInstanceProfile":
		h.removeRoleFromInstanceProfile(w, r)
	default:
		awsquery.WriteXMLError(w, http.StatusBadRequest,
			"InvalidAction", "unknown IAM action: "+r.Form.Get("Action"))
	}
}

// writeErr maps canonical cloudemu errors to IAM XML error responses.
func writeErr(w http.ResponseWriter, err error) {
	switch {
	case cerrors.IsNotFound(err):
		awsquery.WriteXMLError(w, http.StatusNotFound, notFoundCode(err), err.Error())
	case cerrors.IsAlreadyExists(err):
		awsquery.WriteXMLError(w, http.StatusConflict, alreadyExistsCode(err), err.Error())
	case cerrors.IsInvalidArgument(err):
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidInput", err.Error())
	case cerrors.IsFailedPrecondition(err):
		awsquery.WriteXMLError(w, http.StatusConflict, "DeleteConflict", err.Error())
	case cerrors.GetCode(err) == cerrors.ResourceExhausted:
		awsquery.WriteXMLError(w, http.StatusConflict, "LimitExceeded", err.Error())
	default:
		awsquery.WriteXMLError(w, http.StatusInternalServerError, "InternalFailure", err.Error())
	}
}
