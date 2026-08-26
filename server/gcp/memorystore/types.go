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
	defaultConnectMode  = "DIRECT_PEERING"
	defaultNetwork      = "default"
	defaultZoneSuffix   = "-a"
	defaultMemorySizeGb = 1
	// readReplicasEnabled is the readReplicasMode that makes Memorystore expose a
	// read endpoint (independent of tier).
	readReplicasEnabled = "READ_REPLICAS_ENABLED"
	// reservedPrefix marks tag keys that carry cloudemu-internal fields, not user
	// labels.
	reservedPrefix = "cloudemu:"
	// stateReady is Memorystore's terminal instance state; the mock provisions
	// synchronously so every instance reports READY.
	stateReady = "READY"
)

// instanceJSON mirrors the subset of google.golang.org/api/redis/v1 Instance
// the cache driver can populate. `name` is the full resource path
// (projects/{p}/locations/{l}/instances/{i}); `memorySizeGb` is required by the
// API surface so a stub default is emitted.
type instanceJSON struct {
	Name              string            `json:"name,omitempty"`
	DisplayName       string            `json:"displayName,omitempty"`
	Tier              string            `json:"tier,omitempty"`
	MemorySizeGb      int64             `json:"memorySizeGb,omitempty"`
	RedisVersion      string            `json:"redisVersion,omitempty"`
	State             string            `json:"state,omitempty"`
	Host              string            `json:"host,omitempty"`
	Port              int64             `json:"port,omitempty"`
	CreateTime        string            `json:"createTime,omitempty"`
	Labels            map[string]string `json:"labels,omitempty"`
	LocationID        string            `json:"locationId,omitempty"`
	CurrentLocationID string            `json:"currentLocationId,omitempty"`
	ReservedIPRng     string            `json:"reservedIpRange,omitempty"`
	AuthorizedNetwork string            `json:"authorizedNetwork,omitempty"`
	ConnectMode       string            `json:"connectMode,omitempty"`
	ReadEndpoint      string            `json:"readEndpoint,omitempty"`
	ReadEndpointPort  int64             `json:"readEndpointPort,omitempty"`
	PersistenceIAMID  string            `json:"persistenceIamIdentity,omitempty"`
	RedisConfigs      map[string]string `json:"redisConfigs,omitempty"`
	AuthEnabled       bool              `json:"authEnabled,omitempty"`
	TransitEncryption string            `json:"transitEncryptionMode,omitempty"`
	ReplicaCount      int64             `json:"replicaCount,omitempty"`
	ReadReplicasMode  string            `json:"readReplicasMode,omitempty"`
}

// listInstancesResponse mirrors redis/v1 ListInstancesResponse.
type listInstancesResponse struct {
	Instances     []instanceJSON `json:"instances"`
	NextPageToken string         `json:"nextPageToken,omitempty"`
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

	zone := zoneForLocation(location, info.Tags[locationIDTag])

	inst := instanceJSON{
		Name:              instanceResourceName(project, location, instanceID),
		DisplayName:       info.Tags[displayNameTag],
		Tier:              tier,
		MemorySizeGb:      memorySizeFromTags(info.Tags),
		RedisVersion:      redisVersionFromTags(info.Tags),
		State:             stateOrReady(info.Status),
		Host:              host,
		Port:              port,
		CreateTime:        info.CreatedAt,
		Labels:            stripReservedTags(info.Tags),
		LocationID:        zone,
		CurrentLocationID: zone,
		ReservedIPRng:     info.Tags[reservedIPTag],
		AuthorizedNetwork: authorizedNetworkOrDefault(project, info.Tags[authorizedNetworkTag]),
		ConnectMode:       connectModeOrDefault(info.Tags[connectModeTag]),
		PersistenceIAMID:  persistenceIAMIdentity(project),
		RedisConfigs:      redisConfigsFromTags(info.Tags),
		AuthEnabled:       boolFromTag(info.Tags[authEnabledTag]),
		TransitEncryption: info.Tags[transitEncryptionTag],
		ReplicaCount:      intFromTag(info.Tags[replicaCountTag]),
		ReadReplicasMode:  info.Tags[readReplicasModeTag],
	}

	// The read endpoint is provided only when read replicas are enabled (matching
	// real Memorystore); tier alone does not expose it.
	if strings.EqualFold(inst.ReadReplicasMode, readReplicasEnabled) {
		inst.ReadEndpoint = readReplicaHost(host)
		inst.ReadEndpointPort = port
	}

	return inst
}

// memorySizeFromTags reads the round-tripped memorySizeGb, defaulting to the
// stub the API surface requires when unset.
func memorySizeFromTags(tags map[string]string) int64 {
	if v, err := strconv.ParseInt(tags[memorySizeTag], 10, 64); err == nil && v > 0 {
		return v
	}

	return defaultMemorySizeGb
}

// redisVersionFromTags reads the round-tripped redisVersion, defaulting to the
// stub version when unset.
func redisVersionFromTags(tags map[string]string) string {
	if v := tags[redisVersionTag]; v != "" {
		return v
	}

	return defaultRedisVersion
}

// redisConfigsFromTags decodes the JSON-encoded redisConfigs map round-tripped
// through the reserved tag; a missing or malformed value yields no configs.
func redisConfigsFromTags(tags map[string]string) map[string]string {
	raw := tags[redisConfigsTag]
	if raw == "" {
		return nil
	}

	var cfg map[string]string
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return nil
	}

	return cfg
}

// boolFromTag parses a boolean reserved tag, treating any unparseable value as
// false.
func boolFromTag(v string) bool {
	b, _ := strconv.ParseBool(v)
	return b
}

// intFromTag parses an integer reserved tag, treating any unparseable value as
// zero.
func intFromTag(v string) int64 {
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0
	}

	return n
}

// zoneForLocation resolves the instance's zone. Real Memorystore reports a zone
// (e.g. "us-central1-a") in locationId/currentLocationId, distinct from the
// region in the resource path; a create-time override round-trips, otherwise the
// region's first zone is used.
func zoneForLocation(region, override string) string {
	if override != "" {
		return override
	}

	return region + defaultZoneSuffix
}

// authorizedNetworkOrDefault normalizes the instance's authorized VPC network to
// the full projects/{p}/global/networks/{n} form real Memorystore returns,
// defaulting to the "default" network when unspecified.
func authorizedNetworkOrDefault(project, network string) string {
	if network == "" {
		network = defaultNetwork
	}

	if strings.Contains(network, "/") {
		return network
	}

	return "projects/" + project + "/global/networks/" + network
}

// connectModeOrDefault defaults the connect mode to DIRECT_PEERING, as real
// Memorystore does when the field is omitted at create time.
func connectModeOrDefault(mode string) string {
	if mode == "" {
		return defaultConnectMode
	}

	return mode
}

// persistenceIAMIdentity returns the output-only service-account identity
// Memorystore uses for RDB persistence to Cloud Storage.
func persistenceIAMIdentity(project string) string {
	return "serviceAccount:" + project + "@cloud-redis.iam.gserviceaccount.com"
}

// readReplicaHost derives a distinct read-endpoint IP from the primary host by
// advancing the final octet, so a Standard-tier read endpoint differs from the
// primary host (as real Memorystore reports).
func readReplicaHost(host string) string {
	i := strings.LastIndex(host, ".")
	if i < 0 {
		return host
	}

	n, err := strconv.Atoi(host[i+1:])
	if err != nil {
		return host
	}

	return host[:i+1] + strconv.Itoa(n+1)
}

// Reserved tag keys carry GCP-specific fields the cache driver can't model, so
// they round-trip through the cache's tags.
const (
	memorySizeTag        = "cloudemu:gcpMemorySizeGb"
	redisVersionTag      = "cloudemu:gcpRedisVersion"
	displayNameTag       = "cloudemu:gcpDisplayName"
	reservedIPTag        = "cloudemu:gcpReservedIpRange"
	authorizedNetworkTag = "cloudemu:gcpAuthorizedNetwork"
	connectModeTag       = "cloudemu:gcpConnectMode"
	locationIDTag        = "cloudemu:gcpLocationId"
	redisConfigsTag      = "cloudemu:gcpRedisConfigs"
	authEnabledTag       = "cloudemu:gcpAuthEnabled"
	transitEncryptionTag = "cloudemu:gcpTransitEncryptionMode"
	replicaCountTag      = "cloudemu:gcpReplicaCount"
	readReplicasModeTag  = "cloudemu:gcpReadReplicasMode"
)

// fieldMask is the set of instance fields a PATCH updateMask selects. A nil mask
// (Create, or a PATCH with no updateMask) applies every field the body provides.
type fieldMask map[string]bool

// parseFieldMask parses an updateMask query value into a set of field paths. An
// empty value yields a nil mask, meaning "apply all provided fields".
func parseFieldMask(raw string) fieldMask {
	if strings.TrimSpace(raw) == "" {
		return nil
	}

	m := make(fieldMask)

	for _, f := range strings.Split(raw, ",") {
		if f = strings.TrimSpace(f); f != "" {
			m[f] = true
		}
	}

	return m
}

// has reports whether field is selected. A nil mask selects every field.
func (m fieldMask) has(field string) bool {
	return m == nil || m[field]
}

// stringTagField pairs an updateMask field path with the reserved tag key and
// request value that back it, so simple string fields fold in via one loop.
type stringTagField struct {
	field string
	tag   string
	value string
}

// instanceTags folds the GCP-specific request fields into the tag map. Only
// masked fields are applied, so a PATCH leaves fields outside its updateMask
// untouched; labels and redisConfigs are whole-replaced when masked so a removed
// entry disappears rather than lingering.
func instanceTags(body *instanceJSON, existing map[string]string, mask fieldMask) map[string]string {
	out := carryForwardTags(existing, mask)

	applyLabels(out, body, mask)
	applyStringTags(out, body, mask)
	applyScalarTags(out, body, mask)
	applyRedisConfigs(out, body, mask)

	return out
}

// carryForwardTags copies existing tags forward, dropping user labels when the
// mask replaces the whole label set (reserved keys always carry forward).
func carryForwardTags(existing map[string]string, mask fieldMask) map[string]string {
	out := make(map[string]string, len(existing)+1)
	dropLabels := mask.has("labels")

	for k, v := range existing {
		if dropLabels && !strings.HasPrefix(k, reservedPrefix) {
			continue
		}

		out[k] = v
	}

	return out
}

// applyLabels writes the request's labels as the whole label set when masked.
func applyLabels(out map[string]string, body *instanceJSON, mask fieldMask) {
	if !mask.has("labels") {
		return
	}

	for k, v := range body.Labels {
		out[k] = v
	}
}

// applyStringTags folds the simple string fields in, honoring the mask.
func applyStringTags(out map[string]string, body *instanceJSON, mask fieldMask) {
	for _, f := range stringTagFields(body) {
		if mask.has(f.field) && f.value != "" {
			out[f.tag] = f.value
		}
	}
}

func stringTagFields(body *instanceJSON) []stringTagField {
	return []stringTagField{
		{"redisVersion", redisVersionTag, body.RedisVersion},
		{"displayName", displayNameTag, body.DisplayName},
		{"reservedIpRange", reservedIPTag, body.ReservedIPRng},
		{"authorizedNetwork", authorizedNetworkTag, body.AuthorizedNetwork},
		{"connectMode", connectModeTag, body.ConnectMode},
		{"locationId", locationIDTag, body.LocationID},
		{"transitEncryptionMode", transitEncryptionTag, body.TransitEncryption},
		{"readReplicasMode", readReplicasModeTag, body.ReadReplicasMode},
	}
}

// applyScalarTags folds the numeric and boolean fields in, honoring the mask.
func applyScalarTags(out map[string]string, body *instanceJSON, mask fieldMask) {
	if mask.has("memorySizeGb") && body.MemorySizeGb > 0 {
		out[memorySizeTag] = strconv.FormatInt(body.MemorySizeGb, 10)
	}

	if mask.has("replicaCount") && body.ReplicaCount > 0 {
		out[replicaCountTag] = strconv.FormatInt(body.ReplicaCount, 10)
	}

	if mask.has("authEnabled") {
		setBoolTag(out, authEnabledTag, body.AuthEnabled)
	}
}

// setBoolTag records a true boolean and clears the tag when false.
func setBoolTag(out map[string]string, key string, val bool) {
	if val {
		out[key] = "true"

		return
	}

	delete(out, key)
}

// applyRedisConfigs whole-replaces the JSON-encoded redisConfigs map when masked
// (an empty map clears it), matching real Memorystore's replace semantics.
func applyRedisConfigs(out map[string]string, body *instanceJSON, mask fieldMask) {
	if !mask.has("redisConfigs") {
		return
	}

	delete(out, redisConfigsTag)

	if len(body.RedisConfigs) == 0 {
		return
	}

	if raw, err := json.Marshal(body.RedisConfigs); err == nil {
		out[redisConfigsTag] = string(raw)
	}
}

// stripReservedTags returns user labels without cloudemu-internal keys.
func stripReservedTags(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}

	out := make(map[string]string, len(in))

	for k, v := range in {
		if strings.HasPrefix(k, reservedPrefix) {
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
// operation id and records it with the shared LRO poller so a client polling the
// returned name resolves the same done operation (with its response) in the full
// server. When resp is non-nil it is embedded as the operation response.
func (h *Handler) doneOperation(project, location, operationID string, resp json.RawMessage) operationJSON {
	name := operationResourceName(project, location, operationID)
	h.ops.Register(name, resp)

	return operationJSON{
		Name:     name,
		Done:     true,
		Response: resp,
	}
}
