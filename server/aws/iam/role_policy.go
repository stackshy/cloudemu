package iam

import (
	"encoding/xml"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
)

type putRolePolicyResponse struct {
	XMLName  xml.Name         `xml:"PutRolePolicyResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type deleteRolePolicyResponse struct {
	XMLName  xml.Name         `xml:"DeleteRolePolicyResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type getRolePolicyResult struct {
	RoleName       string `xml:"RoleName"`
	PolicyName     string `xml:"PolicyName"`
	PolicyDocument string `xml:"PolicyDocument"`
}

type getRolePolicyResponse struct {
	XMLName  xml.Name            `xml:"GetRolePolicyResponse"`
	Xmlns    string              `xml:"xmlns,attr"`
	Result   getRolePolicyResult `xml:"GetRolePolicyResult"`
	Metadata responseMetadata    `xml:"ResponseMetadata"`
}

type listRolePoliciesResult struct {
	PolicyNames []string `xml:"PolicyNames>member"`
}

type listRolePoliciesResponse struct {
	XMLName  xml.Name               `xml:"ListRolePoliciesResponse"`
	Xmlns    string                 `xml:"xmlns,attr"`
	Result   listRolePoliciesResult `xml:"ListRolePoliciesResult"`
	Metadata responseMetadata       `xml:"ResponseMetadata"`
}

func (h *Handler) rolePolicies() (rolePolicyManager, bool) {
	pm, ok := h.iam.(rolePolicyManager)

	return pm, ok
}

func (h *Handler) putRolePolicy(w http.ResponseWriter, r *http.Request) {
	pm, ok := h.rolePolicies()
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "inline role policies not supported"))
		return
	}

	if err := pm.PutRolePolicy(r.Context(),
		r.Form.Get("RoleName"), r.Form.Get("PolicyName"), r.Form.Get("PolicyDocument")); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, putRolePolicyResponse{
		Xmlns: Namespace, Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) getRolePolicy(w http.ResponseWriter, r *http.Request) {
	pm, ok := h.rolePolicies()
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "inline role policies not supported"))
		return
	}

	roleName := r.Form.Get("RoleName")
	policyName := r.Form.Get("PolicyName")

	doc, err := pm.GetRolePolicy(r.Context(), roleName, policyName)
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, getRolePolicyResponse{
		Xmlns: Namespace,
		Result: getRolePolicyResult{
			RoleName: roleName, PolicyName: policyName, PolicyDocument: doc,
		},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) deleteRolePolicy(w http.ResponseWriter, r *http.Request) {
	pm, ok := h.rolePolicies()
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "inline role policies not supported"))
		return
	}

	if err := pm.DeleteRolePolicy(r.Context(), r.Form.Get("RoleName"), r.Form.Get("PolicyName")); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, deleteRolePolicyResponse{
		Xmlns: Namespace, Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) listRolePolicies(w http.ResponseWriter, r *http.Request) {
	pm, ok := h.rolePolicies()
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "inline role policies not supported"))
		return
	}

	names, err := pm.ListRolePolicies(r.Context(), r.Form.Get("RoleName"))
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, listRolePoliciesResponse{
		Xmlns:    Namespace,
		Result:   listRolePoliciesResult{PolicyNames: names},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}
