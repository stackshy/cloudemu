package elasticache

import (
	"encoding/xml"
	"net/http"
	"strconv"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	cachedriver "github.com/stackshy/cloudemu/v2/services/cache/driver"
)

type nodeGroupXML struct {
	NodeGroupID     string       `xml:"NodeGroupId"`
	Status          string       `xml:"Status"`
	PrimaryEndpoint *endpointXML `xml:"PrimaryEndpoint,omitempty"`
}

type replicationGroupXML struct {
	ReplicationGroupID string         `xml:"ReplicationGroupId"`
	Description        string         `xml:"Description,omitempty"`
	Status             string         `xml:"Status"`
	CacheNodeType      string         `xml:"CacheNodeType,omitempty"`
	ARN                string         `xml:"ARN,omitempty"`
	NodeGroups         []nodeGroupXML `xml:"NodeGroups>NodeGroup,omitempty"`
}

type replicationGroupResult struct {
	ReplicationGroup replicationGroupXML `xml:"ReplicationGroup"`
}

type createReplicationGroupResponse struct {
	XMLName  xml.Name               `xml:"CreateReplicationGroupResponse"`
	Xmlns    string                 `xml:"xmlns,attr"`
	Result   replicationGroupResult `xml:"CreateReplicationGroupResult"`
	Metadata responseMetadata       `xml:"ResponseMetadata"`
}

type modifyReplicationGroupResponse struct {
	XMLName  xml.Name               `xml:"ModifyReplicationGroupResponse"`
	Xmlns    string                 `xml:"xmlns,attr"`
	Result   replicationGroupResult `xml:"ModifyReplicationGroupResult"`
	Metadata responseMetadata       `xml:"ResponseMetadata"`
}

type deleteReplicationGroupResponse struct {
	XMLName  xml.Name               `xml:"DeleteReplicationGroupResponse"`
	Xmlns    string                 `xml:"xmlns,attr"`
	Result   replicationGroupResult `xml:"DeleteReplicationGroupResult"`
	Metadata responseMetadata       `xml:"ResponseMetadata"`
}

type replicationGroupsList struct {
	ReplicationGroups []replicationGroupXML `xml:"ReplicationGroups>ReplicationGroup"`
}

type describeReplicationGroupsResponse struct {
	XMLName  xml.Name              `xml:"DescribeReplicationGroupsResponse"`
	Xmlns    string                `xml:"xmlns,attr"`
	Result   replicationGroupsList `xml:"DescribeReplicationGroupsResult"`
	Metadata responseMetadata      `xml:"ResponseMetadata"`
}

func (h *Handler) replicationGroups() (cachedriver.ReplicationGroups, bool) {
	rg, ok := h.cache.(cachedriver.ReplicationGroups)

	return rg, ok
}

func (h *Handler) createReplicationGroup(w http.ResponseWriter, r *http.Request) {
	store, ok := h.replicationGroups()
	if !ok {
		writeErr(w, errUnsupportedReplicationGroups())
		return
	}

	nodes, _ := strconv.Atoi(r.Form.Get("NumCacheClusters"))

	rg, err := store.CreateReplicationGroup(r.Context(), cachedriver.ReplicationGroupConfig{
		ID:               r.Form.Get("ReplicationGroupId"),
		Description:      r.Form.Get("ReplicationGroupDescription"),
		Engine:           r.Form.Get("Engine"),
		EngineVersion:    r.Form.Get("EngineVersion"),
		NodeType:         r.Form.Get("CacheNodeType"),
		NumCacheNodes:    nodes,
		SubnetGroupName:  r.Form.Get("CacheSubnetGroupName"),
		SecurityGroupIDs: awsquery.ListStrings(r.Form, "SecurityGroupIds.SecurityGroupId"),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, createReplicationGroupResponse{
		Xmlns:    Namespace,
		Result:   replicationGroupResult{ReplicationGroup: toReplicationGroupXML(rg)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

//nolint:dupl // per-resource describe pattern; the sibling reads the other collection
func (h *Handler) describeReplicationGroups(w http.ResponseWriter, r *http.Request) {
	store, ok := h.replicationGroups()
	if !ok {
		writeErr(w, errUnsupportedReplicationGroups())
		return
	}

	var ids []string
	if id := r.Form.Get("ReplicationGroupId"); id != "" {
		ids = []string{id}
	}

	groups, err := store.DescribeReplicationGroups(r.Context(), ids)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := make([]replicationGroupXML, 0, len(groups))
	for i := range groups {
		out = append(out, toReplicationGroupXML(&groups[i]))
	}

	awsquery.WriteXMLResponse(w, describeReplicationGroupsResponse{
		Xmlns:    Namespace,
		Result:   replicationGroupsList{ReplicationGroups: out},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) modifyReplicationGroup(w http.ResponseWriter, r *http.Request) {
	store, ok := h.replicationGroups()
	if !ok {
		writeErr(w, errUnsupportedReplicationGroups())
		return
	}

	nodes, _ := strconv.Atoi(r.Form.Get("NumCacheClusters"))

	rg, err := store.ModifyReplicationGroup(r.Context(), r.Form.Get("ReplicationGroupId"), nodes)
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, modifyReplicationGroupResponse{
		Xmlns:    Namespace,
		Result:   replicationGroupResult{ReplicationGroup: toReplicationGroupXML(rg)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

// deleteReplicationGroup echoes the group back in the response, as real
// ElastiCache does — the delete is asynchronous there and the caller is handed
// the record it just asked to remove.
func (h *Handler) deleteReplicationGroup(w http.ResponseWriter, r *http.Request) {
	store, ok := h.replicationGroups()
	if !ok {
		writeErr(w, errUnsupportedReplicationGroups())
		return
	}

	id := r.Form.Get("ReplicationGroupId")

	groups, err := store.DescribeReplicationGroups(r.Context(), []string{id})
	if err != nil {
		writeErr(w, err)
		return
	}

	last := groups[0]

	if err := store.DeleteReplicationGroup(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}

	last.Status = "deleting"

	awsquery.WriteXMLResponse(w, deleteReplicationGroupResponse{
		Xmlns:    Namespace,
		Result:   replicationGroupResult{ReplicationGroup: toReplicationGroupXML(&last)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func errUnsupportedReplicationGroups() error {
	return cerrors.New(cerrors.InvalidArgument,
		"InvalidAction: this driver does not model replication groups")
}

func toReplicationGroupXML(rg *cachedriver.ReplicationGroup) replicationGroupXML {
	x := replicationGroupXML{
		ReplicationGroupID: rg.ID,
		Description:        rg.Description,
		Status:             rg.Status,
		CacheNodeType:      rg.NodeType,
		ARN:                rg.ARN,
	}

	// The primary endpoint is how a caller reaches the cache at all — it reads
	// NodeGroups[0].PrimaryEndpoint.Address to build the connection string, so
	// the node group has to be present even for a single-node group.
	if rg.PrimaryAddress != "" {
		x.NodeGroups = []nodeGroupXML{{
			NodeGroupID:     "0001",
			Status:          rg.Status,
			PrimaryEndpoint: &endpointXML{Address: rg.PrimaryAddress, Port: rg.PrimaryPort},
		}}
	}

	return x
}
