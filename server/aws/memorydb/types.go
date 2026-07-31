package memorydb

import (
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/memorydb/types"

	mdbdriver "github.com/stackshy/cloudemu/v2/services/memorydb/driver"
)

// i32 narrows a mock-scale int to int32.
//
//nolint:gosec // mock-scale values (shard/node counts, ports) never overflow int32.
func i32(n int) int32 { return int32(n) }

func toWireEndpoint(e mdbdriver.Endpoint) *types.Endpoint {
	if e.Address == "" {
		return nil
	}

	return &types.Endpoint{Address: aws.String(e.Address), Port: i32(e.Port)}
}

func toWireCluster(c *mdbdriver.Cluster) *types.Cluster {
	shards := make([]types.Shard, 0, len(c.Shards))

	for i := range c.Shards {
		s := c.Shards[i]
		nodes := make([]types.Node, 0, len(s.Nodes))

		for j := range s.Nodes {
			n := s.Nodes[j]
			nodes = append(nodes, types.Node{
				Name: aws.String(n.Name), Status: aws.String(n.Status),
				AvailabilityZone: aws.String(n.AvailabilityZone),
				// CreateTime omitted: AWS JSON 1.1 encodes timestamps as epoch
				// numbers, which encoding/json cannot emit for a time.Time.
				Endpoint: toWireEndpoint(n.Endpoint),
			})
		}

		shards = append(shards, types.Shard{
			Name: aws.String(s.Name), Status: aws.String(s.Status), Slots: aws.String(s.Slots),
			NumberOfNodes: aws.Int32(i32(s.NumberOfNodes)), Nodes: nodes,
		})
	}

	sgs := make([]types.SecurityGroupMembership, 0, len(c.SecurityGroups))
	for _, sg := range c.SecurityGroups {
		sgs = append(sgs, types.SecurityGroupMembership{SecurityGroupId: aws.String(sg.SecurityGroupID), Status: aws.String(sg.Status)})
	}

	out := &types.Cluster{
		Name: aws.String(c.Name), ARN: aws.String(c.ARN), Description: aws.String(c.Description),
		Status: aws.String(c.Status), NodeType: aws.String(c.NodeType), Engine: aws.String(c.Engine),
		EngineVersion: aws.String(c.EngineVersion), EnginePatchVersion: aws.String(c.EnginePatchVersion),
		NumberOfShards: aws.Int32(i32(c.NumberOfShards)), ACLName: aws.String(c.ACLName),
		ParameterGroupName: aws.String(c.ParameterGroupName), ParameterGroupStatus: aws.String(c.ParameterGroupStatus),
		SubnetGroupName: aws.String(c.SubnetGroupName), SecurityGroups: sgs, Shards: shards,
		ClusterEndpoint: toWireEndpoint(c.ClusterEndpoint), TLSEnabled: aws.Bool(c.TLSEnabled),
		KmsKeyId: aws.String(c.KmsKeyID), MaintenanceWindow: aws.String(c.MaintenanceWindow),
		SnapshotWindow: aws.String(c.SnapshotWindow), SnapshotRetentionLimit: aws.Int32(i32(c.SnapshotRetentionLimit)),
		SnsTopicArn: aws.String(c.SnsTopicARN), AutoMinorVersionUpgrade: aws.Bool(c.AutoMinorVersionUpgrade),
		DataTiering: dataTieringStatus(c.DataTiering), AvailabilityMode: azStatus(c.AvailabilityMode),
		NetworkType: types.NetworkType(c.NetworkType), IpDiscovery: types.IpDiscovery(c.IPDiscovery),
	}

	if c.MultiRegionClusterName != "" {
		out.MultiRegionClusterName = aws.String(c.MultiRegionClusterName)
	}

	return out
}

func dataTieringStatus(on bool) types.DataTieringStatus {
	if on {
		return types.DataTieringStatusTrue
	}

	return types.DataTieringStatusFalse
}

func azStatus(mode string) types.AZStatus {
	if mode == "MultiAZ" {
		return types.AZStatusMultiAZ
	}

	return types.AZStatusSingleAZ
}

func toWireACL(a *mdbdriver.ACL) *types.ACL {
	return &types.ACL{
		Name: aws.String(a.Name), ARN: aws.String(a.ARN), Status: aws.String(a.Status),
		MinimumEngineVersion: aws.String(a.MinimumEngineVersion),
		UserNames:            a.UserNames, Clusters: a.Clusters,
	}
}

func toWireUser(u *mdbdriver.User) *types.User {
	return &types.User{
		Name: aws.String(u.Name), ARN: aws.String(u.ARN), Status: aws.String(u.Status),
		AccessString: aws.String(u.AccessString), MinimumEngineVersion: aws.String(u.MinimumEngineVersion),
		ACLNames: u.ACLNames,
		Authentication: &types.Authentication{
			Type: types.AuthenticationType(u.Authentication.Type), PasswordCount: aws.Int32(i32(u.Authentication.PasswordCount)),
		},
	}
}

func toWireParameterGroup(p *mdbdriver.ParameterGroup) *types.ParameterGroup {
	return &types.ParameterGroup{
		Name: aws.String(p.Name), ARN: aws.String(p.ARN),
		Family: aws.String(p.Family), Description: aws.String(p.Description),
	}
}

func toWireParameter(p *mdbdriver.Parameter) types.Parameter {
	return types.Parameter{
		Name: aws.String(p.Name), Value: aws.String(p.Value), Description: aws.String(p.Description),
		DataType: aws.String(p.DataType), AllowedValues: aws.String(p.AllowedValues),
		MinimumEngineVersion: aws.String(p.MinimumEngineVersion),
	}
}

func toWireSubnetGroup(g *mdbdriver.SubnetGroup) *types.SubnetGroup {
	subnets := make([]types.Subnet, 0, len(g.Subnets))
	for _, s := range g.Subnets {
		subnets = append(subnets, types.Subnet{
			Identifier:       aws.String(s.Identifier),
			AvailabilityZone: &types.AvailabilityZone{Name: aws.String(s.AvailabilityZone)},
		})
	}

	return &types.SubnetGroup{
		Name: aws.String(g.Name), ARN: aws.String(g.ARN), Description: aws.String(g.Description),
		VpcId: aws.String(g.VpcID), Subnets: subnets,
	}
}

func toWireSnapshot(s *mdbdriver.Snapshot) *types.Snapshot {
	cc := s.ClusterConfiguration

	return &types.Snapshot{
		Name: aws.String(s.Name), ARN: aws.String(s.ARN), Status: aws.String(s.Status),
		Source: aws.String(s.Source), KmsKeyId: aws.String(s.KmsKeyID),
		DataTiering: dataTieringStatus(s.DataTiering),
		ClusterConfiguration: &types.ClusterConfiguration{
			Name: aws.String(cc.Name), NodeType: aws.String(cc.NodeType), Engine: aws.String(cc.Engine),
			EngineVersion: aws.String(cc.EngineVersion), ParameterGroupName: aws.String(cc.ParameterGroupName),
			SubnetGroupName: aws.String(cc.SubnetGroupName), VpcId: aws.String(cc.VpcID),
			MaintenanceWindow: aws.String(cc.MaintenanceWindow), SnapshotWindow: aws.String(cc.SnapshotWindow),
			TopicArn: aws.String(cc.TopicARN), NumShards: aws.Int32(i32(cc.NumShards)),
			Port: aws.Int32(i32(cc.Port)), SnapshotRetentionLimit: aws.Int32(i32(cc.SnapshotRetentionLimit)),
		},
	}
}

func toWireMultiRegion(c *mdbdriver.MultiRegionCluster) *types.MultiRegionCluster {
	members := make([]types.RegionalCluster, 0, len(c.Members))
	for _, r := range c.Members {
		members = append(members, types.RegionalCluster{
			ClusterName: aws.String(r.ClusterName), Region: aws.String(r.Region),
			Status: aws.String(r.Status), ARN: aws.String(r.ARN),
		})
	}

	return &types.MultiRegionCluster{
		MultiRegionClusterName: aws.String(c.Name), ARN: aws.String(c.ARN), Status: aws.String(c.Status),
		NodeType: aws.String(c.NodeType), Engine: aws.String(c.Engine), EngineVersion: aws.String(c.EngineVersion),
		NumberOfShards: aws.Int32(i32(c.NumberOfShards)), TLSEnabled: aws.Bool(c.TLSEnabled),
		MultiRegionParameterGroupName: aws.String(c.MultiRegionParameterGroupName), Clusters: members,
	}
}

func toWireTags(tags []mdbdriver.Tag) []types.Tag {
	out := make([]types.Tag, 0, len(tags))
	for _, t := range tags {
		out = append(out, types.Tag{Key: aws.String(t.Key), Value: aws.String(t.Value)})
	}

	return out
}

func mrParamGroupWire(g mdbdriver.MultiRegionParameterGroup) types.MultiRegionParameterGroup {
	return types.MultiRegionParameterGroup{
		Name: aws.String(g.Name), ARN: aws.String(g.ARN),
		Family: aws.String(g.Family), Description: aws.String(g.Description),
	}
}

func mrParamWire(p *mdbdriver.Parameter) types.MultiRegionParameter {
	return types.MultiRegionParameter{
		Name: aws.String(p.Name), Value: aws.String(p.Value), Description: aws.String(p.Description),
		DataType: aws.String(p.DataType), AllowedValues: aws.String(p.AllowedValues),
		MinimumEngineVersion: aws.String(p.MinimumEngineVersion),
	}
}

func serviceUpdateWire(s *mdbdriver.ServiceUpdate) types.ServiceUpdate {
	return types.ServiceUpdate{
		ClusterName: aws.String(s.ClusterName), ServiceUpdateName: aws.String(s.ServiceUpdateName),
		Description: aws.String(s.Description), Status: types.ServiceUpdateStatus(s.Status),
		Type: types.ServiceUpdateType(s.Type), Engine: aws.String(s.Engine),
		NodesUpdated: aws.String(s.NodesUpdated),
		// ReleaseDate/AutoUpdateStartDate omitted: AWS JSON 1.1 encodes
		// timestamps as epoch numbers, which encoding/json cannot emit for a
		// time.Time.
	}
}

func unprocessedWire(u *mdbdriver.UnprocessedCluster) types.UnprocessedCluster {
	return types.UnprocessedCluster{
		ClusterName: aws.String(u.ClusterName), ErrorType: aws.String(u.ErrorType),
		ErrorMessage: aws.String(u.ErrorMessage),
	}
}

func reservedNodeWire(n *mdbdriver.ReservedNode) types.ReservedNode {
	return types.ReservedNode{
		ReservationId: aws.String(n.ReservationID), ReservedNodesOfferingId: aws.String(n.ReservedNodesOfferingID),
		NodeType: aws.String(n.NodeType), NodeCount: i32(n.NodeCount), Duration: i32(n.Duration),
		FixedPrice: n.FixedPrice, OfferingType: aws.String(n.OfferingType), State: aws.String(n.State),
		// StartTime omitted: AWS JSON 1.1 encodes timestamps as epoch numbers,
		// which encoding/json cannot emit for a time.Time.
	}
}

func offeringWire(o *mdbdriver.ReservedNodesOffering) types.ReservedNodesOffering {
	return types.ReservedNodesOffering{
		ReservedNodesOfferingId: aws.String(o.OfferingID), NodeType: aws.String(o.NodeType),
		Duration: i32(o.Duration), FixedPrice: o.FixedPrice, OfferingType: aws.String(o.OfferingType),
	}
}

// tagMap converts SDK request tags to a plain map.
func tagMap(in []types.Tag) map[string]string {
	if len(in) == 0 {
		return nil
	}

	out := make(map[string]string, len(in))
	for _, t := range in {
		out[aws.ToString(t.Key)] = aws.ToString(t.Value)
	}

	return out
}
