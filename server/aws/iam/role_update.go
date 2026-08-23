package iam

import (
	"context"
	"encoding/xml"
	"net/http"
	"strconv"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
)

// roleUpdater is the AWS-specific surface for in-place role mutation
// (UpdateRole / UpdateAssumeRolePolicy). It's not part of the portable driver,
// so the handler type-asserts for it.
type roleUpdater interface {
	UpdateRole(ctx context.Context, roleName string, description *string, maxSessionDuration *int) error
	UpdateAssumeRolePolicy(ctx context.Context, roleName, policyDocument string) error
}

type updateRoleResponse struct {
	XMLName  xml.Name         `xml:"UpdateRoleResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   struct{}         `xml:"UpdateRoleResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type updateAssumeRolePolicyResponse struct {
	XMLName  xml.Name         `xml:"UpdateAssumeRolePolicyResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

func (h *Handler) roleUpdates() (roleUpdater, bool) {
	u, ok := h.iam.(roleUpdater)

	return u, ok
}

func (h *Handler) updateRole(w http.ResponseWriter, r *http.Request) {
	upd, ok := h.roleUpdates()
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "role updates not supported"))
		return
	}

	description := optionalFormString(r, "Description")
	maxSession := optionalFormInt(r, "MaxSessionDuration")

	if err := upd.UpdateRole(r.Context(), r.Form.Get("RoleName"), description, maxSession); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, updateRoleResponse{
		Xmlns: Namespace, Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

// optionalFormString returns a pointer to the form value when the key is
// present, or nil when it is absent — so an omitted parameter leaves the target
// field unchanged.
func optionalFormString(r *http.Request, key string) *string {
	if !r.Form.Has(key) {
		return nil
	}

	v := r.Form.Get(key)

	return &v
}

// optionalFormInt returns a pointer to the parsed integer form value, or nil
// when the key is absent or unparseable.
func optionalFormInt(r *http.Request, key string) *int {
	v := r.Form.Get(key)
	if v == "" {
		return nil
	}

	n, err := strconv.Atoi(v)
	if err != nil {
		return nil
	}

	return &n
}

func (h *Handler) updateAssumeRolePolicy(w http.ResponseWriter, r *http.Request) {
	upd, ok := h.roleUpdates()
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "role updates not supported"))
		return
	}

	policyDocument := r.Form.Get("PolicyDocument")
	if !validPolicyDocument(policyDocument) {
		writeMalformedPolicy(w, "The trust policy is not a valid JSON document")
		return
	}

	if err := upd.UpdateAssumeRolePolicy(r.Context(), r.Form.Get("RoleName"), policyDocument); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, updateAssumeRolePolicyResponse{
		Xmlns: Namespace, Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}
