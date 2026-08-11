package driver

import (
	"context"
	"encoding/json"
)

// Kafka is the interface an Amazon MSK backend implements. It covers the full
// 59-operation MSK control plane: clusters (v1/v2), configurations, cluster
// operations and updates, nodes, versions, tags, VPC connections, topics,
// SCRAM secrets, cluster policies, and replicators.
//
//nolint:interfacebloat // Amazon MSK exposes 59 operations; full parity requires them all.
type Kafka interface {
	// Clusters (v1).
	CreateCluster(ctx context.Context, in CreateClusterInput) (*Cluster, error)
	DescribeCluster(ctx context.Context, arn string) (*Cluster, error)
	ListClusters(ctx context.Context, namePrefix string, page Page) ([]Cluster, string, error)
	DeleteCluster(ctx context.Context, arn, currentVersion string) (arnOut, state string, err error)
	GetBootstrapBrokers(ctx context.Context, arn string) (map[string]string, error)

	// Clusters (v2).
	CreateClusterV2(ctx context.Context, in CreateClusterV2Input) (*Cluster, error)
	DescribeClusterV2(ctx context.Context, arn string) (*Cluster, error)
	ListClustersV2(ctx context.Context, namePrefix, clusterType string, page Page) ([]Cluster, string, error)

	// Cluster nodes & operations.
	ListNodes(ctx context.Context, arn string, page Page) ([]Node, string, error)
	ListClusterOperations(ctx context.Context, arn string, page Page) ([]ClusterOperation, string, error)
	ListClusterOperationsV2(ctx context.Context, arn string, page Page) ([]ClusterOperation, string, error)
	DescribeClusterOperation(ctx context.Context, opARN string) (*ClusterOperation, error)
	DescribeClusterOperationV2(ctx context.Context, opARN string) (*ClusterOperation, error)

	// Cluster mutations.
	UpdateBrokerCount(ctx context.Context, arn, currentVersion string, target int32) (*ClusterOperation, error)
	UpdateBrokerStorage(ctx context.Context, arn, currentVersion string, body json.RawMessage) (*ClusterOperation, error)
	UpdateBrokerType(ctx context.Context, arn, currentVersion, targetType string) (*ClusterOperation, error)
	UpdateStorage(ctx context.Context, arn, currentVersion string, body json.RawMessage) (*ClusterOperation, error)
	UpdateClusterConfiguration(ctx context.Context, arn, currentVersion string, body json.RawMessage) (*ClusterOperation, error)
	UpdateClusterKafkaVersion(ctx context.Context, arn, currentVersion string, body json.RawMessage) (*ClusterOperation, error)
	UpdateConnectivity(ctx context.Context, arn, currentVersion string, body json.RawMessage) (*ClusterOperation, error)
	UpdateMonitoring(ctx context.Context, arn, currentVersion string, body json.RawMessage) (*ClusterOperation, error)
	UpdateSecurity(ctx context.Context, arn, currentVersion string, body json.RawMessage) (*ClusterOperation, error)
	UpdateRebalancing(ctx context.Context, arn, currentVersion string, body json.RawMessage) (*ClusterOperation, error)
	RebootBroker(ctx context.Context, arn string, brokerIDs []string) (*ClusterOperation, error)

	// Configurations.
	CreateConfiguration(ctx context.Context, in CreateConfigurationInput) (*Configuration, error)
	DescribeConfiguration(ctx context.Context, arn string) (*Configuration, error)
	ListConfigurations(ctx context.Context, page Page) ([]Configuration, string, error)
	UpdateConfiguration(ctx context.Context, arn, description string, serverProperties []byte) (*Configuration, error)
	DeleteConfiguration(ctx context.Context, arn string) (arnOut, state string, err error)
	ListConfigurationRevisions(ctx context.Context, arn string, page Page) ([]ConfigurationRevision, string, error)
	DescribeConfigurationRevision(ctx context.Context, arn string, revision int64) (*Configuration, *ConfigurationRevision, error)

	// VPC connections.
	CreateVpcConnection(ctx context.Context, body json.RawMessage) (*VpcConnection, error)
	DescribeVpcConnection(ctx context.Context, arn string) (*VpcConnection, error)
	DeleteVpcConnection(ctx context.Context, arn string) (*VpcConnection, error)
	ListVpcConnections(ctx context.Context, page Page) ([]VpcConnection, string, error)
	ListClientVpcConnections(ctx context.Context, clusterARN string, page Page) ([]VpcConnection, string, error)
	RejectClientVpcConnection(ctx context.Context, clusterARN string, body json.RawMessage) error

	// Topics.
	CreateTopic(ctx context.Context, clusterARN string, body json.RawMessage) (*Topic, error)
	ListTopics(ctx context.Context, clusterARN string, page Page) ([]Topic, string, error)
	DescribeTopic(ctx context.Context, clusterARN, topicName string) (*Topic, error)
	UpdateTopic(ctx context.Context, clusterARN, topicName string, body json.RawMessage) (*Topic, error)
	DeleteTopic(ctx context.Context, clusterARN, topicName string) error
	DescribeTopicPartitions(ctx context.Context, clusterARN, topicName string, page Page) ([]json.RawMessage, string, error)

	// SCRAM secrets.
	BatchAssociateScramSecret(ctx context.Context, clusterARN string, secretARNs []string) ([]json.RawMessage, error)
	BatchDisassociateScramSecret(ctx context.Context, clusterARN string, secretARNs []string) ([]json.RawMessage, error)
	ListScramSecrets(ctx context.Context, clusterARN string, page Page) ([]string, string, error)

	// Cluster policy.
	PutClusterPolicy(ctx context.Context, clusterARN string, body json.RawMessage) (string, error)
	GetClusterPolicy(ctx context.Context, clusterARN string) (currentVersion, policy string, err error)
	DeleteClusterPolicy(ctx context.Context, clusterARN string) error

	// Replicators.
	CreateReplicator(ctx context.Context, body json.RawMessage) (*Replicator, error)
	ListReplicators(ctx context.Context, namePrefix string, page Page) ([]Replicator, string, error)
	DescribeReplicator(ctx context.Context, arn string) (*Replicator, error)
	DeleteReplicator(ctx context.Context, arn, currentVersion string) (arnOut, state string, err error)
	UpdateReplicationInfo(ctx context.Context, arn string, body json.RawMessage) (*Replicator, error)

	// Tags.
	TagResource(ctx context.Context, resourceARN string, tags map[string]string) error
	ListTagsForResource(ctx context.Context, resourceARN string) (map[string]string, error)
	UntagResource(ctx context.Context, resourceARN string, keys []string) error

	// Versions (read-only).
	ListKafkaVersions(ctx context.Context, page Page) ([]json.RawMessage, string, error)
	GetCompatibleKafkaVersions(ctx context.Context, clusterARN string) ([]json.RawMessage, error)
}
