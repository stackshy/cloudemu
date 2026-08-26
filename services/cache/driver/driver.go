// Package driver defines the interface for cache service implementations.
package driver

import (
	"context"
	"time"

	"github.com/stackshy/cloudemu/v2/services/scope"
)

// Item represents a cached item.
type Item struct {
	Key       string
	Value     []byte
	TTL       time.Duration
	ExpiresAt time.Time
}

// CacheInfo describes a cache instance.
type CacheInfo struct {
	Name      string
	NodeType  string
	Engine    string
	Status    string
	Endpoint  string
	CreatedAt string
	Tags      map[string]string
	Scope     scope.Scope

	// Location is the geo-region the cache was created in (Azure Redis
	// `location`, e.g. "westus2"). Empty for providers with no per-resource
	// region concept.
	Location string

	// PrimaryKey / SecondaryKey are the Azure Redis access keys clients use to
	// authenticate to the cache. Generated at create time by the Azure backend
	// and rotated by RegenerateCacheKey; empty for providers that do not model
	// access keys.
	PrimaryKey   string
	SecondaryKey string

	// ARN and EngineVersion are populated by the AWS ElastiCache backend (other
	// clouds leave them empty). The ARN is the tag-operation handle, so tag
	// read/write flows depend on it being set on Describe.
	ARN           string
	EngineVersion string

	// SKUFamily / SKUCapacity are the Azure Redis SKU family ("C" for
	// Basic/Standard, "P" for Premium) and capacity unit; empty/zero for
	// providers with no such concept. ShardCount and ReplicasPerPrimary model
	// a Premium clustered cache (both drive node-count cost); zero when
	// clustering is not configured.
	SKUFamily          string
	SKUCapacity        int
	ShardCount         int
	ReplicasPerPrimary int

	// NumCacheNodes / SubnetGroupName are populated by the AWS ElastiCache
	// backend. NumCacheNodes is the number of nodes in a Memcached cluster (1
	// for Redis); SubnetGroupName is the cache subnet group the cluster is
	// placed in, and deleting that group is refused while a cluster references
	// it. Both are empty/zero for non-AWS providers.
	NumCacheNodes   int
	SubnetGroupName string

	// ParameterGroupName is the custom AWS ElastiCache cache parameter group
	// attached to the cluster, echoed back on Describe so IaC that references a
	// custom group converges. Empty means the backend reports the engine's
	// default (default.<family>). AWS-only and left empty by other callers.
	ParameterGroupName string
}

// CacheConfig describes a cache instance to create.
type CacheConfig struct {
	Name          string
	NodeType      string
	Engine        string // "redis", "memcached"
	EngineVersion string
	Tags          map[string]string

	// Location is the geo-region the cache is created in (Azure Redis
	// `location`). Optional and left empty by non-Azure callers.
	Location string

	// Scope records where the resource lives (Azure subscription/resource
	// group, GCP project). Zero for AWS and unscoped portable callers.
	Scope scope.Scope

	// SKUFamily / SKUCapacity carry the Azure Redis SKU family ("C"/"P") and
	// capacity unit. ShardCount / ReplicasPerPrimary configure a Premium
	// clustered cache. All are optional and left zero by non-Azure callers.
	SKUFamily          string
	SKUCapacity        int
	ShardCount         int
	ReplicasPerPrimary int

	// NumCacheNodes is the requested node count for an AWS Memcached cluster
	// (1-40); zero means unspecified and the backend defaults it to 1.
	// SubnetGroupName names the cache subnet group the cluster is placed in.
	// Both are AWS-only and left zero/empty by other callers.
	NumCacheNodes   int
	SubnetGroupName string

	// Port is the requested TCP port the AWS ElastiCache cluster listens on;
	// zero means unspecified and the backend defaults it per engine (Redis
	// 6379, Memcached 11211). AWS-only and left zero by other callers.
	Port int

	// ParameterGroupName names a custom AWS ElastiCache cache parameter group
	// to attach; empty means the backend reports the engine's default
	// (default.<family>). AWS-only and left empty by other callers.
	ParameterGroupName string
}

// ModifyCacheConfig carries the mutable fields of an AWS ElastiCache
// ModifyCacheCluster call. It is an AWS-only surface (not part of the portable
// Cache interface); the wire handler type-asserts for a modifier that accepts
// it. Empty/zero fields leave the corresponding attribute unchanged.
type ModifyCacheConfig struct {
	Name          string
	NodeType      string
	EngineVersion string

	// NumCacheNodes rescales a Memcached cluster; zero leaves the node count
	// unchanged. The backend re-validates it against the cluster's engine.
	NumCacheNodes int
}

// Cache is the interface that cache provider implementations must satisfy.
type Cache interface {
	CreateCache(ctx context.Context, config CacheConfig) (*CacheInfo, error)

	// UpdateCache replaces the mutable fields (node type, tags) of an
	// existing cache, mirroring ARM CreateOrUpdate-on-existing.
	UpdateCache(ctx context.Context, config CacheConfig) (*CacheInfo, error)
	DeleteCache(ctx context.Context, name string) error
	GetCache(ctx context.Context, name string) (*CacheInfo, error)
	ListCaches(ctx context.Context, filter scope.Scope) ([]CacheInfo, error)

	Set(ctx context.Context, cacheName, key string, value []byte, ttl time.Duration) error
	Get(ctx context.Context, cacheName, key string) (*Item, error)
	Delete(ctx context.Context, cacheName, key string) error
	Keys(ctx context.Context, cacheName, pattern string) ([]string, error)
	FlushAll(ctx context.Context, cacheName string) error

	// TTL management
	Expire(ctx context.Context, cacheName, key string, ttl time.Duration) error
	GetTTL(ctx context.Context, cacheName, key string) (time.Duration, error)
	Persist(ctx context.Context, cacheName, key string) error

	// Atomic counters
	Incr(ctx context.Context, cacheName, key string) (int64, error)
	IncrBy(ctx context.Context, cacheName, key string, delta int64) (int64, error)
	Decr(ctx context.Context, cacheName, key string) (int64, error)
	DecrBy(ctx context.Context, cacheName, key string, delta int64) (int64, error)
}

// SubnetGroup is a named set of subnets an ElastiCache cluster is placed into.
type SubnetGroup struct {
	Name        string
	Description string
	VPCID       string
	SubnetIDs   []string
	Status      string
	ARN         string
}

// SubnetGroupConfig describes a cache subnet group to create.
type SubnetGroupConfig struct {
	Name        string
	Description string
	SubnetIDs   []string
}

// SubnetGroups is an OPTIONAL capability, discovered by type assertion. Cache
// subnet groups are an AWS concept; drivers for other clouds do not model them
// and answering InvalidAction is the truthful response there.
type SubnetGroups interface {
	CreateCacheSubnetGroup(ctx context.Context, cfg SubnetGroupConfig) (*SubnetGroup, error)
	DescribeCacheSubnetGroups(ctx context.Context, names []string) ([]SubnetGroup, error)
	DeleteCacheSubnetGroup(ctx context.Context, name string) error
}

// ReplicationGroup is a primary cache node plus its replicas, addressed
// through a single primary endpoint.
type ReplicationGroup struct {
	ID              string
	Description     string
	Status          string
	Engine          string
	EngineVersion   string
	NodeType        string
	NumCacheNodes   int
	PrimaryAddress  string
	PrimaryPort     int
	SubnetGroupName string
	ARN             string

	// ReaderAddress / ReaderPort are the read-only endpoint clients use to scale
	// reads across the group's replicas. AWS-only; empty for other clouds.
	ReaderAddress string
	ReaderPort    int

	// MemberClusters is the set of cache cluster ids that make up the group
	// (`<id>-001`, `<id>-002`, …), read by IaC to enumerate the group's nodes.
	MemberClusters []string

	// AutomaticFailover is the failover status ("enabled" / "disabled") IaC
	// reads back to confirm the requested setting took effect.
	AutomaticFailover string
}

// ReplicationGroupConfig describes a replication group to create.
type ReplicationGroupConfig struct {
	ID               string
	Description      string
	Engine           string
	EngineVersion    string
	NodeType         string
	NumCacheNodes    int
	SubnetGroupName  string
	SecurityGroupIDs []string

	// AutomaticFailoverEnabled requests automatic failover for the group,
	// reflected as AutomaticFailover ("enabled"/"disabled") on Describe.
	AutomaticFailoverEnabled bool
}

// DeleteReplicationGroupOptions carries the optional delete-time behaviors of
// ElastiCache DeleteReplicationGroup. FinalSnapshotIdentifier, when set, takes a
// final snapshot of the group before it is removed; RetainPrimaryCluster, when
// set, keeps the primary node group as a standalone cache cluster instead of
// deleting it.
type DeleteReplicationGroupOptions struct {
	RetainPrimaryCluster    bool
	FinalSnapshotIdentifier string
}

// ReplicationGroups is an OPTIONAL capability, discovered by type assertion.
// Replication groups are an AWS ElastiCache concept; drivers for other clouds
// do not model them.
type ReplicationGroups interface {
	CreateReplicationGroup(ctx context.Context, cfg ReplicationGroupConfig) (*ReplicationGroup, error)
	DescribeReplicationGroups(ctx context.Context, ids []string) ([]ReplicationGroup, error)
	ModifyReplicationGroup(ctx context.Context, id string, numCacheNodes int) (*ReplicationGroup, error)
	DeleteReplicationGroup(ctx context.Context, id string, opts DeleteReplicationGroupOptions) error
}

// Snapshot is a point-in-time backup of an ElastiCache cluster or replication
// group. It captures the source's engine/node identity so a restore can
// recreate a like-for-like cluster.
type Snapshot struct {
	Name               string
	CacheClusterID     string
	ReplicationGroupID string
	Status             string
	Source             string
	Engine             string
	EngineVersion      string
	NodeType           string
	NumCacheNodes      int
	Port               int
	ParameterGroupName string
	SnapshotWindow     string
	RetentionLimit     int
	ARN                string
	CreatedAt          time.Time
}

// SnapshotConfig describes a snapshot to create. Exactly one of CacheClusterID
// or ReplicationGroupID identifies the source.
type SnapshotConfig struct {
	SnapshotName       string
	CacheClusterID     string
	ReplicationGroupID string
	KmsKeyID           string
	Tags               map[string]string
}

// SnapshotFilter narrows a DescribeSnapshots call. Empty fields do not filter.
type SnapshotFilter struct {
	SnapshotName       string
	CacheClusterID     string
	ReplicationGroupID string
}

// Snapshots is an OPTIONAL capability, discovered by type assertion.
// ElastiCache snapshots are an AWS concept (Redis/Valkey only); drivers for other
// clouds do not model them and answering InvalidAction is the truthful response.
type Snapshots interface {
	CreateSnapshot(ctx context.Context, cfg SnapshotConfig) (*Snapshot, error)
	DescribeSnapshots(ctx context.Context, filter SnapshotFilter) ([]Snapshot, error)
}

// AccessKeys is an OPTIONAL capability, discovered by type assertion. Azure
// Cache for Redis exposes the cache's two access keys via listKeys and rotates
// them via regenerateKey; no other cloud in this emulator models cache access
// keys, so their drivers do not implement this interface.
type AccessKeys interface {
	// ListCacheKeys returns the cache's current primary and secondary access
	// keys.
	ListCacheKeys(ctx context.Context, name string) (primary, secondary string, err error)

	// RegenerateCacheKey rotates the requested key ("Primary" or "Secondary")
	// and returns both current keys.
	RegenerateCacheKey(ctx context.Context, name, keyType string) (primary, secondary string, err error)
}
