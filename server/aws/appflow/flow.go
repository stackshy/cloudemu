package appflow

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/appflow/driver"
)

func (h *Handler) createFlow(w http.ResponseWriter, r *http.Request) {
	raw, ok := decodeBodyMap(w, r)
	if !ok {
		return
	}

	var body createFlowBody
	if !unmarshalBody(w, raw, &body) {
		return
	}

	out, err := h.af.CreateFlow(r.Context(), &driver.CreateFlowInput{
		FlowName:    body.FlowName,
		Description: body.Description,
		KmsArn:      body.KmsArn,
		Tags:        body.Tags,
		Extra:       extraFrom(raw, createFlowModeledKeys),
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"flowArn": out.FlowArn, "flowStatus": out.FlowStatus})
}

func (h *Handler) describeFlow(w http.ResponseWriter, r *http.Request) {
	name, ok := flowNameFromBody(w, r)
	if !ok {
		return
	}

	out, err := h.af.DescribeFlow(r.Context(), name)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, flowToWire(out))
}

func (h *Handler) updateFlow(w http.ResponseWriter, r *http.Request) {
	raw, ok := decodeBodyMap(w, r)
	if !ok {
		return
	}

	var body updateFlowBody
	if !unmarshalBody(w, raw, &body) {
		return
	}

	out, err := h.af.UpdateFlow(r.Context(), &driver.UpdateFlowInput{
		FlowName:    body.FlowName,
		Description: body.Description,
		Extra:       extraFrom(raw, updateFlowModeledKeys),
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"flowStatus": out.FlowStatus})
}

func (h *Handler) deleteFlow(w http.ResponseWriter, r *http.Request) {
	var body struct {
		FlowName    string `json:"flowName"`
		ForceDelete bool   `json:"forceDelete"`
	}

	if !decodeJSON(w, r, &body) {
		return
	}

	if err := h.af.DeleteFlow(r.Context(), body.FlowName, body.ForceDelete); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{})
}

func (h *Handler) listFlows(w http.ResponseWriter, r *http.Request) {
	var body pageBody
	if !decodeJSON(w, r, &body) {
		return
	}

	flows, next, err := h.af.ListFlows(r.Context(), driver.Page{
		NextToken:  body.NextToken,
		MaxResults: body.MaxResults,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]map[string]any, 0, len(flows))
	for i := range flows {
		out = append(out, flowDefinitionToWire(&flows[i]))
	}

	writeJSON(w, withNext(map[string]any{"flows": out}, next))
}

func (h *Handler) startFlow(w http.ResponseWriter, r *http.Request) {
	name, ok := flowNameFromBody(w, r)
	if !ok {
		return
	}

	out, execID, err := h.af.StartFlow(r.Context(), name)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{
		"flowArn":     out.FlowArn,
		"flowStatus":  out.FlowStatus,
		"executionId": execID,
	})
}

func (h *Handler) stopFlow(w http.ResponseWriter, r *http.Request) {
	name, ok := flowNameFromBody(w, r)
	if !ok {
		return
	}

	out, err := h.af.StopFlow(r.Context(), name)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"flowArn": out.FlowArn, "flowStatus": out.FlowStatus})
}

// flowNameFromBody decodes the flowName-only request body shared by
// DescribeFlow, StartFlow, and StopFlow.
func flowNameFromBody(w http.ResponseWriter, r *http.Request) (string, bool) {
	var body struct {
		FlowName string `json:"flowName"`
	}

	if !decodeJSON(w, r, &body) {
		return "", false
	}

	return body.FlowName, true
}
