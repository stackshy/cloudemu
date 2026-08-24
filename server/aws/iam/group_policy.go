package iam

import (
	"encoding/xml"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
)

type attachGroupPolicyResponse struct {
	XMLName  xml.Name         `xml:"AttachGroupPolicyResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type detachGroupPolicyResponse struct {
	XMLName  xml.Name         `xml:"DetachGroupPolicyResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type listAttachedGroupPoliciesResult struct {
	AttachedPolicies attachedPoliciesXML `xml:"AttachedPolicies"`
	IsTruncated      bool                `xml:"IsTruncated"`
}

type listAttachedGroupPoliciesResponse struct {
	XMLName  xml.Name                        `xml:"ListAttachedGroupPoliciesResponse"`
	Xmlns    string                          `xml:"xmlns,attr"`
	Result   listAttachedGroupPoliciesResult `xml:"ListAttachedGroupPoliciesResult"`
	Metadata responseMetadata                `xml:"ResponseMetadata"`
}

type putGroupPolicyResponse struct {
	XMLName  xml.Name         `xml:"PutGroupPolicyResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type deleteGroupPolicyResponse struct {
	XMLName  xml.Name         `xml:"DeleteGroupPolicyResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type getGroupPolicyResult struct {
	GroupName      string `xml:"GroupName"`
	PolicyName     string `xml:"PolicyName"`
	PolicyDocument string `xml:"PolicyDocument"`
}

type getGroupPolicyResponse struct {
	XMLName  xml.Name             `xml:"GetGroupPolicyResponse"`
	Xmlns    string               `xml:"xmlns,attr"`
	Result   getGroupPolicyResult `xml:"GetGroupPolicyResult"`
	Metadata responseMetadata     `xml:"ResponseMetadata"`
}

type listGroupPoliciesResult struct {
	PolicyNames []string `xml:"PolicyNames>member"`
}

type listGroupPoliciesResponse struct {
	XMLName  xml.Name                `xml:"ListGroupPoliciesResponse"`
	Xmlns    string                  `xml:"xmlns,attr"`
	Result   listGroupPoliciesResult `xml:"ListGroupPoliciesResult"`
	Metadata responseMetadata        `xml:"ResponseMetadata"`
}

func (h *Handler) groupPolicies() (groupPolicyManager, bool) {
	pm, ok := h.iam.(groupPolicyManager)

	return pm, ok
}

//nolint:dupl // per-entity policy wire handlers share shape but bind distinct actions/form fields and response envelopes.
func (h *Handler) attachGroupPolicy(w http.ResponseWriter, r *http.Request) {
	pm, ok := h.groupPolicies()
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "group policies not supported"))
		return
	}

	if err := pm.AttachGroupPolicy(r.Context(), r.Form.Get("GroupName"), r.Form.Get("PolicyArn")); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, attachGroupPolicyResponse{
		Xmlns: Namespace, Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

//nolint:dupl // per-entity policy wire handlers share shape but bind distinct actions/form fields and response envelopes.
func (h *Handler) detachGroupPolicy(w http.ResponseWriter, r *http.Request) {
	pm, ok := h.groupPolicies()
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "group policies not supported"))
		return
	}

	if err := pm.DetachGroupPolicy(r.Context(), r.Form.Get("GroupName"), r.Form.Get("PolicyArn")); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, detachGroupPolicyResponse{
		Xmlns: Namespace, Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) listAttachedGroupPolicies(w http.ResponseWriter, r *http.Request) {
	pm, ok := h.groupPolicies()
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "group policies not supported"))
		return
	}

	arns, err := pm.ListAttachedGroupPolicies(r.Context(), r.Form.Get("GroupName"))
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, listAttachedGroupPoliciesResponse{
		Xmlns: Namespace,
		Result: listAttachedGroupPoliciesResult{
			AttachedPolicies: attachedPoliciesFromARNs(arns),
			IsTruncated:      false,
		},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

//nolint:dupl // per-entity policy wire handlers share shape but bind distinct actions/form fields and response envelopes.
func (h *Handler) putGroupPolicy(w http.ResponseWriter, r *http.Request) {
	pm, ok := h.groupPolicies()
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "group policies not supported"))
		return
	}

	if err := pm.PutGroupPolicy(r.Context(),
		r.Form.Get("GroupName"), r.Form.Get("PolicyName"), r.Form.Get("PolicyDocument")); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, putGroupPolicyResponse{
		Xmlns: Namespace, Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) getGroupPolicy(w http.ResponseWriter, r *http.Request) {
	pm, ok := h.groupPolicies()
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "group policies not supported"))
		return
	}

	groupName := r.Form.Get("GroupName")
	policyName := r.Form.Get("PolicyName")

	doc, err := pm.GetGroupPolicy(r.Context(), groupName, policyName)
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, getGroupPolicyResponse{
		Xmlns: Namespace,
		Result: getGroupPolicyResult{
			GroupName: groupName, PolicyName: policyName, PolicyDocument: doc,
		},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

//nolint:dupl // per-entity policy wire handlers share shape but bind distinct actions/form fields and response envelopes.
func (h *Handler) deleteGroupPolicy(w http.ResponseWriter, r *http.Request) {
	pm, ok := h.groupPolicies()
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "group policies not supported"))
		return
	}

	if err := pm.DeleteGroupPolicy(r.Context(), r.Form.Get("GroupName"), r.Form.Get("PolicyName")); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, deleteGroupPolicyResponse{
		Xmlns: Namespace, Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) listGroupPolicies(w http.ResponseWriter, r *http.Request) {
	pm, ok := h.groupPolicies()
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "group policies not supported"))
		return
	}

	names, err := pm.ListGroupPolicies(r.Context(), r.Form.Get("GroupName"))
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, listGroupPoliciesResponse{
		Xmlns:    Namespace,
		Result:   listGroupPoliciesResult{PolicyNames: names},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}
