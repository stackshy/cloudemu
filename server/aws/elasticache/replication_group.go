package elasticache

import (
	"encoding/xml"
	"net/http"
	"strconv"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	cachedriver "github.com/stackshy/cloudemu/v2/services/cache/driver"
)

// formTrue is the query-protocol encoding of a boolean-true flag.
const formTrue = "true"

// nodeGroupMemberXML mirrors AWS's NodeGroupMember — the per-node membership
// record a caller reads to enumerate the primary and replicas of a shard.
type nodeGroupMemberXML struct {
	CacheClusterID string `xml:"CacheClusterId"`
	CacheNodeID    string `xml:"CacheNodeId"`
	CurrentRole    string `xml:"CurrentRole,omitempty"`
}

type nodeGroupXML struct {
	NodeGroupID      string               `xml:"NodeGroupId"`
	Status           string               `xml:"Status"`
	PrimaryEndpoint  *endpointXML         `xml:"PrimaryEndpoint,omitempty"`
	ReaderEndpoint   *endpointXML         `xml:"ReaderEndpoint,omitempty"`
	NodeGroupMembers []nodeGroupMemberXML `xml:"NodeGroupMembers>NodeGroupMember,omitempty"`
}

type replicationGroupXML struct {
	ReplicationGroupID string         `xml:"ReplicationGroupId"`
	Description        string         `xml:"Description,omitempty"`
	Status             string         `xml:"Status"`
	CacheNodeType      string         `xml:"CacheNodeType,omitempty"`
	AutomaticFailover  string         `xml:"AutomaticFailover,omitempty"`
	MemberClusters     []string       `xml:"MemberClusters>ClusterId,omitempty"`
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
		writeUnsupported(w, "replication groups")
		return
	}

	nodes, err := parseNodeCount("NumCacheClusters", r.Form.Get("NumCacheClusters"))
	if err != nil {
		writeErr(w, err)
		return
	}

	rg, err := store.CreateReplicationGroup(r.Context(), cachedriver.ReplicationGroupConfig{
		ID:                       r.Form.Get("ReplicationGroupId"),
		Description:              r.Form.Get("ReplicationGroupDescription"),
		Engine:                   r.Form.Get("Engine"),
		EngineVersion:            r.Form.Get("EngineVersion"),
		NodeType:                 r.Form.Get("CacheNodeType"),
		NumCacheNodes:            nodes,
		SubnetGroupName:          r.Form.Get("CacheSubnetGroupName"),
		SecurityGroupIDs:         awsquery.ListStrings(r.Form, "SecurityGroupIds.SecurityGroupId"),
		AutomaticFailoverEnabled: r.Form.Get("AutomaticFailoverEnabled") == formTrue,
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
		writeUnsupported(w, "replication groups")
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
		writeUnsupported(w, "replication groups")
		return
	}

	nodes, err := parseNodeCount("NumCacheClusters", r.Form.Get("NumCacheClusters"))
	if err != nil {
		writeErr(w, err)
		return
	}

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
		writeUnsupported(w, "replication groups")
		return
	}

	id := r.Form.Get("ReplicationGroupId")

	groups, err := store.DescribeReplicationGroups(r.Context(), []string{id})
	if err != nil {
		writeErr(w, err)
		return
	}

	last := groups[0]

	opts := cachedriver.DeleteReplicationGroupOptions{
		RetainPrimaryCluster:    r.Form.Get("RetainPrimaryCluster") == formTrue,
		FinalSnapshotIdentifier: r.Form.Get("FinalSnapshotIdentifier"),
	}

	if err := store.DeleteReplicationGroup(r.Context(), id, opts); err != nil {
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

// parseNodeCount reads a node-count form field (NumCacheClusters for
// replication groups, NumCacheNodes for cache clusters). Absent means
// "unspecified" and the driver picks a default; present-but-unparseable is a
// caller error, and coercing it to a node count silently builds something other
// than what was asked for.
func parseNodeCount(field, raw string) (int, error) {
	if raw == "" {
		return 0, nil
	}

	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, cerrors.Newf(cerrors.InvalidArgument,
			"%s must be a number, got %q", field, raw)
	}

	return n, nil
}

func toReplicationGroupXML(rg *cachedriver.ReplicationGroup) replicationGroupXML {
	x := replicationGroupXML{
		ReplicationGroupID: rg.ID,
		Description:        rg.Description,
		Status:             rg.Status,
		CacheNodeType:      rg.NodeType,
		AutomaticFailover:  rg.AutomaticFailover,
		MemberClusters:     rg.MemberClusters,
		ARN:                rg.ARN,
	}

	// The primary endpoint is how a caller reaches the cache at all — it reads
	// NodeGroups[0].PrimaryEndpoint.Address to build the connection string, so
	// the node group has to be present even for a single-node group. The reader
	// endpoint and per-node membership let clients scale reads and enumerate the
	// group's nodes.
	if rg.PrimaryAddress != "" {
		ng := nodeGroupXML{
			NodeGroupID:      "0001",
			Status:           rg.Status,
			PrimaryEndpoint:  &endpointXML{Address: rg.PrimaryAddress, Port: rg.PrimaryPort},
			NodeGroupMembers: nodeGroupMembers(rg.MemberClusters),
		}

		if rg.ReaderAddress != "" {
			ng.ReaderEndpoint = &endpointXML{Address: rg.ReaderAddress, Port: rg.ReaderPort}
		}

		x.NodeGroups = []nodeGroupXML{ng}
	}

	return x
}

// nodeGroupMembers builds the per-node membership list from the group's member
// cluster ids: the first is the primary, the rest are replicas.
func nodeGroupMembers(members []string) []nodeGroupMemberXML {
	out := make([]nodeGroupMemberXML, 0, len(members))

	for i, id := range members {
		role := "replica"
		if i == 0 {
			role = "primary"
		}

		out = append(out, nodeGroupMemberXML{
			CacheClusterID: id,
			CacheNodeID:    "0001",
			CurrentRole:    role,
		})
	}

	return out
}
