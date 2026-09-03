package rds

import (
	"encoding/xml"
	"sort"
	"strconv"

	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

// snapshotProgressComplete is the PercentProgress an available snapshot reports.
const snapshotProgressComplete = 100

// rdsHostedZoneID is the Route 53 hosted-zone id RDS reports for a DB
// instance endpoint. Real AWS uses a per-region constant; this stand-in keeps
// the Endpoint.HostedZoneId element populated for SDK consumers.
const rdsHostedZoneID = "Z2R2ITUGPM61AM"

// parameterApplyInSync / optionStatusInSync are the statuses RDS reports for a
// DB parameter group / option group membership that is fully applied.
const (
	parameterApplyInSync = "in-sync"
	optionStatusInSync   = "in-sync"
)

// All RDS query-protocol responses are wrapped in <FooResponse> with a
// <FooResult> child and a trailing <ResponseMetadata>. The structures below
// mirror the AWS-published XML closely enough that aws-sdk-go-v2's RDS
// unmarshalers consume them without complaint.

type responseMetadata struct {
	RequestID string `xml:"RequestId"`
}

type endpointXML struct {
	Address      string `xml:"Address,omitempty"`
	Port         int    `xml:"Port,omitempty"`
	HostedZoneID string `xml:"HostedZoneId,omitempty"`
}

type dbParameterGroupMembershipXML struct {
	DBParameterGroupName string `xml:"DBParameterGroupName"`
	ParameterApplyStatus string `xml:"ParameterApplyStatus"`
}

type dbParameterGroupsXML struct {
	DBParameterGroup []dbParameterGroupMembershipXML `xml:"DBParameterGroup,omitempty"`
}

type optionGroupMembershipXML struct {
	OptionGroupName string `xml:"OptionGroupName"`
	Status          string `xml:"Status"`
}

type optionGroupMembershipsXML struct {
	OptionGroupMembership []optionGroupMembershipXML `xml:"OptionGroupMembership,omitempty"`
}

type availabilityZonesXML struct {
	AvailabilityZone []string `xml:"AvailabilityZone,omitempty"`
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
	DBInstanceIdentifier                  string                     `xml:"DBInstanceIdentifier"`
	DBInstanceArn                         string                     `xml:"DBInstanceArn"`
	Engine                                string                     `xml:"Engine,omitempty"`
	EngineVersion                         string                     `xml:"EngineVersion,omitempty"`
	DBInstanceClass                       string                     `xml:"DBInstanceClass,omitempty"`
	DBInstanceStatus                      string                     `xml:"DBInstanceStatus"`
	MasterUsername                        string                     `xml:"MasterUsername,omitempty"`
	DBName                                string                     `xml:"DBName,omitempty"`
	AllocatedStorage                      int                        `xml:"AllocatedStorage,omitempty"`
	StorageType                           string                     `xml:"StorageType,omitempty"`
	Endpoint                              *endpointXML               `xml:"Endpoint,omitempty"`
	MultiAZ                               bool                       `xml:"MultiAZ"`
	PubliclyAccessible                    bool                       `xml:"PubliclyAccessible"`
	AvailabilityZone                      string                     `xml:"AvailabilityZone,omitempty"`
	DBClusterIdentifier                   string                     `xml:"DBClusterIdentifier,omitempty"`
	DBSubnetGroup                         *dbSubnetGroupXML          `xml:"DBSubnetGroup,omitempty"`
	InstanceCreateTime                    string                     `xml:"InstanceCreateTime,omitempty"`
	DbiResourceID                         string                     `xml:"DbiResourceId,omitempty"`
	BackupRetentionPeriod                 int                        `xml:"BackupRetentionPeriod,omitempty"`
	PreferredBackupWindow                 string                     `xml:"PreferredBackupWindow,omitempty"`
	PreferredMaintenanceWindow            string                     `xml:"PreferredMaintenanceWindow,omitempty"`
	CACertificateIdentifier               string                     `xml:"CACertificateIdentifier,omitempty"`
	Iops                                  int                        `xml:"Iops,omitempty"`
	StorageEncrypted                      bool                       `xml:"StorageEncrypted"`
	KmsKeyID                              string                     `xml:"KmsKeyId,omitempty"`
	DeletionProtection                    bool                       `xml:"DeletionProtection"`
	DBParameterGroups                     *dbParameterGroupsXML      `xml:"DBParameterGroups,omitempty"`
	OptionGroupMemberships                *optionGroupMembershipsXML `xml:"OptionGroupMemberships,omitempty"`
	VpcSecurityGroups                     *vpcSecurityGroupsXML      `xml:"VpcSecurityGroups,omitempty"`
	TagList                               *tagListXML                `xml:"TagList,omitempty"`
	ReadReplicaSourceDBInstanceIdentifier string                     `xml:"ReadReplicaSourceDBInstanceIdentifier,omitempty"`
	ReadReplicaDBInstanceIdentifiers      *readReplicaIDsXML         `xml:"ReadReplicaDBInstanceIdentifiers,omitempty"`
	PendingModifiedValues                 *pendingModifiedValuesXML  `xml:"PendingModifiedValues,omitempty"`
}

// pendingModifiedValuesXML is the nested DBInstance element listing the
// deferrable ModifyDBInstance changes requested with ApplyImmediately=false but
// not yet applied. MasterUserPassword is echoed masked ("****"), never in clear.
type pendingModifiedValuesXML struct {
	DBInstanceClass       string `xml:"DBInstanceClass,omitempty"`
	AllocatedStorage      int    `xml:"AllocatedStorage,omitempty"`
	EngineVersion         string `xml:"EngineVersion,omitempty"`
	MasterUserPassword    string `xml:"MasterUserPassword,omitempty"`
	BackupRetentionPeriod int    `xml:"BackupRetentionPeriod,omitempty"`
	MultiAZ               *bool  `xml:"MultiAZ,omitempty"`
	Iops                  int    `xml:"Iops,omitempty"`
	StorageType           string `xml:"StorageType,omitempty"`
}

type readReplicaIDsXML struct {
	ReadReplicaDBInstanceIdentifier []string `xml:"ReadReplicaDBInstanceIdentifier"`
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
	EngineMode          string                `xml:"EngineMode,omitempty"`
	DBClusterResourceID string                `xml:"DbClusterResourceId,omitempty"`
	AllocatedStorage    int                   `xml:"AllocatedStorage,omitempty"`
	StorageEncrypted    bool                  `xml:"StorageEncrypted"`
	KmsKeyID            string                `xml:"KmsKeyId,omitempty"`
	DeletionProtection  bool                  `xml:"DeletionProtection"`
	AvailabilityZones   *availabilityZonesXML `xml:"AvailabilityZones,omitempty"`
	ClusterCreateTime   string                `xml:"ClusterCreateTime,omitempty"`
	DBClusterMembers    *dbClusterMembersXML  `xml:"DBClusterMembers,omitempty"`
	VpcSecurityGroups   *vpcSecurityGroupsXML `xml:"VpcSecurityGroups,omitempty"`
	AssociatedRoles     *associatedRolesXML   `xml:"AssociatedRoles,omitempty"`
	TagList             *tagListXML           `xml:"TagList,omitempty"`
}

type dbClusterRoleXML struct {
	RoleArn     string `xml:"RoleArn"`
	FeatureName string `xml:"FeatureName,omitempty"`
	Status      string `xml:"Status,omitempty"`
}

type associatedRolesXML struct {
	DBClusterRole []dbClusterRoleXML `xml:"DBClusterRole"`
}

type dbSnapshotXML struct {
	DBSnapshotIdentifier string      `xml:"DBSnapshotIdentifier"`
	DBSnapshotArn        string      `xml:"DBSnapshotArn"`
	DBInstanceIdentifier string      `xml:"DBInstanceIdentifier"`
	Engine               string      `xml:"Engine,omitempty"`
	EngineVersion        string      `xml:"EngineVersion,omitempty"`
	AllocatedStorage     int         `xml:"AllocatedStorage,omitempty"`
	Status               string      `xml:"Status"`
	PercentProgress      int         `xml:"PercentProgress"`
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
	PercentProgress             int         `xml:"PercentProgress"`
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
	Marker      string         `xml:"Marker,omitempty"`
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
	Marker     string        `xml:"Marker,omitempty"`
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
	Marker      string         `xml:"Marker,omitempty"`
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
//
// resolvedSubnetGroup, when non-nil, is the fully resolved DB subnet group the
// instance is placed in. Real RDS reports the association as a nested complex
// <DBSubnetGroup> element (name, description, VpcId, status, Subnets) — not a
// scalar <DBSubnetGroupName> — so the aws-sdk-go-v2 DBInstance.DBSubnetGroup
// field (and Terraform's db_subnet_group_name off it) has something to bind.
func toInstanceXML(inst *rdsdriver.Instance, resolvedSubnetGroup *dbSubnetGroupXML) dbInstanceXML {
	x := dbInstanceXML{
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
			Address:      inst.Endpoint,
			Port:         inst.Port,
			HostedZoneID: rdsHostedZoneID,
		},
		MultiAZ:                               inst.MultiAZ,
		PubliclyAccessible:                    inst.PubliclyAccessible,
		AvailabilityZone:                      inst.AvailabilityZone,
		DBClusterIdentifier:                   inst.ClusterID,
		DBSubnetGroup:                         resolvedSubnetGroup,
		InstanceCreateTime:                    inst.CreatedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
		DbiResourceID:                         inst.DbiResourceID,
		BackupRetentionPeriod:                 inst.BackupRetentionPeriod,
		PreferredBackupWindow:                 inst.PreferredBackupWindow,
		PreferredMaintenanceWindow:            inst.PreferredMaintenanceWindow,
		CACertificateIdentifier:               inst.CACertificateIdentifier,
		Iops:                                  inst.Iops,
		StorageEncrypted:                      inst.StorageEncrypted,
		KmsKeyID:                              inst.KmsKeyID,
		DeletionProtection:                    inst.DeletionProtection,
		DBParameterGroups:                     toDBParameterGroupsXML(inst.DBParameterGroupName),
		OptionGroupMemberships:                toOptionGroupMembershipsXML(inst.OptionGroupName),
		VpcSecurityGroups:                     toVpcSGsXML(inst.VPCSecurityGroups),
		TagList:                               toTagListXML(inst.Tags),
		ReadReplicaSourceDBInstanceIdentifier: inst.ReadReplicaSource,
		ReadReplicaDBInstanceIdentifiers:      toReadReplicaIDsXML(inst.ReadReplicaTargets),
		PendingModifiedValues:                 toPendingModifiedValuesXML(inst.PendingModifiedValues),
	}

	return x
}

// toPendingModifiedValuesXML maps the driver's PendingModifiedValues onto its
// nested wire element, returning nil (an omitted element) when nothing is pending.
func toPendingModifiedValuesXML(p *rdsdriver.PendingModifiedValues) *pendingModifiedValuesXML {
	if p == nil {
		return nil
	}

	return &pendingModifiedValuesXML{
		DBInstanceClass:       p.InstanceClass,
		AllocatedStorage:      p.AllocatedStorage,
		EngineVersion:         p.EngineVersion,
		MasterUserPassword:    p.MasterUserPassword,
		BackupRetentionPeriod: p.BackupRetentionPeriod,
		MultiAZ:               p.MultiAZ,
		Iops:                  p.Iops,
		StorageType:           p.StorageType,
	}
}

func toDBParameterGroupsXML(name string) *dbParameterGroupsXML {
	if name == "" {
		return nil
	}

	return &dbParameterGroupsXML{
		DBParameterGroup: []dbParameterGroupMembershipXML{{
			DBParameterGroupName: name,
			ParameterApplyStatus: parameterApplyInSync,
		}},
	}
}

func toOptionGroupMembershipsXML(name string) *optionGroupMembershipsXML {
	if name == "" {
		return nil
	}

	return &optionGroupMembershipsXML{
		OptionGroupMembership: []optionGroupMembershipXML{{
			OptionGroupName: name,
			Status:          optionStatusInSync,
		}},
	}
}

func toReadReplicaIDsXML(ids []string) *readReplicaIDsXML {
	if len(ids) == 0 {
		return nil
	}

	return &readReplicaIDsXML{ReadReplicaDBInstanceIdentifier: ids}
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
		EngineMode:          cluster.EngineMode,
		DBClusterResourceID: cluster.DBClusterResourceID,
		AllocatedStorage:    cluster.AllocatedStorage,
		StorageEncrypted:    cluster.StorageEncrypted,
		KmsKeyID:            cluster.KmsKeyID,
		DeletionProtection:  cluster.DeletionProtection,
		AvailabilityZones:   toAvailabilityZonesXML(cluster.AvailabilityZones),
		ClusterCreateTime:   cluster.CreatedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
		DBClusterMembers:    &members,
		VpcSecurityGroups:   toVpcSGsXML(cluster.VPCSecurityGroups),
		AssociatedRoles:     toAssociatedRolesXML(cluster.AssociatedRoles),
		TagList:             toTagListXML(cluster.Tags),
	}
}

func toAssociatedRolesXML(roles []rdsdriver.DBClusterRole) *associatedRolesXML {
	if len(roles) == 0 {
		return nil
	}

	out := &associatedRolesXML{DBClusterRole: make([]dbClusterRoleXML, 0, len(roles))}
	for i := range roles {
		out.DBClusterRole = append(out.DBClusterRole, dbClusterRoleXML{
			RoleArn:     roles[i].RoleARN,
			FeatureName: roles[i].FeatureName,
			Status:      roles[i].Status,
		})
	}

	return out
}

func toAvailabilityZonesXML(azs []string) *availabilityZonesXML {
	if len(azs) == 0 {
		return nil
	}

	return &availabilityZonesXML{AvailabilityZone: azs}
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
		PercentProgress:      percentProgressForState(snap.State),
		SnapshotCreateTime:   snap.CreatedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
		TagList:              toTagListXML(snap.Tags),
	}
}

// percentProgressForState reports a snapshot's backup progress: an available
// snapshot is fully backed up (100), anything still creating is 0. Real AWS
// reports 100 for an available snapshot, so a caller polling PercentProgress
// alongside Status sees them agree.
func percentProgressForState(state string) int {
	if state == rdsdriver.SnapshotAvailable {
		return snapshotProgressComplete
	}

	return 0
}

func toClusterSnapshotXML(snap *rdsdriver.ClusterSnapshot) dbClusterSnapshotXML {
	return dbClusterSnapshotXML{
		DBClusterSnapshotIdentifier: snap.ID,
		DBClusterSnapshotArn:        snap.ARN,
		DBClusterIdentifier:         snap.ClusterID,
		Engine:                      snap.Engine,
		EngineVersion:               snap.EngineVersion,
		Status:                      snap.State,
		PercentProgress:             percentProgressForState(snap.State),
		SnapshotCreateTime:          snap.CreatedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
		TagList:                     toTagListXML(snap.Tags),
	}
}

func toTagListXML(tags map[string]string) *tagListXML {
	if len(tags) == 0 {
		return nil
	}

	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	out := &tagListXML{Tag: make([]tagXML, 0, len(tags))}
	for _, k := range keys {
		out.Tag = append(out.Tag, tagXML{Key: k, Value: tags[k]})
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
