package memorystore

import (
	"encoding/json"
	"strconv"
	"strings"

	cachedriver "github.com/stackshy/cloudemu/v2/services/cache/driver"
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

	memSize := int64(1)
	if v, err := strconv.ParseInt(info.Tags[memorySizeTag], 10, 64); err == nil && v > 0 {
		memSize = v
	}

	redisVersion := defaultRedisVersion
	if v := info.Tags[redisVersionTag]; v != "" {
		redisVersion = v
	}

	return instanceJSON{
		Name:          instanceResourceName(project, location, instanceID),
		DisplayName:   info.Tags[displayNameTag],
		Tier:          tier,
		MemorySizeGb:  memSize,
		RedisVersion:  redisVersion,
		State:         stateOrReady(info.Status),
		Host:          host,
		Port:          port,
		CreateTime:    info.CreatedAt,
		Labels:        stripReservedTags(info.Tags),
		LocationID:    location,
		ReservedIPRng: info.Tags[reservedIPTag],
	}
}

// Reserved tag keys carry GCP-specific fields the cache driver can't model, so
// they round-trip through the cache's tags.
const (
	memorySizeTag   = "cloudemu:gcpMemorySizeGb"
	redisVersionTag = "cloudemu:gcpRedisVersion"
	displayNameTag  = "cloudemu:gcpDisplayName"
	reservedIPTag   = "cloudemu:gcpReservedIpRange"
)

// instanceTags folds the GCP-specific request fields into the tag map, layered
// over existing tags so a partial PATCH keeps unspecified values.
func instanceTags(body *instanceJSON, existing map[string]string) map[string]string {
	out := make(map[string]string, len(existing)+len(body.Labels))

	for k, v := range existing {
		out[k] = v
	}

	for k, v := range body.Labels {
		out[k] = v
	}

	if body.MemorySizeGb > 0 {
		out[memorySizeTag] = strconv.FormatInt(body.MemorySizeGb, 10)
	}

	if body.RedisVersion != "" {
		out[redisVersionTag] = body.RedisVersion
	}

	if body.DisplayName != "" {
		out[displayNameTag] = body.DisplayName
	}

	if body.ReservedIPRng != "" {
		out[reservedIPTag] = body.ReservedIPRng
	}

	return out
}

// stripReservedTags returns user labels without cloudemu-internal keys.
func stripReservedTags(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}

	out := make(map[string]string, len(in))

	for k, v := range in {
		if strings.HasPrefix(k, "cloudemu:") {
			continue
		}

		out[k] = v
	}

	if len(out) == 0 {
		return nil
	}

	return out
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
