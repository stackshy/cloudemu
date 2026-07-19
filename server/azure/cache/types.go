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

// skuJSON mirrors the armredis SKU. The cache driver only tracks a node-type
// string, which maps onto SKU.Name (Basic/Standard/Premium); Family and
// Capacity carry stub defaults so the SDK round-trips a well-formed SKU.
type skuJSON struct {
	Name     string `json:"name,omitempty"`
	Family   string `json:"family,omitempty"`
	Capacity int    `json:"capacity,omitempty"`
}

// redisProperties mirrors the subset of armredis Properties the cache driver
// can populate.
type redisProperties struct {
	ProvisioningState string   `json:"provisioningState,omitempty"`
	RedisVersion      string   `json:"redisVersion,omitempty"`
	SKU               *skuJSON `json:"sku,omitempty"`
	HostName          string   `json:"hostName,omitempty"`
	SSLPort           int      `json:"sslPort,omitempty"`
	Port              int      `json:"port,omitempty"`
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
			ProvisioningState: provisioningStateSucceeded,
			RedisVersion:      defaultRedisVersion,
			SKU:               &skuJSON{Name: skuNameFromNodeType(info.NodeType), Family: "C", Capacity: 1},
			HostName:          host,
			SSLPort:           sslPort,
		},
	}
}
