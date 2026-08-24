package redshift

import (
	"encoding/xml"
	"strconv"

	rdbdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

// Redshift query-protocol responses are wrapped in <FooResponse> with a
// <FooResult> child and a trailing <ResponseMetadata>. The structures below
// mirror the AWS-published XML closely enough that aws-sdk-go-v2's Redshift
// unmarshalers consume them without complaint.

type responseMetadata struct {
	RequestID string `xml:"RequestId"`
}

type endpointXML struct {
	Address string `xml:"Address,omitempty"`
	Port    int    `xml:"Port,omitempty"`
}

type tagXML struct {
	Key   string `xml:"Key"`
	Value string `xml:"Value"`
}

type tagsXML struct {
	Tag []tagXML `xml:"Tag,omitempty"`
}

type vpcSecurityGroupXML struct {
	VpcSecurityGroupID string `xml:"VpcSecurityGroupId"`
	Status             string `xml:"Status"`
}

type vpcSecurityGroupsXML struct {
	VpcSecurityGroup []vpcSecurityGroupXML `xml:"VpcSecurityGroup,omitempty"`
}

type clusterXML struct {
	ClusterIdentifier      string                     `xml:"ClusterIdentifier"`
	ClusterNamespaceArn    string                     `xml:"ClusterNamespaceArn"`
	ClusterStatus          string                     `xml:"ClusterStatus"`
	ClusterVersion         string                     `xml:"ClusterVersion,omitempty"`
	MasterUsername         string                     `xml:"MasterUsername,omitempty"`
	DBName                 string                     `xml:"DBName,omitempty"`
	Endpoint               *endpointXML               `xml:"Endpoint,omitempty"`
	ClusterCreateTime      string                     `xml:"ClusterCreateTime,omitempty"`
	ClusterSubnetGroupName string                     `xml:"ClusterSubnetGroupName,omitempty"`
	VpcSecurityGroups      *vpcSecurityGroupsXML      `xml:"VpcSecurityGroups,omitempty"`
	Tags                   *tagsXML                   `xml:"Tags,omitempty"`
	NodeType               string                     `xml:"NodeType,omitempty"`
	NumberOfNodes          int                        `xml:"NumberOfNodes,omitempty"`
	Encrypted              bool                       `xml:"Encrypted"`
	PubliclyAccessible     bool                       `xml:"PubliclyAccessible"`
	AvailabilityZone       string                     `xml:"AvailabilityZone,omitempty"`
	VpcID                  string                     `xml:"VpcId,omitempty"`
	ClusterParameterGroups *clusterParameterGroupsXML `xml:"ClusterParameterGroups,omitempty"`
	ClusterNodes           *clusterNodesXML           `xml:"ClusterNodes,omitempty"`
}

type clusterParameterGroupStatusXML struct {
	ParameterGroupName   string `xml:"ParameterGroupName"`
	ParameterApplyStatus string `xml:"ParameterApplyStatus"`
}

type clusterParameterGroupsXML struct {
	ClusterParameterGroup []clusterParameterGroupStatusXML `xml:"ClusterParameterGroup,omitempty"`
}

type clusterNodeXML struct {
	NodeRole         string `xml:"NodeRole"`
	PrivateIPAddress string `xml:"PrivateIPAddress"`
	PublicIPAddress  string `xml:"PublicIPAddress,omitempty"`
}

type clusterNodesXML struct {
	Member []clusterNodeXML `xml:"member,omitempty"`
}

type snapshotXML struct {
	SnapshotIdentifier         string   `xml:"SnapshotIdentifier"`
	SnapshotArn                string   `xml:"SnapshotArn"`
	ClusterIdentifier          string   `xml:"ClusterIdentifier"`
	ClusterVersion             string   `xml:"ClusterVersion,omitempty"`
	Status                     string   `xml:"Status"`
	SnapshotType               string   `xml:"SnapshotType,omitempty"`
	SnapshotCreateTime         string   `xml:"SnapshotCreateTime,omitempty"`
	NodeType                   string   `xml:"NodeType,omitempty"`
	NumberOfNodes              int      `xml:"NumberOfNodes,omitempty"`
	Encrypted                  bool     `xml:"Encrypted"`
	TotalBackupSizeInMegaBytes float64  `xml:"TotalBackupSizeInMegaBytes,omitempty"`
	Tags                       *tagsXML `xml:"Tags,omitempty"`
}

// Result wrappers — one per Action.

type clusterResult struct {
	Cluster clusterXML `xml:"Cluster"`
}

type clustersResult struct {
	Clusters clustersXML `xml:"Clusters"`
}

type clustersXML struct {
	Cluster []clusterXML `xml:"Cluster,omitempty"`
}

type snapshotResult struct {
	Snapshot snapshotXML `xml:"Snapshot"`
}

type snapshotsResult struct {
	Snapshots snapshotsXML `xml:"Snapshots"`
}

type snapshotsXML struct {
	Snapshot []snapshotXML `xml:"Snapshot,omitempty"`
}

type createClusterResponse struct {
	XMLName  xml.Name         `xml:"CreateClusterResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   clusterResult    `xml:"CreateClusterResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type describeClustersResponse struct {
	XMLName  xml.Name         `xml:"DescribeClustersResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   clustersResult   `xml:"DescribeClustersResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type modifyClusterResponse struct {
	XMLName  xml.Name         `xml:"ModifyClusterResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   clusterResult    `xml:"ModifyClusterResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type deleteClusterResponse struct {
	XMLName  xml.Name         `xml:"DeleteClusterResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   clusterResult    `xml:"DeleteClusterResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type rebootClusterResponse struct {
	XMLName  xml.Name         `xml:"RebootClusterResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   clusterResult    `xml:"RebootClusterResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type restoreFromClusterSnapshotResponse struct {
	XMLName  xml.Name         `xml:"RestoreFromClusterSnapshotResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   clusterResult    `xml:"RestoreFromClusterSnapshotResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type createClusterSnapshotResponse struct {
	XMLName  xml.Name         `xml:"CreateClusterSnapshotResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   snapshotResult   `xml:"CreateClusterSnapshotResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type describeClusterSnapshotsResponse struct {
	XMLName  xml.Name         `xml:"DescribeClusterSnapshotsResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   snapshotsResult  `xml:"DescribeClusterSnapshotsResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type deleteClusterSnapshotResponse struct {
	XMLName  xml.Name         `xml:"DeleteClusterSnapshotResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   snapshotResult   `xml:"DeleteClusterSnapshotResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type pauseClusterResponse struct {
	XMLName  xml.Name         `xml:"PauseClusterResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   clusterResult    `xml:"PauseClusterResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type resumeClusterResponse struct {
	XMLName  xml.Name         `xml:"ResumeClusterResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   clusterResult    `xml:"ResumeClusterResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type clusterCredentialsResult struct {
	DBUser     string `xml:"DbUser"`
	DBPassword string `xml:"DbPassword"`
	Expiration string `xml:"Expiration"`
}

type getClusterCredentialsResponse struct {
	XMLName  xml.Name                 `xml:"GetClusterCredentialsResponse"`
	Xmlns    string                   `xml:"xmlns,attr"`
	Result   clusterCredentialsResult `xml:"GetClusterCredentialsResult"`
	Metadata responseMetadata         `xml:"ResponseMetadata"`
}

// toClusterXML converts a driver Cluster to its XML representation.
func toClusterXML(cluster *rdbdriver.Cluster) clusterXML {
	return clusterXML{
		ClusterIdentifier:      cluster.ID,
		ClusterNamespaceArn:    cluster.ARN,
		ClusterStatus:          cluster.State,
		ClusterVersion:         cluster.EngineVersion,
		MasterUsername:         cluster.MasterUsername,
		DBName:                 cluster.DatabaseName,
		Endpoint:               &endpointXML{Address: cluster.Endpoint, Port: cluster.Port},
		ClusterCreateTime:      cluster.CreatedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
		ClusterSubnetGroupName: cluster.SubnetGroupName,
		VpcSecurityGroups:      toVpcSGsXML(cluster.VPCSecurityGroups),
		Tags:                   toTagsXML(cluster.Tags),
		NodeType:               cluster.NodeType,
		NumberOfNodes:          cluster.NumberOfNodes,
		Encrypted:              cluster.Encrypted,
		PubliclyAccessible:     cluster.PubliclyAccessible,
		AvailabilityZone:       cluster.AvailabilityZone,
		VpcID:                  cluster.VpcID,
		ClusterParameterGroups: toClusterParameterGroupsXML(cluster.DBClusterParameterGroupName),
		ClusterNodes:           toClusterNodesXML(cluster.NumberOfNodes),
	}
}

// leaderPrivateIP / computePrivateIPBase build the deterministic private IPs
// synthesized for a cluster's node list.
const (
	leaderPrivateIP      = "10.0.0.1"
	computePrivateIPBase = 10
	nodeRoleLeader       = "LEADER"
	nodeRoleCompute      = "COMPUTE"
	parameterApplyInSync = "in-sync"
)

func toClusterParameterGroupsXML(name string) *clusterParameterGroupsXML {
	if name == "" {
		return nil
	}

	return &clusterParameterGroupsXML{
		ClusterParameterGroup: []clusterParameterGroupStatusXML{{
			ParameterGroupName:   name,
			ParameterApplyStatus: parameterApplyInSync,
		}},
	}
}

// toClusterNodesXML synthesizes one LEADER node plus numberOfNodes COMPUTE
// nodes with deterministic private IPs, matching the ClusterNodes list real
// Redshift returns.
func toClusterNodesXML(numberOfNodes int) *clusterNodesXML {
	if numberOfNodes <= 0 {
		return nil
	}

	nodes := make([]clusterNodeXML, 0, numberOfNodes+1)
	nodes = append(nodes, clusterNodeXML{NodeRole: nodeRoleLeader, PrivateIPAddress: leaderPrivateIP})

	for i := 0; i < numberOfNodes; i++ {
		nodes = append(nodes, clusterNodeXML{
			NodeRole:         nodeRoleCompute,
			PrivateIPAddress: "10.0.0." + strconv.Itoa(computePrivateIPBase+i),
		})
	}

	return &clusterNodesXML{Member: nodes}
}

func toSnapshotXML(snap *rdbdriver.ClusterSnapshot) snapshotXML {
	return snapshotXML{
		SnapshotIdentifier:         snap.ID,
		SnapshotArn:                snap.ARN,
		ClusterIdentifier:          snap.ClusterID,
		ClusterVersion:             snap.EngineVersion,
		Status:                     snap.State,
		SnapshotType:               "manual",
		SnapshotCreateTime:         snap.CreatedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
		NodeType:                   snap.NodeType,
		NumberOfNodes:              snap.NumberOfNodes,
		Encrypted:                  snap.Encrypted,
		TotalBackupSizeInMegaBytes: snap.TotalBackupSizeInMegaBytes,
		Tags:                       toTagsXML(snap.Tags),
	}
}

func toTagsXML(tags map[string]string) *tagsXML {
	if len(tags) == 0 {
		return nil
	}

	out := &tagsXML{Tag: make([]tagXML, 0, len(tags))}
	for k, v := range tags {
		out.Tag = append(out.Tag, tagXML{Key: k, Value: v})
	}

	return out
}

func toVpcSGsXML(sgs []string) *vpcSecurityGroupsXML {
	if len(sgs) == 0 {
		return nil
	}

	out := &vpcSecurityGroupsXML{
		VpcSecurityGroup: make([]vpcSecurityGroupXML, 0, len(sgs)),
	}
	for _, sg := range sgs {
		out.VpcSecurityGroup = append(out.VpcSecurityGroup, vpcSecurityGroupXML{
			VpcSecurityGroupID: sg,
			Status:             "active",
		})
	}

	return out
}

// formInt returns the integer value of a form field, or 0 on missing/parse error.
func formInt(v string) int {
	if v == "" {
		return 0
	}

	n, err := strconv.Atoi(v)
	if err != nil {
		return 0
	}

	return n
}

// formBool returns the boolean value of a form field, or false on missing/parse error.
func formBool(v string) bool {
	if v == "" {
		return false
	}

	b, err := strconv.ParseBool(v)
	if err != nil {
		return false
	}

	return b
}
