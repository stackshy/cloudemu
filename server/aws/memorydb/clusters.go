package memorydb

import (
	"net/http"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/memorydb"

	"github.com/stackshy/cloudemu/v2/server/wire"
	mdbdriver "github.com/stackshy/cloudemu/v2/services/memorydb/driver"
)

func (h *Handler) createCluster(w http.ResponseWriter, r *http.Request) {
	var in memorydb.CreateClusterInput
	if !wire.DecodeJSON(w, r, &in) {
		return
	}

	cfg := mdbdriver.CreateClusterConfig{
		Name:                    aws.ToString(in.ClusterName),
		Description:             aws.ToString(in.Description),
		NodeType:                aws.ToString(in.NodeType),
		Engine:                  aws.ToString(in.Engine),
		EngineVersion:           aws.ToString(in.EngineVersion),
		NumShards:               int(aws.ToInt32(in.NumShards)),
		NumReplicasPerShard:     in.NumReplicasPerShard,
		ACLName:                 aws.ToString(in.ACLName),
		ParameterGroupName:      aws.ToString(in.ParameterGroupName),
		SubnetGroupName:         aws.ToString(in.SubnetGroupName),
		SecurityGroupIDs:        in.SecurityGroupIds,
		Port:                    int(aws.ToInt32(in.Port)),
		TLSEnabled:              in.TLSEnabled,
		AutoMinorVersionUpgrade: aws.ToBool(in.AutoMinorVersionUpgrade),
		DataTiering:             aws.ToBool(in.DataTiering),
		KmsKeyID:                aws.ToString(in.KmsKeyId),
		MaintenanceWindow:       aws.ToString(in.MaintenanceWindow),
		SnapshotWindow:          aws.ToString(in.SnapshotWindow),
		SnapshotRetentionLimit:  int(aws.ToInt32(in.SnapshotRetentionLimit)),
		SnsTopicARN:             aws.ToString(in.SnsTopicArn),
		SnapshotName:            aws.ToString(in.SnapshotName),
		MultiRegionClusterName:  aws.ToString(in.MultiRegionClusterName),
		NetworkType:             string(in.NetworkType),
		IPDiscovery:             string(in.IpDiscovery),
		Tags:                    tagMap(in.Tags),
	}

	c, err := h.db.CreateCluster(r.Context(), cfg)
	if err != nil {
		writeErr(w, "Cluster", err)
		return
	}

	wire.WriteJSON(w, memorydb.CreateClusterOutput{Cluster: toWireCluster(c)})
}

//nolint:dupl // per-operation handlers share the decode-call-encode shape.
func (h *Handler) describeClusters(w http.ResponseWriter, r *http.Request) {
	var in memorydb.DescribeClustersInput
	if !wire.DecodeJSON(w, r, &in) {
		return
	}

	var names []string
	if in.ClusterName != nil {
		names = []string{aws.ToString(in.ClusterName)}
	}

	clusters, err := h.db.DescribeClusters(r.Context(), names)
	if err != nil {
		writeErr(w, "Cluster", err)
		return
	}

	page, next, err := paginate(clusters, in.MaxResults, in.NextToken)
	if err != nil {
		writeErr(w, "Cluster", err)
		return
	}

	out := memorydb.DescribeClustersOutput{NextToken: next}
	for i := range page {
		out.Clusters = append(out.Clusters, *toWireCluster(&page[i]))
	}

	wire.WriteJSON(w, out)
}

func (h *Handler) updateCluster(w http.ResponseWriter, r *http.Request) {
	var in memorydb.UpdateClusterInput
	if !wire.DecodeJSON(w, r, &in) {
		return
	}

	cfg := mdbdriver.UpdateClusterConfig{
		Name:               aws.ToString(in.ClusterName),
		Description:        aws.ToString(in.Description),
		NodeType:           aws.ToString(in.NodeType),
		EngineVersion:      aws.ToString(in.EngineVersion),
		ACLName:            aws.ToString(in.ACLName),
		ParameterGroupName: aws.ToString(in.ParameterGroupName),
		MaintenanceWindow:  aws.ToString(in.MaintenanceWindow),
		SnapshotWindow:     aws.ToString(in.SnapshotWindow),
		SnsTopicARN:        aws.ToString(in.SnsTopicArn),
		SnsTopicStatus:     aws.ToString(in.SnsTopicStatus),
		SecurityGroupIDs:   in.SecurityGroupIds,
	}

	if in.SnapshotRetentionLimit != nil {
		v := int(aws.ToInt32(in.SnapshotRetentionLimit))
		cfg.SnapshotRetentionLimit = &v
	}

	if in.ShardConfiguration != nil {
		v := int(in.ShardConfiguration.ShardCount)
		cfg.ShardCount = &v
	}

	if in.ReplicaConfiguration != nil {
		v := int(in.ReplicaConfiguration.ReplicaCount)
		cfg.ReplicaCount = &v
	}

	c, err := h.db.UpdateCluster(r.Context(), cfg)
	if err != nil {
		writeErr(w, "Cluster", err)
		return
	}

	wire.WriteJSON(w, memorydb.UpdateClusterOutput{Cluster: toWireCluster(c)})
}

func (h *Handler) deleteCluster(w http.ResponseWriter, r *http.Request) {
	var in memorydb.DeleteClusterInput
	if !wire.DecodeJSON(w, r, &in) {
		return
	}

	c, err := h.db.DeleteCluster(r.Context(), aws.ToString(in.ClusterName), aws.ToString(in.FinalSnapshotName))
	if err != nil {
		writeErr(w, "Cluster", err)
		return
	}

	wire.WriteJSON(w, memorydb.DeleteClusterOutput{Cluster: toWireCluster(c)})
}

func (h *Handler) failoverShard(w http.ResponseWriter, r *http.Request) {
	var in memorydb.FailoverShardInput
	if !wire.DecodeJSON(w, r, &in) {
		return
	}

	c, err := h.db.FailoverShard(r.Context(), aws.ToString(in.ClusterName), aws.ToString(in.ShardName))
	if err != nil {
		writeErr(w, "Cluster", err)
		return
	}

	wire.WriteJSON(w, memorydb.FailoverShardOutput{Cluster: toWireCluster(c)})
}

func (h *Handler) listAllowedNodeTypeUpdates(w http.ResponseWriter, r *http.Request) {
	var in memorydb.ListAllowedNodeTypeUpdatesInput
	if !wire.DecodeJSON(w, r, &in) {
		return
	}

	up, down, err := h.db.ListAllowedNodeTypeUpdates(r.Context(), aws.ToString(in.ClusterName))
	if err != nil {
		writeErr(w, "Cluster", err)
		return
	}

	wire.WriteJSON(w, memorydb.ListAllowedNodeTypeUpdatesOutput{
		ScaleUpNodeTypes: up, ScaleDownNodeTypes: down,
	})
}
