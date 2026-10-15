package cloudformation

import (
	"encoding/xml"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	cfn "github.com/stackshy/cloudemu/v2/services/cloudformation"
)

// The stack policy, cancel and rollback actions.
const (
	actionSetStackPolicy    = "SetStackPolicy"
	actionGetStackPolicy    = "GetStackPolicy"
	actionCancelUpdateStack = "CancelUpdateStack"
	actionRollbackStack     = "RollbackStack"
)

// serveStackControl dispatches the stack policy, cancel and rollback
// actions.
func (h *Handler) serveStackControl(w http.ResponseWriter, r *http.Request) {
	switch r.Form.Get("Action") {
	case actionSetStackPolicy:
		h.setStackPolicy(w, r)
	case actionGetStackPolicy:
		h.getStackPolicy(w, r)
	case actionCancelUpdateStack:
		h.cancelUpdateStack(w, r)
	case actionRollbackStack:
		h.rollbackStack(w, r)
	}
}

func (h *Handler) setStackPolicy(w http.ResponseWriter, r *http.Request) {
	err := h.api.SetStackPolicy(r.Context(), &cfn.SetStackPolicyInput{
		StackName:       r.Form.Get("StackName"),
		StackPolicyBody: r.Form.Get("StackPolicyBody"),
		StackPolicyURL:  r.Form.Get("StackPolicyURL"),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, setStackPolicyResponse{Xmlns: Namespace, Meta: meta()})
}

func (h *Handler) getStackPolicy(w http.ResponseWriter, r *http.Request) {
	body, err := h.api.GetStackPolicy(r.Context(), r.Form.Get("StackName"))
	if err != nil {
		writeErr(w, err)
		return
	}

	resp := getStackPolicyResponse{Xmlns: Namespace, Meta: meta()}
	resp.Result.StackPolicyBody = body

	awsquery.WriteXMLResponse(w, resp)
}

func (h *Handler) cancelUpdateStack(w http.ResponseWriter, r *http.Request) {
	err := h.api.CancelUpdateStack(r.Context(), &cfn.CancelUpdateStackInput{
		StackName:          r.Form.Get("StackName"),
		ClientRequestToken: r.Form.Get("ClientRequestToken"),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, cancelUpdateStackResponse{Xmlns: Namespace, Meta: meta()})
}

func (h *Handler) rollbackStack(w http.ResponseWriter, r *http.Request) {
	id, err := h.api.RollbackStack(r.Context(), &cfn.RollbackStackInput{
		StackName:            r.Form.Get("StackName"),
		ClientRequestToken:   r.Form.Get("ClientRequestToken"),
		RetainExceptOnCreate: formBool(r.Form, "RetainExceptOnCreate"),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, rollbackStackResponse{
		Xmlns: Namespace, Result: stackIDResult{StackID: id}, Meta: meta(),
	})
}

type setStackPolicyResponse struct {
	XMLName xml.Name         `xml:"SetStackPolicyResponse"`
	Xmlns   string           `xml:"xmlns,attr"`
	Meta    responseMetadata `xml:"ResponseMetadata"`
}

type getStackPolicyResponse struct {
	XMLName xml.Name `xml:"GetStackPolicyResponse"`
	Xmlns   string   `xml:"xmlns,attr"`
	Result  struct {
		StackPolicyBody string `xml:"StackPolicyBody,omitempty"`
	} `xml:"GetStackPolicyResult"`
	Meta responseMetadata `xml:"ResponseMetadata"`
}

type cancelUpdateStackResponse struct {
	XMLName xml.Name         `xml:"CancelUpdateStackResponse"`
	Xmlns   string           `xml:"xmlns,attr"`
	Meta    responseMetadata `xml:"ResponseMetadata"`
}

type rollbackStackResponse struct {
	XMLName xml.Name         `xml:"RollbackStackResponse"`
	Xmlns   string           `xml:"xmlns,attr"`
	Result  stackIDResult    `xml:"RollbackStackResult"`
	Meta    responseMetadata `xml:"ResponseMetadata"`
}
