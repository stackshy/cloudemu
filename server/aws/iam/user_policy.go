//nolint:dupl // inline user-policy handlers mirror the role variant but bind distinct actions, form fields, and response envelopes.
package iam

import (
	"encoding/xml"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
)

type putUserPolicyResponse struct {
	XMLName  xml.Name         `xml:"PutUserPolicyResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type deleteUserPolicyResponse struct {
	XMLName  xml.Name         `xml:"DeleteUserPolicyResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type getUserPolicyResult struct {
	UserName       string `xml:"UserName"`
	PolicyName     string `xml:"PolicyName"`
	PolicyDocument string `xml:"PolicyDocument"`
}

type getUserPolicyResponse struct {
	XMLName  xml.Name            `xml:"GetUserPolicyResponse"`
	Xmlns    string              `xml:"xmlns,attr"`
	Result   getUserPolicyResult `xml:"GetUserPolicyResult"`
	Metadata responseMetadata    `xml:"ResponseMetadata"`
}

type listUserPoliciesResult struct {
	PolicyNames []string `xml:"PolicyNames>member"`
}

type listUserPoliciesResponse struct {
	XMLName  xml.Name               `xml:"ListUserPoliciesResponse"`
	Xmlns    string                 `xml:"xmlns,attr"`
	Result   listUserPoliciesResult `xml:"ListUserPoliciesResult"`
	Metadata responseMetadata       `xml:"ResponseMetadata"`
}

func (h *Handler) userPolicies() (userPolicyManager, bool) {
	pm, ok := h.iam.(userPolicyManager)

	return pm, ok
}

func (h *Handler) putUserPolicy(w http.ResponseWriter, r *http.Request) {
	pm, ok := h.userPolicies()
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "inline user policies not supported"))
		return
	}

	if err := pm.PutUserPolicy(r.Context(),
		r.Form.Get("UserName"), r.Form.Get("PolicyName"), r.Form.Get("PolicyDocument")); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, putUserPolicyResponse{
		Xmlns: Namespace, Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) getUserPolicy(w http.ResponseWriter, r *http.Request) {
	pm, ok := h.userPolicies()
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "inline user policies not supported"))
		return
	}

	userName := r.Form.Get("UserName")
	policyName := r.Form.Get("PolicyName")

	doc, err := pm.GetUserPolicy(r.Context(), userName, policyName)
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, getUserPolicyResponse{
		Xmlns: Namespace,
		Result: getUserPolicyResult{
			UserName: userName, PolicyName: policyName, PolicyDocument: doc,
		},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) deleteUserPolicy(w http.ResponseWriter, r *http.Request) {
	pm, ok := h.userPolicies()
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "inline user policies not supported"))
		return
	}

	if err := pm.DeleteUserPolicy(r.Context(), r.Form.Get("UserName"), r.Form.Get("PolicyName")); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, deleteUserPolicyResponse{
		Xmlns: Namespace, Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) listUserPolicies(w http.ResponseWriter, r *http.Request) {
	pm, ok := h.userPolicies()
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "inline user policies not supported"))
		return
	}

	names, err := pm.ListUserPolicies(r.Context(), r.Form.Get("UserName"))
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, listUserPoliciesResponse{
		Xmlns:    Namespace,
		Result:   listUserPoliciesResult{PolicyNames: names},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}
