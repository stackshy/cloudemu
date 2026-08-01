package memorydb

import (
	"net/http"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/memorydb"

	"github.com/stackshy/cloudemu/v2/server/wire"
	mdbdriver "github.com/stackshy/cloudemu/v2/services/memorydb/driver"
)

func (h *Handler) createParameterGroup(w http.ResponseWriter, r *http.Request) {
	var in memorydb.CreateParameterGroupInput
	if !wire.DecodeJSON(w, r, &in) {
		return
	}

	pg, err := h.db.CreateParameterGroup(r.Context(),
		aws.ToString(in.ParameterGroupName), aws.ToString(in.Family), aws.ToString(in.Description), tagMap(in.Tags))
	if err != nil {
		writeErr(w, "ParameterGroup", err)
		return
	}

	wire.WriteJSON(w, memorydb.CreateParameterGroupOutput{ParameterGroup: toWireParameterGroup(pg)})
}

//nolint:dupl // per-operation handlers share the decode-call-encode shape.
func (h *Handler) describeParameterGroups(w http.ResponseWriter, r *http.Request) {
	var in memorydb.DescribeParameterGroupsInput
	if !wire.DecodeJSON(w, r, &in) {
		return
	}

	var names []string
	if in.ParameterGroupName != nil {
		names = []string{aws.ToString(in.ParameterGroupName)}
	}

	groups, err := h.db.DescribeParameterGroups(r.Context(), names)
	if err != nil {
		writeErr(w, "ParameterGroup", err)
		return
	}

	page, next, err := paginate(groups, in.MaxResults, in.NextToken)
	if err != nil {
		writeErr(w, "ParameterGroup", err)
		return
	}

	out := memorydb.DescribeParameterGroupsOutput{NextToken: next}
	for i := range page {
		out.ParameterGroups = append(out.ParameterGroups, *toWireParameterGroup(&page[i]))
	}

	wire.WriteJSON(w, out)
}

func (h *Handler) updateParameterGroup(w http.ResponseWriter, r *http.Request) {
	var in memorydb.UpdateParameterGroupInput
	if !wire.DecodeJSON(w, r, &in) {
		return
	}

	params := make([]mdbdriver.ParameterNameValue, 0, len(in.ParameterNameValues))
	for _, p := range in.ParameterNameValues {
		params = append(params, mdbdriver.ParameterNameValue{Name: aws.ToString(p.ParameterName), Value: aws.ToString(p.ParameterValue)})
	}

	pg, err := h.db.UpdateParameterGroup(r.Context(), aws.ToString(in.ParameterGroupName), params)
	if err != nil {
		writeErr(w, "ParameterGroup", err)
		return
	}

	wire.WriteJSON(w, memorydb.UpdateParameterGroupOutput{ParameterGroup: toWireParameterGroup(pg)})
}

func (h *Handler) resetParameterGroup(w http.ResponseWriter, r *http.Request) {
	var in memorydb.ResetParameterGroupInput
	if !wire.DecodeJSON(w, r, &in) {
		return
	}

	names := make([]string, 0, len(in.ParameterNames))
	names = append(names, in.ParameterNames...)

	pg, err := h.db.ResetParameterGroup(r.Context(), aws.ToString(in.ParameterGroupName), in.AllParameters, names)
	if err != nil {
		writeErr(w, "ParameterGroup", err)
		return
	}

	wire.WriteJSON(w, memorydb.ResetParameterGroupOutput{ParameterGroup: toWireParameterGroup(pg)})
}

func (h *Handler) deleteParameterGroup(w http.ResponseWriter, r *http.Request) {
	var in memorydb.DeleteParameterGroupInput
	if !wire.DecodeJSON(w, r, &in) {
		return
	}

	pg, err := h.db.DeleteParameterGroup(r.Context(), aws.ToString(in.ParameterGroupName))
	if err != nil {
		writeErr(w, "ParameterGroup", err)
		return
	}

	wire.WriteJSON(w, memorydb.DeleteParameterGroupOutput{ParameterGroup: toWireParameterGroup(pg)})
}

func (h *Handler) describeParameters(w http.ResponseWriter, r *http.Request) {
	var in memorydb.DescribeParametersInput
	if !wire.DecodeJSON(w, r, &in) {
		return
	}

	params, err := h.db.DescribeParameters(r.Context(), aws.ToString(in.ParameterGroupName))
	if err != nil {
		writeErr(w, "ParameterGroup", err)
		return
	}

	page, next, err := paginate(params, in.MaxResults, in.NextToken)
	if err != nil {
		writeErr(w, "ParameterGroup", err)
		return
	}

	out := memorydb.DescribeParametersOutput{NextToken: next}
	for i := range page {
		out.Parameters = append(out.Parameters, toWireParameter(&page[i]))
	}

	wire.WriteJSON(w, out)
}
