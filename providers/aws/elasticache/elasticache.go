// Package elasticache provides an in-memory mock implementation of AWS ElastiCache.
package elasticache

import (
	"context"
	"fmt"
	"maps"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/internal/regionctx"
	cacheengine "github.com/stackshy/cloudemu/v2/services/cache/cacheengine"
	"github.com/stackshy/cloudemu/v2/services/cache/driver"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

const (
	defaultRedisPort     = 6379
	defaultMemcachedPort = 11211
)

// resolvePort returns the requested port, or the engine default when unset:
// Memcached listens on 11211, Redis/Valkey on 6379.
func resolvePort(engine string, port int) int {
	if port != 0 {
		return port
	}

	if engine == engineMemcached {
		return defaultMemcachedPort
	}

	return defaultRedisPort
}

// clusterEndpoint builds the synthetic host:port a client connects to. Memcached
// exposes a single configuration endpoint whose host carries the ".cfg" segment
// real AWS uses; Redis/Valkey use a plain per-cluster host.
func clusterEndpoint(name, region, engine string, port int) string {
	if engine == engineMemcached {
		return fmt.Sprintf("%s.cfg.%s.cache.amazonaws.com:%d", name, region, port)
	}

	return fmt.Sprintf("%s.%s.cache.amazonaws.com:%d", name, region, port)
}

// Compile-time check that Mock implements driver.Cache.
var _ driver.Cache = (*Mock)(nil)

type cacheItem struct {
	Value     []byte
	ExpiresAt time.Time
	HasTTL    bool
}

type cacheData struct {
	info  driver.CacheInfo
	items *memstore.Store[cacheItem]
}

// Defaults applied when a caller omits them, shared by cache clusters and
// replication groups so the two cannot drift apart.
const (
	defaultEngine   = "redis"
	engineMemcached = "memcached"
	defaultNodeType = "cache.t3.micro"
	statusAvailable = "available"

	defaultRedisVersion     = "7.1"
	defaultMemcachedVersion = "1.6.22"

	// maxMemcachedNodes is the ElastiCache ceiling on Memcached nodes per cluster;
	// Redis/Valkey clusters must have exactly one node.
	maxMemcachedNodes = 40
)

// validateNodeCount enforces the per-engine NumCacheNodes limits real
// ElastiCache applies: Memcached allows 1-40 nodes, while Redis/Valkey clusters
// must have exactly 1 (a larger count is InvalidParameterValue, not silently
// accepted).
func validateNodeCount(engine string, numNodes int) error {
	if engine == engineMemcached {
		if numNodes > maxMemcachedNodes {
			return errors.Newf(errors.InvalidArgument,
				"NumCacheNodes must be between 1 and %d for Memcached", maxMemcachedNodes)
		}

		return nil
	}

	if numNodes > 1 {
		return errors.Newf(errors.InvalidArgument,
			"NumCacheNodes must be 1 for engine %q", engine)
	}

	return nil
}

// normalizeNodeCount defaults an unset node count to 1 and validates it against
// the engine's limits.
func normalizeNodeCount(engine string, requested int) (int, error) {
	n := requested
	if n < 1 {
		n = 1
	}

	if err := validateNodeCount(engine, n); err != nil {
		return 0, err
	}

	return n, nil
}

// requireSubnetGroup rejects a create that names a cache subnet group which does
// not exist, matching real ElastiCache (CacheSubnetGroupNotFoundFault) rather
// than silently accepting a typo (mirrors RDS CreateDBInstance's DBSubnetGroup
// check). An empty name places the cluster in the default subnet group.
func (m *Mock) requireSubnetGroup(name string) error {
	if name != "" && !m.subnetGroups.Has(name) {
		return errors.Newf(errors.NotFound,
			"CacheSubnetGroupNotFoundFault: cache subnet group %q not found", name)
	}

	return nil
}

// cacheARN builds an ElastiCache cluster ARN in the given region.
func (m *Mock) cacheARN(region, name string) string {
	return "arn:aws:elasticache:" + region + ":" + m.opts.AccountID + ":cluster:" + name
}

// arnRegion returns the region field of an ElastiCache ARN
// (arn:aws:elasticache:<region>:<account>:<type>:<name>), or fallback when the
// ARN is malformed. A replication group's stored ARN is the source of truth for
// the region of a cache cluster retained from it.
func arnRegion(arn, fallback string) string {
	const regionField, minFields = 3, 6

	parts := strings.Split(arn, ":")
	if len(parts) < minFields || parts[regionField] == "" {
		return fallback
	}

	return parts[regionField]
}

// defaultEngineVersion returns the ElastiCache default engine version for an
// engine, matching what real ElastiCache assigns when the caller omits it.
func defaultEngineVersion(engine string) string {
	if engine == engineMemcached {
		return defaultMemcachedVersion
	}

	return defaultRedisVersion
}

// Mock is an in-memory mock implementation of the AWS ElastiCache service.
type Mock struct {
	caches            *memstore.Store[*cacheData]
	subnetGroups      *memstore.Store[driver.SubnetGroup]
	replicationGroups *memstore.Store[driver.ReplicationGroup]
	parameterGroups   *memstore.Store[ParameterGroup]
	snapshots         *memstore.Store[driver.Snapshot]
	subnetResolver    SubnetResolver
	opts              *config.Options
	monitoring        mondriver.Monitoring

	tagMu     sync.Mutex
	tagsByARN map[string]map[string]string
}

// ParameterGroup is an ElastiCache cache parameter group — a named, engine-family
// set of engine parameters. The emulator stores its identity plus any user
// overrides (name→value) applied via ModifyCacheParameterGroup, so IaC that
// creates a group, sets `parameter { … }` blocks, and reads them back on refresh
// converges. The engine-family defaults themselves are synthesized on demand
// (see defaultCacheParameters); Overrides holds only the parameters the user has
// changed from their default.
type ParameterGroup struct {
	Name        string
	Family      string
	Description string
	Overrides   map[string]string
}

// SetMonitoring sets the monitoring backend for auto-metric generation.
func (m *Mock) SetMonitoring(mon mondriver.Monitoring) {
	m.monitoring = mon
}

//nolint:unparam // value is always 1 today but kept for future metrics like evictions or replication lag.
func (m *Mock) emitMetric(metricName string, value float64, dims map[string]string) {
	if m.monitoring == nil {
		return
	}

	_ = m.monitoring.PutMetricData(context.Background(), []mondriver.MetricDatum{{
		Namespace: "AWS/ElastiCache", MetricName: metricName, Value: value, Unit: "Count",
		Dimensions: dims, Timestamp: m.opts.Clock.Now(),
	}})
}

// New creates a new ElastiCache mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		caches:            memstore.New[*cacheData](),
		subnetGroups:      memstore.New[driver.SubnetGroup](),
		replicationGroups: memstore.New[driver.ReplicationGroup](),
		parameterGroups:   memstore.New[ParameterGroup](),
		snapshots:         memstore.New[driver.Snapshot](),
		tagsByARN:         make(map[string]map[string]string),
		opts:              opts,
	}
}

// CreateCache creates a new ElastiCache cluster.
func (m *Mock) CreateCache(ctx context.Context, cfg driver.CacheConfig) (*driver.CacheInfo, error) {
	if cfg.Name == "" {
		return nil, errors.New(errors.InvalidArgument, "cache name is required")
	}

	if m.caches.Has(cfg.Name) {
		return nil, errors.Newf(errors.AlreadyExists, "cache %q already exists", cfg.Name)
	}

	engine := cfg.Engine
	if engine == "" {
		engine = defaultEngine
	}

	nodeType := cfg.NodeType
	if nodeType == "" {
		nodeType = defaultNodeType
	}

	engineVersion := cfg.EngineVersion
	if engineVersion == "" {
		engineVersion = defaultEngineVersion(engine)
	}

	numNodes, err := normalizeNodeCount(engine, cfg.NumCacheNodes)
	if err != nil {
		return nil, err
	}

	if err := m.requireSubnetGroup(cfg.SubnetGroupName); err != nil {
		return nil, err
	}

	region := regionctx.RegionOr(ctx, m.opts.Region)
	endpoint := clusterEndpoint(cfg.Name, region, engine, resolvePort(engine, cfg.Port))

	tags := make(map[string]string, len(cfg.Tags))
	for k, v := range cfg.Tags {
		tags[k] = v
	}

	info := driver.CacheInfo{
		Name:               cfg.Name,
		Scope:              cfg.Scope,
		NodeType:           nodeType,
		Engine:             engine,
		EngineVersion:      engineVersion,
		Status:             statusAvailable,
		Endpoint:           endpoint,
		ARN:                m.cacheARN(region, cfg.Name),
		CreatedAt:          m.opts.Clock.Now().UTC().Format(time.RFC3339),
		Tags:               tags,
		NumCacheNodes:      numNodes,
		SubnetGroupName:    cfg.SubnetGroupName,
		ParameterGroupName: cfg.ParameterGroupName,
	}

	// Opt-in: back the cache with a real server, replacing the synthetic
	// endpoint with the real host:port a client connects to.
	if err := cacheengine.Provision(ctx, m.opts.CacheEngine, &info); err != nil {
		return nil, err
	}

	cd := &cacheData{
		info:  info,
		items: memstore.New[cacheItem](),
	}

	m.caches.Set(cfg.Name, cd)
	m.seedTags(info.ARN, tags)

	result := info

	return &result, nil
}

// ModifyCache updates the mutable fields (node type, engine version, node
// count) of an existing cache cluster (ElastiCache ModifyCacheCluster). Empty or
// zero fields leave the corresponding attribute unchanged. A NumCacheNodes
// change is re-validated against the cluster's engine (Memcached scales 1-40;
// Redis/Valkey stays at 1).
func (m *Mock) ModifyCache(_ context.Context, cfg driver.ModifyCacheConfig) (*driver.CacheInfo, error) {
	cd, ok := m.caches.Get(cfg.Name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "cache %q not found", cfg.Name)
	}

	if cfg.NumCacheNodes > 0 {
		if err := validateNodeCount(cd.info.Engine, cfg.NumCacheNodes); err != nil {
			return nil, err
		}
	}

	if cfg.NodeType != "" {
		cd.info.NodeType = cfg.NodeType
	}

	if cfg.EngineVersion != "" {
		cd.info.EngineVersion = cfg.EngineVersion
	}

	if cfg.NumCacheNodes > 0 {
		cd.info.NumCacheNodes = cfg.NumCacheNodes
	}

	m.caches.Set(cfg.Name, cd)

	result := cd.info

	return &result, nil
}

// RebootCache reboots a cache cluster (ElastiCache RebootCacheCluster), the
// path real deployments use to apply pending parameter-group changes. The
// cluster stays available in the emulator; a reboot metric is emitted to mirror
// the lifecycle event.
func (m *Mock) RebootCache(_ context.Context, name string) (*driver.CacheInfo, error) {
	cd, ok := m.caches.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "cache %q not found", name)
	}

	m.emitMetric("Reboots", 1, map[string]string{"CacheClusterId": name})

	result := cd.info

	return &result, nil
}

// DeleteCache deletes an ElastiCache cluster by name.
func (m *Mock) DeleteCache(ctx context.Context, name string) error {
	cd, ok := m.caches.Get(name)
	if !ok {
		return errors.Newf(errors.NotFound, "cache %q not found", name)
	}

	// Tear down the real cache server backing the instance, if any.
	if err := cacheengine.Deprovision(ctx, m.opts.CacheEngine, &cd.info); err != nil {
		return err
	}

	m.caches.Delete(name)

	return nil
}

// GetCache retrieves information about an ElastiCache cluster.
func (m *Mock) GetCache(_ context.Context, name string) (*driver.CacheInfo, error) {
	cd, ok := m.caches.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "cache %q not found", name)
	}

	result := cd.info

	return &result, nil
}

// ListCaches lists all ElastiCache clusters.
func (m *Mock) ListCaches(_ context.Context, filter scope.Scope) ([]driver.CacheInfo, error) {
	all := m.caches.SortedValues()

	caches := make([]driver.CacheInfo, 0, len(all))
	for _, cd := range all {
		if !cd.info.Scope.Matches(filter) {
			continue
		}
		caches = append(caches, cd.info)
	}

	return caches, nil
}

// UpdateCache replaces the mutable fields of an existing cache — ARM
// CreateOrUpdate-on-existing semantics (node type and tags come from the
// request; identity, endpoint, and CreatedAt are preserved).
func (m *Mock) UpdateCache(_ context.Context, cfg driver.CacheConfig) (*driver.CacheInfo, error) {
	cd, ok := m.caches.Get(cfg.Name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "cache %q not found", cfg.Name)
	}

	if cfg.NodeType != "" {
		cd.info.NodeType = cfg.NodeType
	}
	if cfg.Tags != nil {
		cd.info.Tags = maps.Clone(cfg.Tags)
	}
	if !cfg.Scope.IsZero() {
		cd.info.Scope = cfg.Scope
	}

	m.caches.Set(cfg.Name, cd)

	result := cd.info

	return &result, nil
}

// Set stores a value in the cache with an optional TTL.
func (m *Mock) Set(_ context.Context, cacheName, key string, value []byte, ttl time.Duration) error {
	cd, ok := m.caches.Get(cacheName)
	if !ok {
		return errors.Newf(errors.NotFound, "cache %q not found", cacheName)
	}

	data := make([]byte, len(value))
	copy(data, value)

	item := cacheItem{
		Value: data,
	}

	if ttl > 0 {
		item.ExpiresAt = m.opts.Clock.Now().Add(ttl)
		item.HasTTL = true
	}

	cd.items.Set(key, item)

	m.emitMetric("SetCommands", 1, map[string]string{"CacheClusterId": cacheName})

	return nil
}

// Get retrieves a value from the cache.
func (m *Mock) Get(_ context.Context, cacheName, key string) (*driver.Item, error) {
	cd, ok := m.caches.Get(cacheName)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "cache %q not found", cacheName)
	}

	dims := map[string]string{"CacheClusterId": cacheName}

	item, ok := cd.items.Get(key)
	if !ok {
		m.emitMetric("CacheMisses", 1, dims)
		m.emitMetric("GetCommands", 1, dims)

		return nil, errors.Newf(errors.NotFound, "key %q not found in cache %q", key, cacheName)
	}

	// Check TTL expiry.
	now := m.opts.Clock.Now()
	if item.HasTTL && now.After(item.ExpiresAt) {
		cd.items.Delete(key)
		m.emitMetric("CacheMisses", 1, dims)
		m.emitMetric("GetCommands", 1, dims)

		return nil, errors.Newf(errors.NotFound, "key %q not found in cache %q", key, cacheName)
	}

	m.emitMetric("CacheHits", 1, dims)
	m.emitMetric("GetCommands", 1, dims)

	data := make([]byte, len(item.Value))
	copy(data, item.Value)

	result := &driver.Item{
		Key:   key,
		Value: data,
	}

	if item.HasTTL {
		result.TTL = item.ExpiresAt.Sub(now)
		result.ExpiresAt = item.ExpiresAt
	}

	return result, nil
}

// Delete removes a value from the cache.
func (m *Mock) Delete(_ context.Context, cacheName, key string) error {
	cd, ok := m.caches.Get(cacheName)
	if !ok {
		return errors.Newf(errors.NotFound, "cache %q not found", cacheName)
	}

	if !cd.items.Delete(key) {
		return errors.Newf(errors.NotFound, "key %q not found in cache %q", key, cacheName)
	}

	m.emitMetric("DeleteCommands", 1, map[string]string{"CacheClusterId": cacheName})

	return nil
}

// Keys returns all keys matching the given pattern in the cache.
// Supports "*" as a wildcard. Empty pattern returns all keys.
func (m *Mock) Keys(_ context.Context, cacheName, pattern string) ([]string, error) {
	cd, ok := m.caches.Get(cacheName)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "cache %q not found", cacheName)
	}

	now := m.opts.Clock.Now()
	allKeys := cd.items.Keys()

	var matched []string

	for _, key := range allKeys {
		item, ok := cd.items.Get(key)
		if !ok {
			continue
		}

		// Skip expired keys.
		if item.HasTTL && now.After(item.ExpiresAt) {
			cd.items.Delete(key)

			continue
		}

		if matchPattern(pattern, key) {
			matched = append(matched, key)
		}
	}

	if matched == nil {
		matched = []string{}
	}

	return matched, nil
}

// FlushAll removes all items from the cache.
func (m *Mock) FlushAll(_ context.Context, cacheName string) error {
	cd, ok := m.caches.Get(cacheName)
	if !ok {
		return errors.Newf(errors.NotFound, "cache %q not found", cacheName)
	}

	cd.items.Clear()

	return nil
}

// Expire sets a TTL on an existing key.
func (m *Mock) Expire(_ context.Context, cacheName, key string, ttl time.Duration) error {
	cd, ok := m.caches.Get(cacheName)
	if !ok {
		return errors.Newf(errors.NotFound, "cache %q not found", cacheName)
	}

	item, ok := cd.items.Get(key)
	if !ok || (item.HasTTL && m.opts.Clock.Now().After(item.ExpiresAt)) {
		return errors.Newf(errors.NotFound, "key %q not found in cache %q", key, cacheName)
	}

	item.HasTTL = true
	item.ExpiresAt = m.opts.Clock.Now().Add(ttl)
	cd.items.Set(key, item)

	return nil
}

// GetTTL returns the remaining TTL for a key. Returns -1 if the key has no TTL.
func (m *Mock) GetTTL(_ context.Context, cacheName, key string) (time.Duration, error) {
	cd, ok := m.caches.Get(cacheName)
	if !ok {
		return 0, errors.Newf(errors.NotFound, "cache %q not found", cacheName)
	}

	item, ok := cd.items.Get(key)
	if !ok || (item.HasTTL && m.opts.Clock.Now().After(item.ExpiresAt)) {
		return 0, errors.Newf(errors.NotFound, "key %q not found in cache %q", key, cacheName)
	}

	if !item.HasTTL {
		return -1, nil
	}

	return item.ExpiresAt.Sub(m.opts.Clock.Now()), nil
}

// Persist removes the TTL from a key, making it persistent.
func (m *Mock) Persist(_ context.Context, cacheName, key string) error {
	cd, ok := m.caches.Get(cacheName)
	if !ok {
		return errors.Newf(errors.NotFound, "cache %q not found", cacheName)
	}

	item, ok := cd.items.Get(key)
	if !ok || (item.HasTTL && m.opts.Clock.Now().After(item.ExpiresAt)) {
		return errors.Newf(errors.NotFound, "key %q not found in cache %q", key, cacheName)
	}

	item.HasTTL = false
	item.ExpiresAt = time.Time{}
	cd.items.Set(key, item)

	return nil
}

// Incr atomically increments the integer value of a key by 1.
func (m *Mock) Incr(ctx context.Context, cacheName, key string) (int64, error) {
	return m.IncrBy(ctx, cacheName, key, 1)
}

// IncrBy atomically increments the integer value of a key by delta.
func (m *Mock) IncrBy(_ context.Context, cacheName, key string, delta int64) (int64, error) {
	cd, ok := m.caches.Get(cacheName)
	if !ok {
		return 0, errors.Newf(errors.NotFound, "cache %q not found", cacheName)
	}

	newVal, err := applyDelta(cd, key, delta, m.opts.Clock.Now())
	if err != nil {
		return 0, err
	}

	m.emitMetric("IncrCommands", 1, map[string]string{"CacheClusterId": cacheName})

	return newVal, nil
}

// Decr atomically decrements the integer value of a key by 1.
func (m *Mock) Decr(ctx context.Context, cacheName, key string) (int64, error) {
	return m.DecrBy(ctx, cacheName, key, 1)
}

// DecrBy atomically decrements the integer value of a key by delta.
func (m *Mock) DecrBy(_ context.Context, cacheName, key string, delta int64) (int64, error) {
	cd, ok := m.caches.Get(cacheName)
	if !ok {
		return 0, errors.Newf(errors.NotFound, "cache %q not found", cacheName)
	}

	newVal, err := applyDelta(cd, key, -delta, m.opts.Clock.Now())
	if err != nil {
		return 0, err
	}

	m.emitMetric("DecrCommands", 1, map[string]string{"CacheClusterId": cacheName})

	return newVal, nil
}

func applyDelta(cd *cacheData, key string, delta int64, now time.Time) (int64, error) {
	item, ok := cd.items.Get(key)

	var current int64

	if ok && (!item.HasTTL || !now.After(item.ExpiresAt)) {
		val, err := strconv.ParseInt(string(item.Value), 10, 64)
		if err != nil {
			return 0, errors.New(errors.InvalidArgument, "value is not an integer")
		}

		current = val
	}

	newVal := current + delta
	newItem := cacheItem{
		Value: []byte(strconv.FormatInt(newVal, 10)),
	}

	if ok && item.HasTTL && !now.After(item.ExpiresAt) {
		newItem.HasTTL = true
		newItem.ExpiresAt = item.ExpiresAt
	}

	cd.items.Set(key, newItem)

	return newVal, nil
}

// matchPattern matches a key against a glob-like pattern.
// Supports full glob syntax including middle wildcards like "user:*:session".
func matchPattern(pattern, key string) bool {
	if pattern == "" || pattern == "*" {
		return true
	}

	matched, err := path.Match(pattern, key)
	if err != nil {
		return false
	}

	return matched
}
