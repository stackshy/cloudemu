package networkfirewall

import (
	"encoding/json"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
	nfdriver "github.com/stackshy/cloudemu/v2/services/networkfirewall/driver"
)

type flowScopeRequest struct {
	FirewallArn               string `json:"FirewallArn"`
	AvailabilityZone          string `json:"AvailabilityZone"`
	VpcEndpointID             string `json:"VpcEndpointId"`
	VpcEndpointAssociationArn string `json:"VpcEndpointAssociationArn"`
}

func (s *flowScopeRequest) scope() nfdriver.FlowOperationScope {
	return nfdriver.FlowOperationScope{
		FirewallARN: s.FirewallArn, AvailabilityZone: s.AvailabilityZone,
		VPCEndpointID: s.VpcEndpointID, VPCEndpointAssociationARN: s.VpcEndpointAssociationArn,
	}
}

type startFlowRequest struct {
	flowScopeRequest
	FlowFilters             json.RawMessage `json:"FlowFilters"`
	MinimumFlowAgeInSeconds *int32          `json:"MinimumFlowAgeInSeconds"`
}

func (s *startFlowRequest) request() nfdriver.FlowOperationRequest {
	return nfdriver.FlowOperationRequest{
		FirewallARN: s.FirewallArn, AvailabilityZone: s.AvailabilityZone, VPCEndpointID: s.VpcEndpointID,
		VPCEndpointAssociationARN: s.VpcEndpointAssociationArn,
		MinimumFlowAgeSeconds:     s.MinimumFlowAgeInSeconds, Filters: s.FlowFilters,
	}
}

func (h *Handler) startFlowCapture(w http.ResponseWriter, r *http.Request) {
	h.startFlow(w, r, func(f nfdriver.FlowOperations, req nfdriver.FlowOperationRequest) (*nfdriver.FlowOperation, error) {
		return f.StartFlowCapture(r.Context(), req)
	})
}

func (h *Handler) startFlowFlush(w http.ResponseWriter, r *http.Request) {
	h.startFlow(w, r, func(f nfdriver.FlowOperations, req nfdriver.FlowOperationRequest) (*nfdriver.FlowOperation, error) {
		return f.StartFlowFlush(r.Context(), req)
	})
}

func (h *Handler) startFlow(
	w http.ResponseWriter, r *http.Request,
	start func(nfdriver.FlowOperations, nfdriver.FlowOperationRequest) (*nfdriver.FlowOperation, error),
) {
	var req startFlowRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	flows, ok := capability[nfdriver.FlowOperations](h, w)
	if !ok {
		return
	}

	op, err := start(flows, req.request())
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{
		"FirewallArn": op.FirewallARN, "FlowOperationId": op.ID, "FlowOperationStatus": op.Status,
	})
}

type describeFlowRequest struct {
	flowScopeRequest
	FlowOperationID string `json:"FlowOperationId"`
}

func (h *Handler) describeFlowOperation(w http.ResponseWriter, r *http.Request) {
	var req describeFlowRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	flows, ok := capability[nfdriver.FlowOperations](h, w)
	if !ok {
		return
	}

	op, err := flows.DescribeFlowOperation(r.Context(), req.scope(), req.FlowOperationID)
	if err != nil {
		writeErr(w, err)
		return
	}

	resp := flowOperationFields(op)
	resp["FlowOperation"] = map[string]any{
		"FlowFilters": op.Filters, "MinimumFlowAgeInSeconds": op.MinimumFlowAgeSeconds,
	}
	resp["FlowOperationType"] = op.Type

	wire.WriteJSON(w, resp)
}

// flowOperationFields are the response members DescribeFlowOperation and
// ListFlowOperationResults share.
func flowOperationFields(op *nfdriver.FlowOperation) map[string]any {
	resp := map[string]any{
		"FirewallArn": op.FirewallARN, "FlowOperationId": op.ID, "FlowOperationStatus": op.Status,
		"FlowRequestTimestamp": epochSeconds(op.RequestedAt),
	}
	if op.AvailabilityZone != "" {
		resp["AvailabilityZone"] = op.AvailabilityZone
	}

	if op.VPCEndpointID != "" {
		resp["VpcEndpointId"] = op.VPCEndpointID
	}

	if op.VPCEndpointAssociationARN != "" {
		resp["VpcEndpointAssociationArn"] = op.VPCEndpointAssociationARN
	}

	if op.StatusMessage != "" {
		resp["StatusMessage"] = op.StatusMessage
	}

	return resp
}

type listFlowOperationsRequest struct {
	flowScopeRequest
	listRequest
	FlowOperationType string `json:"FlowOperationType"`
}

func (h *Handler) listFlowOperations(w http.ResponseWriter, r *http.Request) {
	var req listFlowOperationsRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	flows, ok := capability[nfdriver.FlowOperations](h, w)
	if !ok {
		return
	}

	items, next, err := flows.ListFlowOperations(r.Context(), req.scope(), req.FlowOperationType, req.options())
	if err != nil {
		writeErr(w, err)
		return
	}

	metas := make([]map[string]any, 0, len(items))
	for i := range items {
		metas = append(metas, map[string]any{
			"FlowOperationId": items[i].ID, "FlowOperationType": items[i].Type,
			"FlowOperationStatus": items[i].Status, "FlowRequestTimestamp": epochSeconds(items[i].RequestedAt),
		})
	}

	wire.WriteJSON(w, withNextToken(map[string]any{"FlowOperations": metas}, next))
}

type listFlowResultsRequest struct {
	flowScopeRequest
	listRequest
	FlowOperationID string `json:"FlowOperationId"`
}

func (h *Handler) listFlowOperationResults(w http.ResponseWriter, r *http.Request) {
	var req listFlowResultsRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	flows, ok := capability[nfdriver.FlowOperations](h, w)
	if !ok {
		return
	}

	op, next, err := flows.ListFlowOperationResults(r.Context(), req.scope(), req.FlowOperationID, req.options())
	if err != nil {
		writeErr(w, err)
		return
	}

	resp := flowOperationFields(op)
	resp["Flows"] = []map[string]any{}

	wire.WriteJSON(w, withNextToken(resp, next))
}
