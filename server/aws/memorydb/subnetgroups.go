package memorydb

import (
	"net/http"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/memorydb"

	"github.com/stackshy/cloudemu/v2/server/wire"
	mdbdriver "github.com/stackshy/cloudemu/v2/services/memorydb/driver"
)

func (h *Handler) createSubnetGroup(w http.ResponseWriter, r *http.Request) {
	var in memorydb.CreateSubnetGroupInput
	if !wire.DecodeJSON(w, r, &in) {
		return
	}

	g, err := h.db.CreateSubnetGroup(r.Context(), mdbdriver.CreateSubnetGroupConfig{
		Name: aws.ToString(in.SubnetGroupName), Description: aws.ToString(in.Description),
		SubnetIDs: in.SubnetIds, Tags: tagMap(in.Tags),
	})
	if err != nil {
		writeErr(w, "SubnetGroup", err)
		return
	}

	wire.WriteJSON(w, memorydb.CreateSubnetGroupOutput{SubnetGroup: toWireSubnetGroup(g)})
}

//nolint:dupl // per-operation handlers share the decode-call-encode shape.
func (h *Handler) describeSubnetGroups(w http.ResponseWriter, r *http.Request) {
	var in memorydb.DescribeSubnetGroupsInput
	if !wire.DecodeJSON(w, r, &in) {
		return
	}

	var names []string
	if in.SubnetGroupName != nil {
		names = []string{aws.ToString(in.SubnetGroupName)}
	}

	groups, err := h.db.DescribeSubnetGroups(r.Context(), names)
	if err != nil {
		writeErr(w, "SubnetGroup", err)
		return
	}

	out := memorydb.DescribeSubnetGroupsOutput{}
	for i := range groups {
		out.SubnetGroups = append(out.SubnetGroups, *toWireSubnetGroup(&groups[i]))
	}

	wire.WriteJSON(w, out)
}

func (h *Handler) updateSubnetGroup(w http.ResponseWriter, r *http.Request) {
	var in memorydb.UpdateSubnetGroupInput
	if !wire.DecodeJSON(w, r, &in) {
		return
	}

	g, err := h.db.UpdateSubnetGroup(r.Context(), mdbdriver.UpdateSubnetGroupConfig{
		Name: aws.ToString(in.SubnetGroupName), Description: aws.ToString(in.Description), SubnetIDs: in.SubnetIds,
	})
	if err != nil {
		writeErr(w, "SubnetGroup", err)
		return
	}

	wire.WriteJSON(w, memorydb.UpdateSubnetGroupOutput{SubnetGroup: toWireSubnetGroup(g)})
}

func (h *Handler) deleteSubnetGroup(w http.ResponseWriter, r *http.Request) {
	var in memorydb.DeleteSubnetGroupInput
	if !wire.DecodeJSON(w, r, &in) {
		return
	}

	g, err := h.db.DeleteSubnetGroup(r.Context(), aws.ToString(in.SubnetGroupName))
	if err != nil {
		writeErr(w, "SubnetGroup", err)
		return
	}

	wire.WriteJSON(w, memorydb.DeleteSubnetGroupOutput{SubnetGroup: toWireSubnetGroup(g)})
}
