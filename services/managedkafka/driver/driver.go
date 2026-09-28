// Package driver defines the portable interface for the Google Cloud Managed
// Service for Apache Kafka control plane (managedkafka.googleapis.com/v1). It is
// control-plane only: the two resource collections a Terraform google provider
// or a real google.golang.org/api/managedkafka client CRUDs are modeled:
//
//	projects/{p}/locations/{region}/clusters/{cluster}
//	projects/{p}/locations/{region}/clusters/{cluster}/topics/{topic}
//
// Cluster mutations (create, update, delete) return location-scoped long-running
// operations, which share the operations space the shared GCP LRO poller owns:
//
//	projects/{p}/locations/{region}/operations/{op}
//
// Topic mutations are synchronous, as in the real API: create/update return the
// Topic and delete returns Empty.
//
// There is no data plane: no brokers, no bootstrap address, no produce/consume.
// Consumer groups, ACLs, Kafka Connect clusters/connectors and schema registries
// are out of scope.
package driver

import (
	"context"
	"time"
)

// Cluster is one Managed Kafka cluster. Name components are stored separately so
// the full resource name can be rebuilt without re-parsing. State, CreateTime and
// UpdateTime are output-only and owned by the provider.
type Cluster struct {
	Project  string
	Location string
	ID       string

	// VcpuCount and MemoryBytes are the capacityConfig block. The real API
	// requires vcpuCount >= 3 and a vCPU:GiB ratio between 1:1 and 1:8.
	VcpuCount   int64
	MemoryBytes int64

	// Subnets are gcpConfig.accessConfig.networkConfigs[].subnet, in order.
	Subnets []string

	// KmsKey is gcpConfig.kmsKey (immutable after create).
	KmsKey string

	// RebalanceMode is rebalanceConfig.mode. The provider defaults an unset
	// mode to NO_REBALANCE, as the real API does.
	RebalanceMode string

	// KafkaVersion is the Apache Kafka version (e.g. "3.7.x"). Optional on
	// create; the provider defaults it to "3.7.x", as the real API does.
	KafkaVersion string

	// TLS is tlsConfig; nil when the cluster has no TLS configuration.
	TLS *TLSConfig

	// AllowBrokerDownscaleOnClusterUpscale is
	// updateOptions.allowBrokerDownscaleOnClusterUpscale.
	AllowBrokerDownscaleOnClusterUpscale bool

	// BrokerDiskSizeGib is brokerCapacityConfig.diskSizeGib (per-broker disk,
	// minimum 100 GiB); 0 when no brokerCapacityConfig was supplied.
	BrokerDiskSizeGib int64

	Labels map[string]string

	State        string
	SatisfiesPzi bool
	SatisfiesPzs bool
	CreateTime   time.Time
	UpdateTime   time.Time
}

// Topic is one Kafka topic nested under a cluster.
type Topic struct {
	Project   string
	Location  string
	ClusterID string
	ID        string

	PartitionCount    int32
	ReplicationFactor int32
	Configs           map[string]string
}

// TLSConfig is the cluster's tlsConfig block.
type TLSConfig struct {
	// SSLPrincipalMappingRules is tlsConfig.sslPrincipalMappingRules.
	SSLPrincipalMappingRules string
	// CAPools are tlsConfig.trustConfig.casConfigs[].caPool, in order.
	CAPools []string
}

// Operation is a completed long-running operation. Every CloudEmu mutation
// finishes synchronously, so Done is always true. CreateTime, EndTime,
// TargetName, Type and APIVersion render as the operation's
// google.cloud.managedkafka.v1.OperationMetadata.
type Operation struct {
	Name       string // projects/{p}/locations/{region}/operations/{op}
	Done       bool
	TargetName string // the cluster the operation acted on
	Type       string // create | update | delete (OperationMetadata.verb)
	APIVersion string // OperationMetadata.apiVersion ("v1")
	CreateTime time.Time
	EndTime    time.Time
}

// ManagedKafka is the control-plane interface a provider implements.
type ManagedKafka interface {
	CreateCluster(ctx context.Context, c *Cluster) (*Cluster, *Operation, error)
	GetCluster(ctx context.Context, project, location, id string) (*Cluster, error)
	ListClusters(ctx context.Context, project, location string) ([]Cluster, error)
	// UpdateCluster applies the fields of c named by mask (field-mask paths
	// relative to the Cluster resource; "*" means every mutable field).
	UpdateCluster(ctx context.Context, c *Cluster, mask []string) (*Cluster, *Operation, error)
	// DeleteCluster removes the cluster and every topic under it.
	DeleteCluster(ctx context.Context, project, location, id string) (*Operation, error)

	CreateTopic(ctx context.Context, t *Topic) (*Topic, error)
	GetTopic(ctx context.Context, project, location, clusterID, id string) (*Topic, error)
	ListTopics(ctx context.Context, project, location, clusterID string) ([]Topic, error)
	// UpdateTopic applies the fields of t named by mask ("*" means every mutable
	// field). partitionCount may only increase.
	UpdateTopic(ctx context.Context, t *Topic, mask []string) (*Topic, error)
	DeleteTopic(ctx context.Context, project, location, clusterID, id string) error

	// GetOperation returns an operation this driver created; unknown is NOT_FOUND.
	// The store is bounded, so a very old (evicted) name is NOT_FOUND too.
	GetOperation(ctx context.Context, name string) (*Operation, error)
}
