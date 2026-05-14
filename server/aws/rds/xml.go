package rds

import (
	"encoding/xml"
	"strconv"

	rdsdriver "github.com/stackshy/cloudemu/relationaldb/driver"
)

// All RDS query-protocol responses are wrapped in <FooResponse> with a
// <FooResult> child and a trailing <ResponseMetadata>. The structures below
// mirror the AWS-published XML closely enough that aws-sdk-go-v2's RDS
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

type tagListXML struct {
	Tag []tagXML `xml:"Tag,omitempty"`
}

type vpcSecurityGroupXML struct {
	VpcSecurityGroupID string `xml:"VpcSecurityGroupId"`
	Status             string `xml:"Status"`
}

type vpcSecurityGroupsXML struct {
	VpcSecurityGroupMembership []vpcSecurityGroupXML `xml:"VpcSecurityGroupMembership,omitempty"`
}

type dbInstanceXML struct {
	DBInstanceIdentifier string                `xml:"DBInstanceIdentifier"`
	DBInstanceArn        string                `xml:"DBInstanceArn"`
	Engine               string                `xml:"Engine,omitempty"`
	EngineVersion        string                `xml:"EngineVersion,omitempty"`
	DBInstanceClass      string                `xml:"DBInstanceClass,omitempty"`
	DBInstanceStatus     string                `xml:"DBInstanceStatus"`
	MasterUsername       string                `xml:"MasterUsername,omitempty"`
	DBName               string                `xml:"DBName,omitempty"`
	AllocatedStorage     int                   `xml:"AllocatedStorage,omitempty"`
	StorageType          string                `xml:"StorageType,omitempty"`
	Endpoint             *endpointXML          `xml:"Endpoint,omitempty"`
	MultiAZ              bool                  `xml:"MultiAZ"`
	PubliclyAccessible   bool                  `xml:"PubliclyAccessible"`
	AvailabilityZone     string                `xml:"AvailabilityZone,omitempty"`
	DBClusterIdentifier  string                `xml:"DBClusterIdentifier,omitempty"`
	DBSubnetGroupName    string                `xml:"DBSubnetGroupName,omitempty"`
	InstanceCreateTime   string                `xml:"InstanceCreateTime,omitempty"`
	VpcSecurityGroups    *vpcSecurityGroupsXML `xml:"VpcSecurityGroups,omitempty"`
	TagList              *tagListXML           `xml:"TagList,omitempty"`
}

type dbClusterMemberXML struct {
	DBInstanceIdentifier string `xml:"DBInstanceIdentifier"`
	IsClusterWriter      bool   `xml:"IsClusterWriter"`
}

type dbClusterMembersXML struct {
	DBClusterMember []dbClusterMemberXML `xml:"DBClusterMember,omitempty"`
}

type dbClusterXML struct {
	DBClusterIdentifier string                `xml:"DBClusterIdentifier"`
	DBClusterArn        string                `xml:"DBClusterArn"`
	Engine              string                `xml:"Engine,omitempty"`
	EngineVersion       string                `xml:"EngineVersion,omitempty"`
	Status              string                `xml:"Status"`
	MasterUsername      string                `xml:"MasterUsername,omitempty"`
	DatabaseName        string                `xml:"DatabaseName,omitempty"`
	Endpoint            string                `xml:"Endpoint,omitempty"`
	ReaderEndpoint      string                `xml:"ReaderEndpoint,omitempty"`
	Port                int                   `xml:"Port,omitempty"`
	DBSubnetGroup       string                `xml:"DBSubnetGroup,omitempty"`
	ClusterCreateTime   string                `xml:"ClusterCreateTime,omitempty"`
	DBClusterMembers    *dbClusterMembersXML  `xml:"DBClusterMembers,omitempty"`
	VpcSecurityGroups   *vpcSecurityGroupsXML `xml:"VpcSecurityGroups,omitempty"`
	TagList             *tagListXML           `xml:"TagList,omitempty"`
}

type dbSnapshotXML struct {
	DBSnapshotIdentifier string      `xml:"DBSnapshotIdentifier"`
	DBSnapshotArn        string      `xml:"DBSnapshotArn"`
	DBInstanceIdentifier string      `xml:"DBInstanceIdentifier"`
	Engine               string      `xml:"Engine,omitempty"`
	EngineVersion        string      `xml:"EngineVersion,omitempty"`
	AllocatedStorage     int         `xml:"AllocatedStorage,omitempty"`
	Status               string      `xml:"Status"`
	SnapshotCreateTime   string      `xml:"SnapshotCreateTime,omitempty"`
	TagList              *tagListXML `xml:"TagList,omitempty"`
}

type dbClusterSnapshotXML struct {
	DBClusterSnapshotIdentifier string      `xml:"DBClusterSnapshotIdentifier"`
	DBClusterSnapshotArn        string      `xml:"DBClusterSnapshotArn"`
	DBClusterIdentifier         string      `xml:"DBClusterIdentifier"`
	Engine                      string      `xml:"Engine,omitempty"`
	EngineVersion               string      `xml:"EngineVersion,omitempty"`
	Status                      string      `xml:"Status"`
	SnapshotCreateTime          string      `xml:"SnapshotCreateTime,omitempty"`
	TagList                     *tagListXML `xml:"TagList,omitempty"`
}

// Result wrappers — one per Action. Action-name + "Response" is the outer
// envelope; Action-name + "Result" is the payload child.

type createDBInstanceResponse struct {
	XMLName  xml.Name         `xml:"CreateDBInstanceResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   dbInstanceResult `xml:"CreateDBInstanceResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type modifyDBInstanceResponse struct {
	XMLName  xml.Name         `xml:"ModifyDBInstanceResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   dbInstanceResult `xml:"ModifyDBInstanceResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type deleteDBInstanceResponse struct {
	XMLName  xml.Name         `xml:"DeleteDBInstanceResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   dbInstanceResult `xml:"DeleteDBInstanceResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type startDBInstanceResponse struct {
	XMLName  xml.Name         `xml:"StartDBInstanceResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   dbInstanceResult `xml:"StartDBInstanceResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type stopDBInstanceResponse struct {
	XMLName  xml.Name         `xml:"StopDBInstanceResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   dbInstanceResult `xml:"StopDBInstanceResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type rebootDBInstanceResponse struct {
	XMLName  xml.Name         `xml:"RebootDBInstanceResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   dbInstanceResult `xml:"RebootDBInstanceResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type restoreDBInstanceFromDBSnapshotResponse struct {
	XMLName  xml.Name         `xml:"RestoreDBInstanceFromDBSnapshotResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   dbInstanceResult `xml:"RestoreDBInstanceFromDBSnapshotResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type dbInstanceResult struct {
	DBInstance dbInstanceXML `xml:"DBInstance"`
}

type dbInstancesResult struct {
	DBInstances dbInstancesXML `xml:"DBInstances"`
}

type dbInstancesXML struct {
	DBInstance []dbInstanceXML `xml:"DBInstance,omitempty"`
}

type describeDBInstancesResponse struct {
	XMLName  xml.Name          `xml:"DescribeDBInstancesResponse"`
	Xmlns    string            `xml:"xmlns,attr"`
	Result   dbInstancesResult `xml:"DescribeDBInstancesResult"`
	Metadata responseMetadata  `xml:"ResponseMetadata"`
}

type createDBClusterResponse struct {
	XMLName  xml.Name         `xml:"CreateDBClusterResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   dbClusterResult  `xml:"CreateDBClusterResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type modifyDBClusterResponse struct {
	XMLName  xml.Name         `xml:"ModifyDBClusterResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   dbClusterResult  `xml:"ModifyDBClusterResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type deleteDBClusterResponse struct {
	XMLName  xml.Name         `xml:"DeleteDBClusterResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   dbClusterResult  `xml:"DeleteDBClusterResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type startDBClusterResponse struct {
	XMLName  xml.Name         `xml:"StartDBClusterResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   dbClusterResult  `xml:"StartDBClusterResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type stopDBClusterResponse struct {
	XMLName  xml.Name         `xml:"StopDBClusterResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   dbClusterResult  `xml:"StopDBClusterResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type restoreDBClusterFromSnapshotResponse struct {
	XMLName  xml.Name         `xml:"RestoreDBClusterFromSnapshotResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   dbClusterResult  `xml:"RestoreDBClusterFromSnapshotResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type dbClusterResult struct {
	DBCluster dbClusterXML `xml:"DBCluster"`
}

type dbClustersResult struct {
	DBClusters dbClustersXML `xml:"DBClusters"`
}

type dbClustersXML struct {
	DBCluster []dbClusterXML `xml:"DBCluster,omitempty"`
}

type describeDBClustersResponse struct {
	XMLName  xml.Name         `xml:"DescribeDBClustersResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   dbClustersResult `xml:"DescribeDBClustersResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type createDBSnapshotResponse struct {
	XMLName  xml.Name         `xml:"CreateDBSnapshotResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   dbSnapshotResult `xml:"CreateDBSnapshotResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type deleteDBSnapshotResponse struct {
	XMLName  xml.Name         `xml:"DeleteDBSnapshotResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   dbSnapshotResult `xml:"DeleteDBSnapshotResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type dbSnapshotResult struct {
	DBSnapshot dbSnapshotXML `xml:"DBSnapshot"`
}

type dbSnapshotsResult struct {
	DBSnapshots dbSnapshotsXML `xml:"DBSnapshots"`
}

type dbSnapshotsXML struct {
	DBSnapshot []dbSnapshotXML `xml:"DBSnapshot,omitempty"`
}

type describeDBSnapshotsResponse struct {
	XMLName  xml.Name          `xml:"DescribeDBSnapshotsResponse"`
	Xmlns    string            `xml:"xmlns,attr"`
	Result   dbSnapshotsResult `xml:"DescribeDBSnapshotsResult"`
	Metadata responseMetadata  `xml:"ResponseMetadata"`
}

type createDBClusterSnapshotResponse struct {
	XMLName  xml.Name                `xml:"CreateDBClusterSnapshotResponse"`
	Xmlns    string                  `xml:"xmlns,attr"`
	Result   dbClusterSnapshotResult `xml:"CreateDBClusterSnapshotResult"`
	Metadata responseMetadata        `xml:"ResponseMetadata"`
}

type deleteDBClusterSnapshotResponse struct {
	XMLName  xml.Name                `xml:"DeleteDBClusterSnapshotResponse"`
	Xmlns    string                  `xml:"xmlns,attr"`
	Result   dbClusterSnapshotResult `xml:"DeleteDBClusterSnapshotResult"`
	Metadata responseMetadata        `xml:"ResponseMetadata"`
}

type dbClusterSnapshotResult struct {
	DBClusterSnapshot dbClusterSnapshotXML `xml:"DBClusterSnapshot"`
}

type dbClusterSnapshotsResult struct {
	DBClusterSnapshots dbClusterSnapshotsXML `xml:"DBClusterSnapshots"`
}

type dbClusterSnapshotsXML struct {
	DBClusterSnapshot []dbClusterSnapshotXML `xml:"DBClusterSnapshot,omitempty"`
}

type describeDBClusterSnapshotsResponse struct {
	XMLName  xml.Name                 `xml:"DescribeDBClusterSnapshotsResponse"`
	Xmlns    string                   `xml:"xmlns,attr"`
	Result   dbClusterSnapshotsResult `xml:"DescribeDBClusterSnapshotsResult"`
	Metadata responseMetadata         `xml:"ResponseMetadata"`
}

// toInstanceXML converts a driver Instance to its XML representation.
func toInstanceXML(inst *rdsdriver.Instance) dbInstanceXML {
	return dbInstanceXML{
		DBInstanceIdentifier: inst.ID,
		DBInstanceArn:        inst.ARN,
		Engine:               inst.Engine,
		EngineVersion:        inst.EngineVersion,
		DBInstanceClass:      inst.InstanceClass,
		DBInstanceStatus:     inst.State,
		MasterUsername:       inst.MasterUsername,
		DBName:               inst.DBName,
		AllocatedStorage:     inst.AllocatedStorage,
		StorageType:          inst.StorageType,
		Endpoint: &endpointXML{
			Address: inst.Endpoint,
			Port:    inst.Port,
		},
		MultiAZ:             inst.MultiAZ,
		PubliclyAccessible:  inst.PubliclyAccessible,
		AvailabilityZone:    inst.AvailabilityZone,
		DBClusterIdentifier: inst.ClusterID,
		DBSubnetGroupName:   inst.SubnetGroupName,
		InstanceCreateTime:  inst.CreatedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
		VpcSecurityGroups:   toVpcSGsXML(inst.VPCSecurityGroups),
		TagList:             toTagListXML(inst.Tags),
	}
}

func toClusterXML(cluster *rdsdriver.Cluster) dbClusterXML {
	members := dbClusterMembersXML{
		DBClusterMember: make([]dbClusterMemberXML, 0, len(cluster.Members)),
	}

	for i, m := range cluster.Members {
		members.DBClusterMember = append(members.DBClusterMember, dbClusterMemberXML{
			DBInstanceIdentifier: m,
			IsClusterWriter:      i == 0,
		})
	}

	return dbClusterXML{
		DBClusterIdentifier: cluster.ID,
		DBClusterArn:        cluster.ARN,
		Engine:              cluster.Engine,
		EngineVersion:       cluster.EngineVersion,
		Status:              cluster.State,
		MasterUsername:      cluster.MasterUsername,
		DatabaseName:        cluster.DatabaseName,
		Endpoint:            cluster.Endpoint,
		ReaderEndpoint:      cluster.ReaderEndpoint,
		Port:                cluster.Port,
		DBSubnetGroup:       cluster.SubnetGroupName,
		ClusterCreateTime:   cluster.CreatedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
		DBClusterMembers:    &members,
		VpcSecurityGroups:   toVpcSGsXML(cluster.VPCSecurityGroups),
		TagList:             toTagListXML(cluster.Tags),
	}
}

func toSnapshotXML(snap *rdsdriver.Snapshot) dbSnapshotXML {
	return dbSnapshotXML{
		DBSnapshotIdentifier: snap.ID,
		DBSnapshotArn:        snap.ARN,
		DBInstanceIdentifier: snap.InstanceID,
		Engine:               snap.Engine,
		EngineVersion:        snap.EngineVersion,
		AllocatedStorage:     snap.AllocatedStorage,
		Status:               snap.State,
		SnapshotCreateTime:   snap.CreatedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
		TagList:              toTagListXML(snap.Tags),
	}
}

func toClusterSnapshotXML(snap *rdsdriver.ClusterSnapshot) dbClusterSnapshotXML {
	return dbClusterSnapshotXML{
		DBClusterSnapshotIdentifier: snap.ID,
		DBClusterSnapshotArn:        snap.ARN,
		DBClusterIdentifier:         snap.ClusterID,
		Engine:                      snap.Engine,
		EngineVersion:               snap.EngineVersion,
		Status:                      snap.State,
		SnapshotCreateTime:          snap.CreatedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
		TagList:                     toTagListXML(snap.Tags),
	}
}

func toTagListXML(tags map[string]string) *tagListXML {
	if len(tags) == 0 {
		return nil
	}

	out := &tagListXML{Tag: make([]tagXML, 0, len(tags))}
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
		VpcSecurityGroupMembership: make([]vpcSecurityGroupXML, 0, len(sgs)),
	}
	for _, sg := range sgs {
		out.VpcSecurityGroupMembership = append(out.VpcSecurityGroupMembership, vpcSecurityGroupXML{
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
