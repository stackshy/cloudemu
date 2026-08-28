package cloudformation

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
)

func (h *Handler) createStack(w http.ResponseWriter, r *http.Request) {
	in := createInput(r.Form)

	stack, err := h.api.CreateStack(r.Context(), &in)
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, createStackResponse{
		Xmlns: Namespace, Result: stackIDResult{StackID: stack.ID}, Meta: meta(),
	})
}

func (h *Handler) updateStack(w http.ResponseWriter, r *http.Request) {
	in := updateInput(r.Form)

	stack, err := h.api.UpdateStack(r.Context(), &in)
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, updateStackResponse{
		Xmlns: Namespace, Result: stackIDResult{StackID: stack.ID}, Meta: meta(),
	})
}

func (h *Handler) deleteStack(w http.ResponseWriter, r *http.Request) {
	if err := h.api.DeleteStack(r.Context(), r.Form.Get("StackName")); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, deleteStackResponse{Xmlns: Namespace, Meta: meta()})
}

func (h *Handler) describeStacks(w http.ResponseWriter, r *http.Request) {
	stacks, err := h.api.DescribeStacks(r.Context(), r.Form.Get("StackName"))
	if err != nil {
		writeErr(w, err)
		return
	}

	var resp describeStacksResponse
	resp.Xmlns = Namespace
	resp.Meta = meta()

	for i := range stacks {
		resp.Result.Stacks = append(resp.Result.Stacks, toStackXML(&stacks[i]))
	}

	awsquery.WriteXMLResponse(w, resp)
}

func (h *Handler) describeStackEvents(w http.ResponseWriter, r *http.Request) {
	events, err := h.api.DescribeStackEvents(r.Context(), r.Form.Get("StackName"))
	if err != nil {
		writeErr(w, err)
		return
	}

	var resp describeStackEventsResponse
	resp.Xmlns = Namespace
	resp.Meta = meta()

	for i := range events {
		e := &events[i]
		resp.Result.StackEvents = append(resp.Result.StackEvents, eventXML{
			StackID: e.StackID, EventID: e.EventID, StackName: e.StackName,
			LogicalResourceID: e.LogicalID, PhysicalResourceID: e.PhysicalID,
			ResourceType: e.ResourceType, Timestamp: isoTime(e.Timestamp),
			ResourceStatus: e.Status, ResourceStatusReason: e.StatusReason,
		})
	}

	awsquery.WriteXMLResponse(w, resp)
}

func (h *Handler) listStacks(w http.ResponseWriter, r *http.Request) {
	summaries, err := h.api.ListStacks(r.Context(), awsquery.ListStrings(r.Form, "StackStatusFilter.member"))
	if err != nil {
		writeErr(w, err)
		return
	}

	var resp listStacksResponse
	resp.Xmlns = Namespace
	resp.Meta = meta()

	for i := range summaries {
		s := &summaries[i]
		resp.Result.StackSummaries = append(resp.Result.StackSummaries, summaryXML{
			StackID: s.ID, StackName: s.Name, TemplateDescription: s.TemplateDescription,
			CreationTime: isoTime(s.CreationTime), LastUpdatedTime: isoTime(s.LastUpdated),
			DeletionTime: isoTime(s.DeletionTime), StackStatus: s.Status, StackStatusReason: s.StatusReason,
		})
	}

	awsquery.WriteXMLResponse(w, resp)
}

func (h *Handler) describeStackResources(w http.ResponseWriter, r *http.Request) {
	name := r.Form.Get("StackName")

	resources, err := h.api.DescribeStackResources(r.Context(), name)
	if err != nil {
		writeErr(w, err)
		return
	}

	stackID := h.stackID(r, name)

	var resp describeStackResourcesResponse
	resp.Xmlns = Namespace
	resp.Meta = meta()

	for _, res := range resources {
		resp.Result.StackResources = append(resp.Result.StackResources, resourceXML{
			StackID: stackID, StackName: name, LogicalResourceID: res.LogicalID,
			PhysicalResourceID: res.PhysicalID, ResourceType: res.Type,
			Timestamp: isoTime(res.Timestamp), ResourceStatus: res.Status,
			ResourceStatusReason: res.StatusReason,
		})
	}

	awsquery.WriteXMLResponse(w, resp)
}

func (h *Handler) listStackResources(w http.ResponseWriter, r *http.Request) {
	resources, err := h.api.ListStackResources(r.Context(), r.Form.Get("StackName"))
	if err != nil {
		writeErr(w, err)
		return
	}

	var resp listStackResourcesResponse
	resp.Xmlns = Namespace
	resp.Meta = meta()

	for _, res := range resources {
		resp.Result.StackResourceSummaries = append(resp.Result.StackResourceSummaries, resourceSummaryXML{
			LogicalResourceID: res.LogicalID, PhysicalResourceID: res.PhysicalID,
			ResourceType: res.Type, LastUpdatedTimestamp: isoTime(res.Timestamp),
			ResourceStatus: res.Status, ResourceStatusReason: res.StatusReason,
		})
	}

	awsquery.WriteXMLResponse(w, resp)
}

func (h *Handler) getTemplate(w http.ResponseWriter, r *http.Request) {
	body, err := h.api.GetTemplate(r.Context(), r.Form.Get("StackName"))
	if err != nil {
		writeErr(w, err)
		return
	}

	var resp getTemplateResponse
	resp.Xmlns = Namespace
	resp.Meta = meta()
	resp.Result.TemplateBody = body

	awsquery.WriteXMLResponse(w, resp)
}

// stackID resolves a stack's id for stamping onto resource rows (they carry the
// StackId, not just the name). A lookup failure degrades to the name.
func (h *Handler) stackID(r *http.Request, name string) string {
	stacks, err := h.api.DescribeStacks(r.Context(), name)
	if err != nil || len(stacks) == 0 {
		return name
	}

	return stacks[0].ID
}
