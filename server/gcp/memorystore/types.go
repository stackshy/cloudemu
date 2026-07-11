package memorystore

import (
	"encoding/json"
	"strconv"
	"strings"

	cachedriver "github.com/stackshy/cloudemu/cache/driver"
)

const (
	defaultRedisPort    = 6379
	defaultRedisVersion = "REDIS_6_X"
	defaultTier         = "BASIC"
	// stateReady is Memorystore's terminal instance state; the mock provisions
	// synchronously so every instance reports READY.
	stateReady = "READY"
)

// instanceJSON mirrors the subset of google.golang.org/api/redis/v1 Instance
// the cache driver can populate. `name` is the full resource path
// (projects/{p}/locations/{l}/instances/{i}); `memorySizeGb` is required by the
// API surface so a stub default is emitted.
type instanceJSON struct {
	Name          string            `json:"name,omitempty"`
	DisplayName   string            `json:"displayName,omitempty"`
	Tier          string            `json:"tier,omitempty"`
	MemorySizeGb  int64             `json:"memorySizeGb,omitempty"`
	RedisVersion  string            `json:"redisVersion,omitempty"`
	State         string            `json:"state,omitempty"`
	Host          string            `json:"host,omitempty"`
	Port          int64             `json:"port,omitempty"`
	CreateTime    string            `json:"createTime,omitempty"`
	Labels        map[string]string `json:"labels,omitempty"`
	LocationID    string            `json:"locationId,omitempty"`
	ReservedIPRng string            `json:"reservedIpRange,omitempty"`
}

// listInstancesResponse mirrors redis/v1 ListInstancesResponse.
type listInstancesResponse struct {
	Instances []instanceJSON `json:"instances"`
}

// operationJSON mirrors google.longrunning.Operation. Mutating ops complete
// inline, so `done` is always true; `response` carries the resulting resource.
type operationJSON struct {
	Name     string          `json:"name"`
	Done     bool            `json:"done"`
	Response json.RawMessage `json:"response,omitempty"`
}

// instanceResourceName builds the full API resource name for an instance.
func instanceResourceName(project, location, instanceID string) string {
	return "projects/" + project + "/locations/" + location + "/instances/" + instanceID
}

// operationResourceName builds the full API resource name for an operation.
func operationResourceName(project, location, operationID string) string {
	return "projects/" + project + "/locations/" + location + "/operations/" + operationID
}

// shortInstanceID recovers the driver's map key (the short instance id) from a
// CacheInfo.Name. The Memorystore driver stamps info.Name as
// "projects/{p}/instances/{id}" (its own resource id, without a location
// segment) but keys the store on the bare {id}; that trailing segment is the
// key. Names without a "/" are returned unchanged.
func shortInstanceID(name string) string {
	if i := strings.LastIndex(name, "/"); i >= 0 {
		return name[i+1:]
	}

	return name
}

// hostAndPort splits the driver's "host:port" endpoint into a host and a numeric
// port, defaulting to the standard Redis port when no ":port" suffix is present.
func hostAndPort(endpoint string) (string, int64) {
	if endpoint == "" {
		return "", defaultRedisPort
	}

	host := endpoint
	port := int64(defaultRedisPort)

	if i := strings.LastIndex(endpoint, ":"); i >= 0 {
		host = endpoint[:i]
		if p, err := strconv.ParseInt(endpoint[i+1:], 10, 64); err == nil {
			port = p
		}
	}

	return host, port
}

// toInstanceJSON converts a driver CacheInfo into its redis/v1 Instance shape.
// The wire `name` must be the full locations-scoped resource path, which the
// driver's CacheInfo.Name (which lacks the location segment) does not carry, so
// it is rebuilt from the request scope and the short instance id.
func toInstanceJSON(project, location, instanceID string, info *cachedriver.CacheInfo) instanceJSON {
	host, port := hostAndPort(info.Endpoint)

	tier := defaultTier
	if info.NodeType != "" {
		tier = info.NodeType
	}

	return instanceJSON{
		Name:         instanceResourceName(project, location, instanceID),
		Tier:         tier,
		MemorySizeGb: 1,
		RedisVersion: defaultRedisVersion,
		State:        stateOrReady(info.Status),
		Host:         host,
		Port:         port,
		CreateTime:   info.CreatedAt,
		Labels:       info.Tags,
		LocationID:   location,
	}
}

// stateOrReady maps the driver status onto Memorystore's state enum, defaulting
// to READY.
func stateOrReady(status string) string {
	if strings.EqualFold(status, "READY") || status == "" {
		return stateReady
	}

	return status
}

// doneOperation builds a completed google.longrunning.Operation for the given
// operation id. When resp is non-nil it is embedded as the operation response.
func doneOperation(project, location, operationID string, resp json.RawMessage) operationJSON {
	return operationJSON{
		Name:     operationResourceName(project, location, operationID),
		Done:     true,
		Response: resp,
	}
}
