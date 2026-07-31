// Package memorydb provides an in-memory mock of AWS MemoryDB, a
// Redis/Valkey-compatible managed database. MemoryDB is control-plane only, so
// this mock models clusters (with shards/nodes/endpoints), ACLs, users,
// parameter groups, subnet groups, snapshots and multi-region clusters, and the
// cross-references between them.
package memorydb

import (
	"context"
	"fmt"
	"sync"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	mdbdriver "github.com/stackshy/cloudemu/v2/services/memorydb/driver"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

const (
	defaultPort          = 6379
	defaultEngine        = "redis"
	defaultEngineVersion = "7.1"
	defaultNodeType      = "db.t4g.small"
	metricsNamespace     = "AWS/MemoryDB"
)

var (
	_ mdbdriver.MemoryDB      = (*Mock)(nil)
	_ mdbdriver.MultiRegion   = (*Mock)(nil)
	_ mdbdriver.ReservedNodes = (*Mock)(nil)
)

// Mock is the in-memory AWS MemoryDB implementation.
type Mock struct {
	mu sync.RWMutex

	clusters        *memstore.Store[mdbdriver.Cluster]
	acls            *memstore.Store[mdbdriver.ACL]
	users           *memstore.Store[mdbdriver.User]
	parameterGroups *memstore.Store[mdbdriver.ParameterGroup]
	subnetGroups    *memstore.Store[mdbdriver.SubnetGroup]
	snapshots       *memstore.Store[mdbdriver.Snapshot]
	multiRegion     *memstore.Store[mdbdriver.MultiRegionCluster]
	reservedNodes   *memstore.Store[mdbdriver.ReservedNode]

	// paramOverrides holds user-set parameter values per parameter group name.
	paramOverrides map[string]map[string]string
	// tags holds tag maps keyed by resource ARN.
	tags map[string]map[string]string
	// events is an append-only lifecycle log surfaced by DescribeEvents.
	events []mdbdriver.Event

	opts       *config.Options
	monitoring mondriver.Monitoring
}

// New creates a new MemoryDB mock with the default ACL and parameter group that
// real MemoryDB provisions in every account.
func New(opts *config.Options) *Mock {
	m := &Mock{
		clusters:        memstore.New[mdbdriver.Cluster](),
		acls:            memstore.New[mdbdriver.ACL](),
		users:           memstore.New[mdbdriver.User](),
		parameterGroups: memstore.New[mdbdriver.ParameterGroup](),
		subnetGroups:    memstore.New[mdbdriver.SubnetGroup](),
		snapshots:       memstore.New[mdbdriver.Snapshot](),
		multiRegion:     memstore.New[mdbdriver.MultiRegionCluster](),
		reservedNodes:   memstore.New[mdbdriver.ReservedNode](),
		paramOverrides:  make(map[string]map[string]string),
		tags:            make(map[string]map[string]string),
		opts:            opts,
	}

	// Account-default resources (present in every real MemoryDB account).
	m.acls.Set("open-access", mdbdriver.ACL{
		Name: "open-access", ARN: m.arn("acl", "open-access"),
		Status: mdbdriver.StatusAvailable, MinimumEngineVersion: "6.2",
	})
	m.parameterGroups.Set("default.memorydb-redis7", mdbdriver.ParameterGroup{
		Name: "default.memorydb-redis7", ARN: m.arn("parametergroup", "default.memorydb-redis7"),
		Family: "memorydb_redis7", Description: "Default parameter group for redis7",
	})

	return m
}

// SetMonitoring wires a CloudWatch backend for auto-metric emission.
func (m *Mock) SetMonitoring(mon mondriver.Monitoring) {
	m.monitoring = mon
}

func (m *Mock) arn(resourceType, name string) string {
	return fmt.Sprintf("arn:aws:memorydb:%s:%s:%s/%s", m.opts.Region, m.opts.AccountID, resourceType, name)
}

// emitClusterMetrics pushes representative AWS/MemoryDB datapoints on cluster
// create (mirrors the sibling ElastiCache/Cloud SQL behavior).
func (m *Mock) emitClusterMetrics(clusterName string) {
	if m.monitoring == nil {
		return
	}

	now := m.opts.Clock.Now()
	dims := map[string]string{"ClusterName": clusterName}

	_ = m.monitoring.PutMetricData(context.Background(), []mondriver.MetricDatum{
		{Namespace: metricsNamespace, MetricName: "CPUUtilization", Value: 5, Unit: "Percent", Dimensions: dims, Timestamp: now},
		{Namespace: metricsNamespace, MetricName: "CurrConnections", Value: 1, Unit: "Count", Dimensions: dims, Timestamp: now},
		{Namespace: metricsNamespace, MetricName: "BytesUsedForMemoryDB", Value: 1048576, Unit: "Bytes", Dimensions: dims, Timestamp: now},
		{Namespace: metricsNamespace, MetricName: "DatabaseMemoryUsagePercentage", Value: 2, Unit: "Percent", Dimensions: dims, Timestamp: now},
	})
}

// recordClusterEvent appends a cluster lifecycle event. The caller holds the
// write lock.
func (m *Mock) recordClusterEvent(clusterName, message string) {
	m.events = append(m.events, mdbdriver.Event{
		SourceName: clusterName, SourceType: "cluster", Message: message, Date: m.opts.Clock.Now().UTC(),
	})
}

// describeByName returns all values (when names is empty) or the named ones,
// each cloned, from a store — the shared shape of every Describe* method.
func describeByName[T any](
	store *memstore.Store[T], names []string, clone func(*T) T, notFound func(string) error,
) ([]T, error) {
	if len(names) == 0 {
		all := store.SortedValues()
		out := make([]T, 0, len(all))

		for i := range all {
			out = append(out, clone(&all[i]))
		}

		return out, nil
	}

	out := make([]T, 0, len(names))

	for _, n := range names {
		v, ok := store.Get(n)
		if !ok {
			return nil, notFound(n)
		}

		out = append(out, clone(&v))
	}

	return out, nil
}

// ---- tag helpers ----

func copyTags(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}

	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}

	return out
}

func cloneStrings(s []string) []string {
	if len(s) == 0 {
		return nil
	}

	return append([]string(nil), s...)
}

// validName rejects an empty or '/'-containing resource name.
func validName(kind, name string) error {
	if name == "" {
		return cerrors.Newf(cerrors.InvalidArgument, "%s name is required", kind)
	}

	if containsSlash(name) {
		return cerrors.Newf(cerrors.InvalidArgument, "%s name %q must not contain '/'", kind, name)
	}

	return nil
}

func containsSlash(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == '/' {
			return true
		}
	}

	return false
}

// ---- Tags (addressed by ARN) ----

func (m *Mock) tagList(arn string) []mdbdriver.Tag {
	out := []mdbdriver.Tag{}
	for k, v := range m.tags[arn] {
		out = append(out, mdbdriver.Tag{Key: k, Value: v})
	}

	return out
}

// TagResource adds tags to a resource by ARN.
func (m *Mock) TagResource(_ context.Context, arn string, tags map[string]string) ([]mdbdriver.Tag, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.tags[arn] == nil {
		m.tags[arn] = make(map[string]string)
	}

	for k, v := range tags {
		m.tags[arn][k] = v
	}

	return m.tagList(arn), nil
}

// UntagResource removes tag keys from a resource by ARN.
func (m *Mock) UntagResource(_ context.Context, arn string, keys []string) ([]mdbdriver.Tag, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, k := range keys {
		delete(m.tags[arn], k)
	}

	return m.tagList(arn), nil
}

// ListTags returns a resource's tags by ARN.
func (m *Mock) ListTags(_ context.Context, arn string) ([]mdbdriver.Tag, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.tagList(arn), nil
}

// ---- Catalogs ----

// DescribeEngineVersions returns the supported engine-version catalog.
func (*Mock) DescribeEngineVersions(_ context.Context, engine, version string) ([]mdbdriver.EngineVersionInfo, error) {
	all := []mdbdriver.EngineVersionInfo{
		{Engine: "redis", EngineVersion: "7.1", EnginePatchVersion: "7.1.1", ParameterGroupFamily: "memorydb_redis7"},
		{Engine: "redis", EngineVersion: "6.2", EnginePatchVersion: "6.2.6", ParameterGroupFamily: "memorydb_redis6"},
		{Engine: "valkey", EngineVersion: "7.2", EnginePatchVersion: "7.2.0", ParameterGroupFamily: "memorydb_valkey7"},
	}

	out := make([]mdbdriver.EngineVersionInfo, 0, len(all))

	for _, v := range all {
		if engine != "" && v.Engine != engine {
			continue
		}

		if version != "" && v.EngineVersion != version {
			continue
		}

		out = append(out, v)
	}

	return out, nil
}

// DescribeEvents returns the lifecycle event log.
func (m *Mock) DescribeEvents(_ context.Context) ([]mdbdriver.Event, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return append([]mdbdriver.Event(nil), m.events...), nil
}
