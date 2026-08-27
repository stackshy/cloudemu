package elasticache

import (
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"

	cachedriver "github.com/stackshy/cloudemu/v2/services/cache/driver"
)

// All ElastiCache query-protocol responses are wrapped in <FooResponse> with a
// <FooResult> child and a trailing <ResponseMetadata>. The structures below
// mirror the AWS-published XML closely enough that aws-sdk-go-v2's ElastiCache
// unmarshalers consume them without complaint.

const defaultRedisPort = 6379

// memcachedEngine is the ElastiCache engine name whose endpoint semantics differ
// from Redis/Valkey (single configuration endpoint on port 11211).
const memcachedEngine = "memcached"

// maxCacheNodesPerCluster is the real ElastiCache ceiling on nodes in a single
// cache cluster (Memcached allows up to 40; Redis is single-node). It bounds the
// per-node allocation when rendering the cluster's XML.
const maxCacheNodesPerCluster = 40

type responseMetadata struct {
	RequestID string `xml:"RequestId"`
}

type endpointXML struct {
	Address string `xml:"Address,omitempty"`
	Port    int    `xml:"Port,omitempty"`
}

// cacheNodeXML mirrors AWS's CacheNode. The SDK reads the per-node Endpoint to
// populate CacheCluster.CacheNodes[].Endpoint; that is where a single-node
// Redis cluster's connection address lives.
type cacheNodeXML struct {
	CacheNodeID     string       `xml:"CacheNodeId"`
	CacheNodeStatus string       `xml:"CacheNodeStatus"`
	Endpoint        *endpointXML `xml:"Endpoint,omitempty"`
}

type cacheNodesXML struct {
	CacheNode []cacheNodeXML `xml:"CacheNode,omitempty"`
}

// cacheClusterXML mirrors AWS's CacheCluster resource. Only the fields the
// cache driver can populate are emitted; the rest are omitted.
type cacheParameterGroupStatusXML struct {
	CacheParameterGroupName string `xml:"CacheParameterGroupName"`
	ParameterApplyStatus    string `xml:"ParameterApplyStatus"`
}

type cacheClusterXML struct {
	CacheClusterID       string                        `xml:"CacheClusterId"`
	CacheClusterStatus   string                        `xml:"CacheClusterStatus"`
	CacheNodeType        string                        `xml:"CacheNodeType,omitempty"`
	Engine               string                        `xml:"Engine,omitempty"`
	EngineVersion        string                        `xml:"EngineVersion,omitempty"`
	NumCacheNodes        int                           `xml:"NumCacheNodes,omitempty"`
	CacheClusterCreateAt string                        `xml:"CacheClusterCreateTime,omitempty"`
	ARN                  string                        `xml:"ARN,omitempty"`
	CacheParameterGroup  *cacheParameterGroupStatusXML `xml:"CacheParameterGroup,omitempty"`
	ConfigurationEndpt   *endpointXML                  `xml:"ConfigurationEndpoint,omitempty"`
	CacheNodes           *cacheNodesXML                `xml:"CacheNodes,omitempty"`
}

// --- response envelopes, one per Action ---

type cacheClusterResult struct {
	CacheCluster cacheClusterXML `xml:"CacheCluster"`
}

type createCacheClusterResponse struct {
	XMLName  xml.Name           `xml:"CreateCacheClusterResponse"`
	Xmlns    string             `xml:"xmlns,attr"`
	Result   cacheClusterResult `xml:"CreateCacheClusterResult"`
	Metadata responseMetadata   `xml:"ResponseMetadata"`
}

type modifyCacheClusterResponse struct {
	XMLName  xml.Name           `xml:"ModifyCacheClusterResponse"`
	Xmlns    string             `xml:"xmlns,attr"`
	Result   cacheClusterResult `xml:"ModifyCacheClusterResult"`
	Metadata responseMetadata   `xml:"ResponseMetadata"`
}

type deleteCacheClusterResponse struct {
	XMLName  xml.Name           `xml:"DeleteCacheClusterResponse"`
	Xmlns    string             `xml:"xmlns,attr"`
	Result   cacheClusterResult `xml:"DeleteCacheClusterResult"`
	Metadata responseMetadata   `xml:"ResponseMetadata"`
}

type rebootCacheClusterResponse struct {
	XMLName  xml.Name           `xml:"RebootCacheClusterResponse"`
	Xmlns    string             `xml:"xmlns,attr"`
	Result   cacheClusterResult `xml:"RebootCacheClusterResult"`
	Metadata responseMetadata   `xml:"ResponseMetadata"`
}

type cacheClustersXML struct {
	CacheCluster []cacheClusterXML `xml:"CacheCluster,omitempty"`
}

type describeCacheClustersResult struct {
	CacheClusters cacheClustersXML `xml:"CacheClusters"`
}

type describeCacheClustersResponse struct {
	XMLName  xml.Name                    `xml:"DescribeCacheClustersResponse"`
	Xmlns    string                      `xml:"xmlns,attr"`
	Result   describeCacheClustersResult `xml:"DescribeCacheClustersResult"`
	Metadata responseMetadata            `xml:"ResponseMetadata"`
}

// nodeSnapshotXML mirrors AWS's NodeSnapshot — the per-node backup record a
// caller reads to size a restore.
type nodeSnapshotXML struct {
	CacheNodeID         string `xml:"CacheNodeId,omitempty"`
	CacheNodeCreateTime string `xml:"CacheNodeCreateTime,omitempty"`
	SnapshotCreateTime  string `xml:"SnapshotCreateTime,omitempty"`
	CacheSize           string `xml:"CacheSize,omitempty"`
}

// snapshotXML mirrors AWS's Snapshot resource. Only the fields the cache driver
// can populate are emitted; the rest are omitted.
type snapshotXML struct {
	SnapshotName            string            `xml:"SnapshotName"`
	CacheClusterID          string            `xml:"CacheClusterId,omitempty"`
	ReplicationGroupID      string            `xml:"ReplicationGroupId,omitempty"`
	SnapshotStatus          string            `xml:"SnapshotStatus,omitempty"`
	SnapshotSource          string            `xml:"SnapshotSource,omitempty"`
	Engine                  string            `xml:"Engine,omitempty"`
	EngineVersion           string            `xml:"EngineVersion,omitempty"`
	CacheNodeType           string            `xml:"CacheNodeType,omitempty"`
	NumCacheNodes           int               `xml:"NumCacheNodes,omitempty"`
	Port                    int               `xml:"Port,omitempty"`
	CacheParameterGroupName string            `xml:"CacheParameterGroupName,omitempty"`
	SnapshotRetentionLimit  int               `xml:"SnapshotRetentionLimit,omitempty"`
	SnapshotWindow          string            `xml:"SnapshotWindow,omitempty"`
	ARN                     string            `xml:"ARN,omitempty"`
	NodeSnapshots           []nodeSnapshotXML `xml:"NodeSnapshots>NodeSnapshot,omitempty"`
}

type snapshotResult struct {
	Snapshot snapshotXML `xml:"Snapshot"`
}

type createSnapshotResponse struct {
	XMLName  xml.Name         `xml:"CreateSnapshotResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   snapshotResult   `xml:"CreateSnapshotResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type snapshotsListXML struct {
	Snapshot []snapshotXML `xml:"Snapshot,omitempty"`
}

type describeSnapshotsResult struct {
	Marker    string           `xml:"Marker,omitempty"`
	Snapshots snapshotsListXML `xml:"Snapshots"`
}

type describeSnapshotsResponse struct {
	XMLName  xml.Name                `xml:"DescribeSnapshotsResponse"`
	Xmlns    string                  `xml:"xmlns,attr"`
	Result   describeSnapshotsResult `xml:"DescribeSnapshotsResult"`
	Metadata responseMetadata        `xml:"ResponseMetadata"`
}

// toSnapshotXML converts a driver Snapshot into its ElastiCache XML shape.
func toSnapshotXML(s *cachedriver.Snapshot) snapshotXML {
	created := s.CreatedAt.UTC().Format("2006-01-02T15:04:05Z")

	return snapshotXML{
		SnapshotName:            s.Name,
		CacheClusterID:          s.CacheClusterID,
		ReplicationGroupID:      s.ReplicationGroupID,
		SnapshotStatus:          s.Status,
		SnapshotSource:          s.Source,
		Engine:                  s.Engine,
		EngineVersion:           s.EngineVersion,
		CacheNodeType:           s.NodeType,
		NumCacheNodes:           s.NumCacheNodes,
		Port:                    s.Port,
		CacheParameterGroupName: s.ParameterGroupName,
		SnapshotRetentionLimit:  s.RetentionLimit,
		SnapshotWindow:          s.SnapshotWindow,
		ARN:                     s.ARN,
		NodeSnapshots: []nodeSnapshotXML{{
			CacheNodeID:         "0001",
			CacheNodeCreateTime: created,
			SnapshotCreateTime:  created,
			CacheSize:           "0",
		}},
	}
}

// defaultParamGroupName derives the default cache parameter group name AWS
// assigns to a cluster, e.g. "default.redis7" or "default.memcached1.6". For
// redis the family is engine + major version; for memcached it is major.minor.
func defaultParamGroupName(engine, version string) string {
	family := engine

	parts := strings.Split(version, ".")
	switch {
	case engine == "memcached" && len(parts) >= 2:
		family = engine + parts[0] + "." + parts[1]
	case len(parts) >= 1 && parts[0] != "":
		family = engine + parts[0]
	}

	return "default." + family
}

// splitEndpoint separates the driver's "host:port" endpoint into an Address and
// Port. Falls back to the default Redis port when no ":port" suffix is present.
func splitEndpoint(endpoint string) *endpointXML {
	if endpoint == "" {
		return nil
	}

	host := endpoint
	port := defaultRedisPort

	if i := strings.LastIndex(endpoint, ":"); i >= 0 {
		host = endpoint[:i]
		if p, err := strconv.Atoi(endpoint[i+1:]); err == nil {
			port = p
		}
	}

	return &endpointXML{Address: host, Port: port}
}

// toCacheClusterXML converts a driver CacheInfo into its ElastiCache XML shape.
// A Memcached cluster reports NumCacheNodes nodes (Redis reports 1); a
// CacheNode carrying the endpoint is emitted per node (DescribeCacheClusters
// populates them only when ShowCacheNodeInfo is set, but including them
// unconditionally is harmless).
func toCacheClusterXML(info *cachedriver.CacheInfo) cacheClusterXML {
	numNodes := info.NumCacheNodes
	if numNodes < 1 {
		numNodes = 1
	}

	// Defensive clamp to the real ElastiCache ceiling (Memcached tops out at 40
	// nodes; Redis reports 1). The stored count is validated on create, but bound
	// it here too so a tainted value can never size an unbounded node allocation.
	if numNodes > maxCacheNodesPerCluster {
		numNodes = maxCacheNodesPerCluster
	}

	out := cacheClusterXML{
		CacheClusterID:       info.Name,
		CacheClusterStatus:   info.Status,
		CacheNodeType:        info.NodeType,
		Engine:               info.Engine,
		EngineVersion:        info.EngineVersion,
		NumCacheNodes:        numNodes,
		CacheClusterCreateAt: info.CreatedAt,
		ARN:                  info.ARN,
	}

	if info.Engine != "" {
		// A custom parameter group the caller attached is echoed verbatim;
		// otherwise report the engine family's default (default.<family>).
		paramGroup := info.ParameterGroupName
		if paramGroup == "" {
			paramGroup = defaultParamGroupName(info.Engine, info.EngineVersion)
		}

		out.CacheParameterGroup = &cacheParameterGroupStatusXML{
			CacheParameterGroupName: paramGroup,
			ParameterApplyStatus:    "in-sync",
		}
	}

	if ep := splitEndpoint(info.Endpoint); ep != nil {
		// ConfigurationEndpoint is a Memcached-only field in the real API (its
		// host always carries ".cfg"). Redis/Valkey clusters leave it nil and
		// expose their address through the per-node CacheNodes endpoint instead.
		if info.Engine == memcachedEngine {
			out.ConfigurationEndpt = ep
		}

		nodes := make([]cacheNodeXML, 0, numNodes)
		for i := 1; i <= numNodes; i++ {
			nodes = append(nodes, cacheNodeXML{
				CacheNodeID:     fmt.Sprintf("%04d", i),
				CacheNodeStatus: info.Status,
				Endpoint:        ep,
			})
		}

		out.CacheNodes = &cacheNodesXML{CacheNode: nodes}
	}

	return out
}
