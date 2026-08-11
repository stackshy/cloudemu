// Package driver defines the interface and domain types for Amazon MSK (Managed
// Streaming for Apache Kafka) implementations. It models provisioned and
// serverless clusters, broker node groups, configurations and their revisions,
// VPC connections, topics, cluster operations, replicators, and broker nodes.
//
// Types are plain Go (time.Time, maps, slices, nested structs). Rich
// configuration blocks the emulator does not interpret (encryption,
// client-authentication, logging, open-monitoring, connectivity, serverless
// VPC configs, etc.) are carried verbatim as json.RawMessage so a
// round-tripped cluster reflects everything the caller sent without the driver
// modeling every nested shape.
package driver

import (
	"encoding/json"
	"time"
)

// Cluster states reported via the ClusterInfo.State field.
//
// LIMITATION (synchronous lifecycle): the emulator provisions a cluster
// immediately ACTIVE and applies every mutation synchronously, so a cluster is
// only ever ACTIVE (or DELETING, briefly, on delete). It never passes through
// the transient CREATING / UPDATING states, and delete removes the cluster
// rather than leaving it DELETING. Consequently real MSK's rule "reject a
// mutation while the cluster is CREATING / UPDATING / DELETING" is not
// reproduced — back-to-back updates all succeed here. CREATING/UPDATING/FAILED
// are retained to document the real MSK state enum.
const (
	ClusterStateActive   = "ACTIVE"
	ClusterStateCreating = "CREATING"
	ClusterStateDeleting = "DELETING"
	ClusterStateFailed   = "FAILED"
	ClusterStateUpdating = "UPDATING"
)

// Cluster types.
const (
	ClusterTypeProvisioned = "PROVISIONED"
	ClusterTypeServerless  = "SERVERLESS"
)

// Configuration states.
const (
	ConfigurationStateActive   = "ACTIVE"
	ConfigurationStateDeleting = "DELETING"
)

// BrokerNodeGroupInfo is the modeled broker placement of a provisioned cluster.
// Unmodeled blocks (ConnectivityInfo, StorageInfo) survive via RawFields.
type BrokerNodeGroupInfo struct {
	ClientSubnets        []string
	InstanceType         string
	BrokerAZDistribution string
	SecurityGroups       []string
	ZoneIDs              []string
	// RawFields carries broker-group blocks the emulator does not model, keyed
	// by their JSON field name (e.g. "storageInfo", "connectivityInfo").
	RawFields map[string]json.RawMessage
}

// Cluster is the full description returned by DescribeCluster / ListClusters.
// A provisioned cluster carries BrokerNodeGroupInfo; a serverless cluster
// carries its config only through RawOptions. ClusterType distinguishes them.
type Cluster struct {
	ClusterARN          string
	ClusterName         string
	ClusterType         string
	State               string
	CurrentVersion      string
	KafkaVersion        string
	NumberOfBrokerNodes int32
	BrokerNodeGroupInfo *BrokerNodeGroupInfo
	StorageMode         string
	EnhancedMonitoring  string
	Tags                map[string]string
	CreationTime        time.Time
	// RawOptions carries top-level cluster blocks the emulator does not model
	// (encryptionInfo, clientAuthentication, loggingInfo, openMonitoring,
	// serverless, provisioned, etc.), so Describe reflects what Create received.
	RawOptions map[string]json.RawMessage
}

// CreateClusterInput describes a provisioned cluster to create (v1).
type CreateClusterInput struct {
	ClusterName         string
	KafkaVersion        string
	NumberOfBrokerNodes int32
	BrokerNodeGroupInfo *BrokerNodeGroupInfo
	StorageMode         string
	EnhancedMonitoring  string
	Tags                map[string]string
	RawOptions          map[string]json.RawMessage
}

// CreateClusterV2Input describes a v2 cluster create, which can be either
// provisioned or serverless. Exactly one of Provisioned/Serverless is set.
type CreateClusterV2Input struct {
	ClusterName string
	Tags        map[string]string
	// Provisioned carries the provisioned-cluster block when the caller creates
	// a PROVISIONED cluster; Serverless carries the serverless block otherwise.
	Provisioned *CreateClusterInput
	Serverless  json.RawMessage
	// RawOptions carries the full v2 request body's unmodeled top-level blocks.
	RawOptions map[string]json.RawMessage
}

// ConfigurationRevision is one revision of an MSK configuration.
type ConfigurationRevision struct {
	Revision         int64
	Description      string
	CreationTime     time.Time
	ServerProperties []byte
}

// Configuration is an MSK configuration and its revision history.
type Configuration struct {
	ARN            string
	Name           string
	Description    string
	State          string
	KafkaVersions  []string
	CreationTime   time.Time
	LatestRevision ConfigurationRevision
	Revisions      []ConfigurationRevision
	// Tags are stored server-side for TagResource/ListTagsForResource. The MSK
	// Configuration wire shape does not carry tags inline, so DescribeConfiguration
	// does not render them.
	Tags map[string]string
}

// CreateConfigurationInput describes a configuration to create (revision 1).
type CreateConfigurationInput struct {
	Name             string
	Description      string
	KafkaVersions    []string
	ServerProperties []byte
}

// VpcConnection is an MSK VPC connection.
type VpcConnection struct {
	VpcConnectionARN string
	TargetClusterARN string
	State            string
	Authentication   string
	VpcID            string
	CreationTime     time.Time
	Tags             map[string]string
	RawOptions       map[string]json.RawMessage
}

// Topic is a Kafka topic within a cluster (MSK topic-management API).
type Topic struct {
	TopicName          string
	NumberOfPartitions int32
	ReplicationFactor  int32
	RawOptions         map[string]json.RawMessage
}

// ClusterOperation is a record of an in-flight or completed cluster mutation.
type ClusterOperation struct {
	OperationARN   string
	ClusterARN     string
	OperationType  string
	OperationState string
	CreationTime   time.Time
	RawOptions     map[string]json.RawMessage
}

// Replicator is an MSK Replicator (cross-cluster replication).
type Replicator struct {
	ReplicatorARN  string
	ReplicatorName string
	State          string
	CreationTime   time.Time
	Tags           map[string]string
	RawOptions     map[string]json.RawMessage
}

// Node is a broker/zookeeper node reported by ListNodes.
type Node struct {
	NodeARN      string
	NodeType     string
	InstanceType string
	RawOptions   map[string]json.RawMessage
}

// Page is a generic pagination request.
type Page struct {
	NextToken  string
	MaxResults int32
}
