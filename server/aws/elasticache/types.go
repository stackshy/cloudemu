package elasticache

import (
	"encoding/xml"
	"strconv"
	"strings"

	cachedriver "github.com/stackshy/cloudemu/cache/driver"
)

// All ElastiCache query-protocol responses are wrapped in <FooResponse> with a
// <FooResult> child and a trailing <ResponseMetadata>. The structures below
// mirror the AWS-published XML closely enough that aws-sdk-go-v2's ElastiCache
// unmarshalers consume them without complaint.

const defaultRedisPort = 6379

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
type cacheClusterXML struct {
	CacheClusterID       string         `xml:"CacheClusterId"`
	CacheClusterStatus   string         `xml:"CacheClusterStatus"`
	CacheNodeType        string         `xml:"CacheNodeType,omitempty"`
	Engine               string         `xml:"Engine,omitempty"`
	NumCacheNodes        int            `xml:"NumCacheNodes,omitempty"`
	CacheClusterCreateAt string         `xml:"CacheClusterCreateTime,omitempty"`
	ARN                  string         `xml:"ARN,omitempty"`
	ConfigurationEndpt   *endpointXML   `xml:"ConfigurationEndpoint,omitempty"`
	CacheNodes           *cacheNodesXML `xml:"CacheNodes,omitempty"`
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

type deleteCacheClusterResponse struct {
	XMLName  xml.Name           `xml:"DeleteCacheClusterResponse"`
	Xmlns    string             `xml:"xmlns,attr"`
	Result   cacheClusterResult `xml:"DeleteCacheClusterResult"`
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
// The driver models a single-node cache, so one CacheNode carrying the endpoint
// is emitted (DescribeCacheClusters populates it only when ShowCacheNodeInfo is
// set, but including it unconditionally is harmless).
func toCacheClusterXML(info *cachedriver.CacheInfo) cacheClusterXML {
	out := cacheClusterXML{
		CacheClusterID:       info.Name,
		CacheClusterStatus:   info.Status,
		CacheNodeType:        info.NodeType,
		Engine:               info.Engine,
		NumCacheNodes:        1,
		CacheClusterCreateAt: info.CreatedAt,
	}

	if ep := splitEndpoint(info.Endpoint); ep != nil {
		out.ConfigurationEndpt = ep
		out.CacheNodes = &cacheNodesXML{CacheNode: []cacheNodeXML{{
			CacheNodeID:     "0001",
			CacheNodeStatus: info.Status,
			Endpoint:        ep,
		}}}
	}

	return out
}
