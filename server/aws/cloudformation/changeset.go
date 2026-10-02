package cloudformation

import (
	"encoding/xml"
	"net/http"
	"net/url"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	cfn "github.com/stackshy/cloudemu/v2/services/cloudformation"
)

// changeTypeResource is the only Change Type CloudFormation reports.
const changeTypeResource = "Resource"

// The change set actions.
const (
	actionCreateChangeSet   = "CreateChangeSet"
	actionDescribeChangeSet = "DescribeChangeSet"
	actionListChangeSets    = "ListChangeSets"
	actionExecuteChangeSet  = "ExecuteChangeSet"
	actionDeleteChangeSet   = "DeleteChangeSet"
)

// serveChangeSet dispatches the change set actions.
func (h *Handler) serveChangeSet(w http.ResponseWriter, r *http.Request) {
	switch r.Form.Get("Action") {
	case actionCreateChangeSet:
		h.createChangeSet(w, r)
	case actionDescribeChangeSet:
		h.describeChangeSet(w, r)
	case actionListChangeSets:
		h.listChangeSets(w, r)
	case actionExecuteChangeSet:
		h.executeChangeSet(w, r)
	case actionDeleteChangeSet:
		h.deleteChangeSet(w, r)
	}
}

func (h *Handler) createChangeSet(w http.ResponseWriter, r *http.Request) {
	cs, err := h.api.CreateChangeSet(r.Context(), createChangeSetInput(r.Form))
	if err != nil {
		writeErr(w, err)
		return
	}

	var resp createChangeSetResponse
	resp.Xmlns = Namespace
	resp.Meta = meta()
	resp.Result.ID = cs.ID
	resp.Result.StackID = cs.StackID

	awsquery.WriteXMLResponse(w, resp)
}

func (h *Handler) describeChangeSet(w http.ResponseWriter, r *http.Request) {
	cs, err := h.api.DescribeChangeSet(r.Context(), &cfn.DescribeChangeSetInput{
		ChangeSetName:         r.Form.Get("ChangeSetName"),
		StackName:             r.Form.Get("StackName"),
		NextToken:             r.Form.Get("NextToken"),
		IncludePropertyValues: strings.EqualFold(r.Form.Get("IncludePropertyValues"), "true"),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	resp := describeChangeSetResponse{Xmlns: Namespace, Result: toChangeSetXML(cs), Meta: meta()}

	awsquery.WriteXMLResponse(w, resp)
}

func (h *Handler) listChangeSets(w http.ResponseWriter, r *http.Request) {
	list, err := h.api.ListChangeSets(r.Context(), &cfn.ListChangeSetsInput{
		StackName: r.Form.Get("StackName"),
		NextToken: r.Form.Get("NextToken"),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	var resp listChangeSetsResponse
	resp.Xmlns = Namespace
	resp.Meta = meta()
	resp.Result.NextToken = list.NextToken

	for i := range list.Summaries {
		cs := &list.Summaries[i]
		resp.Result.Summaries = append(resp.Result.Summaries, changeSetSummaryXML{
			StackID: cs.StackID, StackName: cs.StackName, ChangeSetID: cs.ID, ChangeSetName: cs.Name,
			ExecutionStatus: cs.ExecutionStatus, Status: cs.Status, StatusReason: cs.StatusReason,
			CreationTime: isoTime(cs.CreationTime), Description: cs.Description,
		})
	}

	awsquery.WriteXMLResponse(w, resp)
}

func (h *Handler) executeChangeSet(w http.ResponseWriter, r *http.Request) {
	in := &cfn.ExecuteChangeSetInput{
		ChangeSetName:      r.Form.Get("ChangeSetName"),
		StackName:          r.Form.Get("StackName"),
		ClientRequestToken: r.Form.Get("ClientRequestToken"),
	}

	if r.Form.Has("DisableRollback") {
		disable := formBool(r.Form, "DisableRollback")
		in.DisableRollback = &disable
	}

	if r.Form.Has("RetainExceptOnCreate") {
		retain := formBool(r.Form, "RetainExceptOnCreate")
		in.RetainExceptOnCreate = &retain
	}

	if err := h.api.ExecuteChangeSet(r.Context(), in); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, executeChangeSetResponse{Xmlns: Namespace, Meta: meta()})
}

func (h *Handler) deleteChangeSet(w http.ResponseWriter, r *http.Request) {
	err := h.api.DeleteChangeSet(r.Context(), &cfn.DeleteChangeSetInput{
		ChangeSetName: r.Form.Get("ChangeSetName"),
		StackName:     r.Form.Get("StackName"),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, deleteChangeSetResponse{Xmlns: Namespace, Meta: meta()})
}

// createChangeSetInput reads a CreateChangeSet form. Absent topics keep the
// stack's, and an empty list removes them, as on UpdateStack.
func createChangeSetInput(form url.Values) *cfn.CreateChangeSetInput {
	return &cfn.CreateChangeSetInput{
		StackName:           form.Get("StackName"),
		ChangeSetName:       form.Get("ChangeSetName"),
		ChangeSetType:       form.Get("ChangeSetType"),
		Description:         form.Get("Description"),
		TemplateBody:        form.Get("TemplateBody"),
		TemplateURL:         form.Get("TemplateURL"),
		UsePreviousTemplate: strings.EqualFold(form.Get("UsePreviousTemplate"), "true"),
		Parameters:          parseParameters(form),
		Tags:                parseTags(form),
		Capabilities:        awsquery.ListStrings(form, "Capabilities.member"),
		NotificationARNs:    updateNotificationARNs(form),
		OnStackFailure:      form.Get("OnStackFailure"),
		ClientToken:         form.Get("ClientToken"),
	}
}

func toChangeSetXML(cs *cfn.ChangeSet) describeChangeSetResult {
	x := describeChangeSetResult{
		ChangeSetName: cs.Name, ChangeSetID: cs.ID, StackID: cs.StackID, StackName: cs.StackName,
		Description: cs.Description, CreationTime: isoTime(cs.CreationTime),
		ExecutionStatus: cs.ExecutionStatus, Status: cs.Status, StatusReason: cs.StatusReason,
		NotificationARNs: cs.NotificationARNs, Capabilities: cs.Capabilities,
		OnStackFailure: cs.OnStackFailure, NextToken: cs.NextToken,
	}

	for _, p := range cs.Parameters {
		x.Parameters = append(x.Parameters, parameterXML{
			ParameterKey: p.Key, ParameterValue: p.Value, ResolvedValue: p.ResolvedValue,
		})
	}

	for k, v := range cs.Tags {
		x.Tags = append(x.Tags, tagXML{Key: k, Value: v})
	}

	for i := range cs.Changes {
		x.Changes = append(x.Changes, changeXML{Type: changeTypeResource, ResourceChange: toResourceChangeXML(&cs.Changes[i])})
	}

	return x
}

func toResourceChangeXML(c *cfn.ResourceChange) resourceChangeXML {
	x := resourceChangeXML{
		PolicyAction: c.PolicyAction, Action: c.Action, LogicalResourceID: c.LogicalID,
		PhysicalResourceID: c.PhysicalID, ResourceType: c.ResourceType, Replacement: c.Replacement,
		Scope: c.Scope, BeforeContext: c.BeforeContext, AfterContext: c.AfterContext,
	}

	for i := range c.Details {
		d := &c.Details[i]
		x.Details = append(x.Details, changeDetailXML{
			Target: changeTargetXML{
				Attribute: d.Target.Attribute, Name: d.Target.Name, RequiresRecreation: d.Target.RequiresRecreation,
				BeforeValue: d.Target.BeforeValue, AfterValue: d.Target.AfterValue,
				AttributeChangeType: d.Target.AttributeChangeType,
			},
			Evaluation: d.Evaluation, ChangeSource: d.ChangeSource, CausingEntity: d.CausingEntity,
		})
	}

	return x
}

type createChangeSetResponse struct {
	XMLName xml.Name `xml:"CreateChangeSetResponse"`
	Xmlns   string   `xml:"xmlns,attr"`
	Result  struct {
		ID      string `xml:"Id"`
		StackID string `xml:"StackId"`
	} `xml:"CreateChangeSetResult"`
	Meta responseMetadata `xml:"ResponseMetadata"`
}

type changeTargetXML struct {
	Attribute           string `xml:"Attribute"`
	Name                string `xml:"Name,omitempty"`
	RequiresRecreation  string `xml:"RequiresRecreation"`
	BeforeValue         string `xml:"BeforeValue,omitempty"`
	AfterValue          string `xml:"AfterValue,omitempty"`
	AttributeChangeType string `xml:"AttributeChangeType,omitempty"`
}

type changeDetailXML struct {
	Target        changeTargetXML `xml:"Target"`
	Evaluation    string          `xml:"Evaluation"`
	ChangeSource  string          `xml:"ChangeSource"`
	CausingEntity string          `xml:"CausingEntity,omitempty"`
}

type resourceChangeXML struct {
	PolicyAction       string            `xml:"PolicyAction,omitempty"`
	Action             string            `xml:"Action"`
	LogicalResourceID  string            `xml:"LogicalResourceId"`
	PhysicalResourceID string            `xml:"PhysicalResourceId,omitempty"`
	ResourceType       string            `xml:"ResourceType"`
	Replacement        string            `xml:"Replacement,omitempty"`
	Scope              []string          `xml:"Scope>member"`
	Details            []changeDetailXML `xml:"Details>member"`
	BeforeContext      string            `xml:"BeforeContext,omitempty"`
	AfterContext       string            `xml:"AfterContext,omitempty"`
}

type changeXML struct {
	Type           string            `xml:"Type"`
	ResourceChange resourceChangeXML `xml:"ResourceChange"`
}

type describeChangeSetResult struct {
	ChangeSetName         string         `xml:"ChangeSetName"`
	ChangeSetID           string         `xml:"ChangeSetId"`
	StackID               string         `xml:"StackId"`
	StackName             string         `xml:"StackName"`
	Description           string         `xml:"Description,omitempty"`
	Parameters            []parameterXML `xml:"Parameters>member"`
	CreationTime          string         `xml:"CreationTime"`
	ExecutionStatus       string         `xml:"ExecutionStatus"`
	Status                string         `xml:"Status"`
	StatusReason          string         `xml:"StatusReason,omitempty"`
	NotificationARNs      []string       `xml:"NotificationARNs>member"`
	RollbackConfiguration struct{}       `xml:"RollbackConfiguration"`
	Capabilities          []string       `xml:"Capabilities>member"`
	Tags                  []tagXML       `xml:"Tags>member"`
	Changes               []changeXML    `xml:"Changes>member"`
	NextToken             string         `xml:"NextToken,omitempty"`
	IncludeNestedStacks   bool           `xml:"IncludeNestedStacks"`
	OnStackFailure        string         `xml:"OnStackFailure,omitempty"`
}

type describeChangeSetResponse struct {
	XMLName xml.Name                `xml:"DescribeChangeSetResponse"`
	Xmlns   string                  `xml:"xmlns,attr"`
	Result  describeChangeSetResult `xml:"DescribeChangeSetResult"`
	Meta    responseMetadata        `xml:"ResponseMetadata"`
}

type changeSetSummaryXML struct {
	StackID             string `xml:"StackId"`
	StackName           string `xml:"StackName"`
	ChangeSetID         string `xml:"ChangeSetId"`
	ChangeSetName       string `xml:"ChangeSetName"`
	ExecutionStatus     string `xml:"ExecutionStatus"`
	Status              string `xml:"Status"`
	StatusReason        string `xml:"StatusReason,omitempty"`
	CreationTime        string `xml:"CreationTime"`
	Description         string `xml:"Description,omitempty"`
	IncludeNestedStacks bool   `xml:"IncludeNestedStacks"`
}

type listChangeSetsResponse struct {
	XMLName xml.Name `xml:"ListChangeSetsResponse"`
	Xmlns   string   `xml:"xmlns,attr"`
	Result  struct {
		Summaries []changeSetSummaryXML `xml:"Summaries>member"`
		NextToken string                `xml:"NextToken,omitempty"`
	} `xml:"ListChangeSetsResult"`
	Meta responseMetadata `xml:"ResponseMetadata"`
}

type executeChangeSetResponse struct {
	XMLName xml.Name         `xml:"ExecuteChangeSetResponse"`
	Xmlns   string           `xml:"xmlns,attr"`
	Result  struct{}         `xml:"ExecuteChangeSetResult"`
	Meta    responseMetadata `xml:"ResponseMetadata"`
}

type deleteChangeSetResponse struct {
	XMLName xml.Name         `xml:"DeleteChangeSetResponse"`
	Xmlns   string           `xml:"xmlns,attr"`
	Result  struct{}         `xml:"DeleteChangeSetResult"`
	Meta    responseMetadata `xml:"ResponseMetadata"`
}
