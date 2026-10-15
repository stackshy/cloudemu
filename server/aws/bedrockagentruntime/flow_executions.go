package bedrockagentruntime

import (
	"encoding/json"
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/bedrockagentruntime/driver"
)

type flowInputJSON struct {
	NodeName       string `json:"nodeName"`
	NodeOutputName string `json:"nodeOutputName"`
	NodeInputName  string `json:"nodeInputName"`
	Content        struct {
		Document json.RawMessage `json:"document"`
	} `json:"content"`
}

type flowRequest struct {
	Inputs            []flowInputJSON `json:"inputs"`
	EnableTrace       bool            `json:"enableTrace"`
	ExecutionID       string          `json:"executionId"`
	FlowExecutionName string          `json:"flowExecutionName"`
}

func (in *flowRequest) toDriver(segs []string) driver.FlowInvocationInput {
	out := driver.FlowInvocationInput{
		FlowID: segs[1], AliasID: segs[3], ExecutionID: in.ExecutionID, ExecutionName: in.FlowExecutionName,
		EnableTrace: in.EnableTrace,
	}

	for _, i := range in.Inputs {
		out.Inputs = append(out.Inputs, driver.FlowInput{
			NodeName: i.NodeName, NodeOutputName: i.NodeOutputName, NodeInputName: i.NodeInputName, Content: i.Content.Document,
		})
	}

	return out
}

type documentJSON struct {
	Document json.RawMessage `json:"document"`
}

// serveInvokeFlow handles InvokeFlow: POST /flows/{flowId}/aliases/{aliasId}.
func serveInvokeFlow(h *Handler, w http.ResponseWriter, r *http.Request, segs []string) {
	withCapability(h, w, "InvokeFlow", func(c driver.FlowExecutions) {
		var in flowRequest
		if !decodeJSON(w, r, &in) {
			return
		}

		res, err := c.InvokeFlow(r.Context(), in.toDriver(segs))
		if err != nil {
			writeErr(w, err)

			return
		}

		events := make([]streamEvent, 0, len(res.Traces)+len(res.Outputs)+1)
		for _, t := range res.Traces {
			events = append(events, streamEvent{"flowTraceEvent", map[string]any{"trace": map[string]any{
				t.Kind: map[string]any{
					"nodeName": t.NodeName, "timestamp": t.Timestamp,
					"fields": []map[string]any{{"nodeInputName": t.FieldName, "nodeOutputName": t.FieldName,
						"content": documentJSON{Document: t.Content}}},
				},
			}}})
		}

		for _, o := range res.Outputs {
			events = append(events, streamEvent{"flowOutputEvent", map[string]any{
				"nodeName": o.NodeName, "nodeType": o.NodeType, "content": documentJSON{Document: o.Content},
			}})
		}

		events = append(events, streamEvent{"flowCompletionEvent", map[string]string{"completionReason": res.CompletionReason}})

		writeEventStream(w, map[string]string{headerFlowExecutionID: res.ExecutionID}, events)
	})
}

type executionJSON struct {
	ExecutionArn        string `json:"executionArn"`
	FlowIdentifier      string `json:"flowIdentifier"`
	FlowAliasIdentifier string `json:"flowAliasIdentifier"`
	FlowVersion         string `json:"flowVersion"`
	Status              string `json:"status"`
	StartedAt           string `json:"startedAt,omitempty"`
	CreatedAt           string `json:"createdAt,omitempty"`
	EndedAt             string `json:"endedAt,omitempty"`
}

func toExecutionJSON(e *driver.FlowExecution) executionJSON {
	return executionJSON{
		ExecutionArn: e.ARN, FlowIdentifier: e.FlowID, FlowAliasIdentifier: e.AliasID, FlowVersion: e.FlowVersion,
		Status: e.Status, StartedAt: e.StartedAt, EndedAt: e.EndedAt,
	}
}

func serveStartExecution(h *Handler, w http.ResponseWriter, r *http.Request, segs []string) {
	withCapability(h, w, "StartFlowExecution", func(c driver.FlowExecutions) {
		var in flowRequest
		if !decodeJSON(w, r, &in) {
			return
		}

		ex, err := c.StartFlowExecution(r.Context(), in.toDriver(segs))
		if err != nil {
			writeErr(w, err)

			return
		}

		writeJSON(w, map[string]string{"executionArn": ex.ARN})
	})
}

func serveGetExecution(h *Handler, w http.ResponseWriter, r *http.Request, segs []string) {
	withCapability(h, w, "GetFlowExecution", func(c driver.FlowExecutions) {
		ex, err := c.GetFlowExecution(r.Context(), segs[1], segs[3], segs[5])
		if err != nil {
			writeErr(w, err)

			return
		}

		writeJSON(w, toExecutionJSON(ex))
	})
}

func serveStopExecution(h *Handler, w http.ResponseWriter, r *http.Request, segs []string) {
	withCapability(h, w, "StopFlowExecution", func(c driver.FlowExecutions) {
		ex, err := c.StopFlowExecution(r.Context(), segs[1], segs[3], segs[5])
		if err != nil {
			writeErr(w, err)

			return
		}

		writeJSON(w, map[string]string{"executionArn": ex.ARN, "status": ex.Status})
	})
}

func serveListExecutions(h *Handler, w http.ResponseWriter, r *http.Request, segs []string) {
	withCapability(h, w, "ListFlowExecutions", func(c driver.FlowExecutions) {
		page, ok := queryPage(w, r, "maxResults")
		if !ok {
			return
		}

		list, next, err := c.ListFlowExecutions(r.Context(), segs[1], r.URL.Query().Get("flowAliasIdentifier"), page)
		if err != nil {
			writeErr(w, err)

			return
		}

		out := make([]executionJSON, 0, len(list))

		for i := range list {
			e := toExecutionJSON(&list[i])
			e.CreatedAt, e.StartedAt = e.StartedAt, ""
			out = append(out, e)
		}

		writeJSON(w, map[string]any{"flowExecutionSummaries": out, "nextToken": omitEmpty(next)})
	})
}

func serveExecutionEvents(h *Handler, w http.ResponseWriter, r *http.Request, segs []string) {
	withCapability(h, w, "ListFlowExecutionEvents", func(c driver.FlowExecutions) {
		page, ok := queryPage(w, r, "maxResults")
		if !ok {
			return
		}

		events, next, err := c.ListFlowExecutionEvents(r.Context(), segs[1], segs[3], segs[5], r.URL.Query().Get("eventType"), page)
		if err != nil {
			writeErr(w, err)

			return
		}

		out := make([]map[string]any, 0, len(events))
		for _, e := range events {
			out = append(out, map[string]any{e.Kind: executionEventJSON(e)})
		}

		writeJSON(w, map[string]any{"flowExecutionEvents": out, "nextToken": omitEmpty(next)})
	})
}

// executionEventJSON renders one event; node events name their fields
// differently from flow events only in the field key they carry.
func executionEventJSON(e driver.FlowExecutionEvent) map[string]any {
	fields := make([]map[string]any, 0, len(e.Fields))
	for _, f := range e.Fields {
		fields = append(fields, map[string]any{"name": f.Name, "content": documentJSON{Document: f.Content}})
	}

	return map[string]any{"nodeName": e.NodeName, "timestamp": e.Timestamp, "fields": fields}
}

func serveExecutionSnapshot(h *Handler, w http.ResponseWriter, r *http.Request, segs []string) {
	withCapability(h, w, "GetExecutionFlowSnapshot", func(c driver.FlowExecutions) {
		s, err := c.GetExecutionFlowSnapshot(r.Context(), segs[1], segs[3], segs[5])
		if err != nil {
			writeErr(w, err)

			return
		}

		writeJSON(w, map[string]string{
			"flowIdentifier": s.FlowID, "flowAliasIdentifier": s.AliasID, "flowVersion": s.FlowVersion,
			"definition": s.Definition, "executionRoleArn": s.ExecutionRoleARN,
			"customerEncryptionKeyArn": s.CustomerEncryptionKeyARN,
		})
	})
}
