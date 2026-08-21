package cache

import (
	"strconv"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	cachedriver "github.com/stackshy/cloudemu/v2/services/cache/driver"
)

const (
	redisResourceType = "Microsoft.Cache/redis"
	defaultLocation   = "eastus"
	// provisioningStateSucceeded is the terminal LRO state; the mock applies
	// mutations synchronously so every response is already Succeeded, which
	// terminates the SDK's poller on the first response.
	provisioningStateSucceeded = "Succeeded"
	defaultRedisSSLPort        = 6380
	defaultRedisVersion        = "6.0"
)

// skuJSON mirrors the armredis SKU. Name is the tier (Basic/Standard/Premium),
// Family is C (Basic/Standard) or P (Premium), and Capacity is the size unit —
// all recorded on the driver so the SDK round-trips the exact SKU it sent.
type skuJSON struct {
	Name     string `json:"name,omitempty"`
	Family   string `json:"family,omitempty"`
	Capacity int    `json:"capacity,omitempty"`
}

// redisProperties mirrors the subset of armredis Properties the cache driver
// can populate. ShardCount and ReplicasPerPrimary describe a Premium clustered
// cache; both are omitted when unset (a non-clustered cache).
type redisProperties struct {
	ProvisioningState  string   `json:"provisioningState,omitempty"`
	RedisVersion       string   `json:"redisVersion,omitempty"`
	SKU                *skuJSON `json:"sku,omitempty"`
	HostName           string   `json:"hostName,omitempty"`
	SSLPort            int      `json:"sslPort,omitempty"`
	Port               int      `json:"port,omitempty"`
	ShardCount         int      `json:"shardCount,omitempty"`
	ReplicasPerPrimary int      `json:"replicasPerPrimary,omitempty"`
	// ReplicasPerMaster is the legacy alias for ReplicasPerPrimary. It is
	// accepted on input (older SDKs still send it) but not emitted — the
	// response carries the current replicasPerPrimary field.
	ReplicasPerMaster int `json:"replicasPerMaster,omitempty"`
}

// redisJSON mirrors the armredis ResourceInfo / CreateParameters resource.
type redisJSON struct {
	ID         string            `json:"id,omitempty"`
	Name       string            `json:"name,omitempty"`
	Type       string            `json:"type,omitempty"`
	Location   string            `json:"location,omitempty"`
	Tags       map[string]string `json:"tags,omitempty"`
	Properties *redisProperties  `json:"properties,omitempty"`
}

type redisListResult struct {
	Value []redisJSON `json:"value"`
}

// skuNameFromNodeType extracts the SKU tier (Basic/Standard/Premium) from the
// driver's node-type string. Azure node types look like "Standard_C1", so the
// tier is the segment before the underscore. Falls back to "Standard".
func skuNameFromNodeType(nodeType string) string {
	if i := strings.Index(nodeType, "_"); i > 0 {
		return nodeType[:i]
	}

	if nodeType != "" {
		return nodeType
	}

	return "Standard"
}

// hostAndSSLPort splits the driver's "host:port" endpoint. Azure Redis exposes
// an SSL port (default 6380); the port suffix, when present, is used.
func hostAndSSLPort(endpoint string) (string, int) {
	if endpoint == "" {
		return "", defaultRedisSSLPort
	}

	host := endpoint
	port := defaultRedisSSLPort

	if i := strings.LastIndex(endpoint, ":"); i >= 0 {
		host = endpoint[:i]
		if p, err := strconv.Atoi(endpoint[i+1:]); err == nil {
			port = p
		}
	}

	return host, port
}

// toRedisJSON converts a driver CacheInfo into its ARM element. The id carries
// the scope the cache was created in; resources without a recorded scope
// (portable-API creations) fall back to the request path's scope.
func toRedisJSON(rp *azurearm.ResourcePath, info *cachedriver.CacheInfo) redisJSON {
	host, sslPort := hostAndSSLPort(info.Endpoint)

	sub := info.Scope.Subscription
	if sub == "" {
		sub = rp.Subscription
	}

	rg := info.Scope.ResourceGroup
	if rg == "" {
		rg = rp.ResourceGroup
	}

	return redisJSON{
		ID:       azurearm.BuildResourceID(sub, rg, providerName, typeRedis, info.Name),
		Name:     info.Name,
		Type:     redisResourceType,
		Location: defaultLocation,
		Tags:     info.Tags,
		Properties: &redisProperties{
			ProvisioningState:  provisioningStateSucceeded,
			RedisVersion:       defaultRedisVersion,
			SKU:                skuFromInfo(info),
			HostName:           host,
			SSLPort:            sslPort,
			ShardCount:         info.ShardCount,
			ReplicasPerPrimary: info.ReplicasPerPrimary,
		},
	}
}

// skuFromInfo builds the ARM SKU from the recorded driver fields. Family
// defaults to "C" (Basic/Standard) and capacity to 1 only when the driver has
// no recorded value — e.g. a cache created through the portable API, which
// carries a node type but no SKU family/capacity.
func skuFromInfo(info *cachedriver.CacheInfo) *skuJSON {
	family := info.SKUFamily
	if family == "" {
		family = "C"
	}

	capacity := info.SKUCapacity
	if capacity == 0 {
		capacity = 1
	}

	return &skuJSON{
		Name:     skuNameFromNodeType(info.NodeType),
		Family:   family,
		Capacity: capacity,
	}
}
