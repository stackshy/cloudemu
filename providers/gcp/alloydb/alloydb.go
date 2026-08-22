// Package alloydb provides an in-memory mock of GCP AlloyDB, a
// PostgreSQL-compatible managed database. It implements
// relationaldb/driver.RelationalDB (plus the Users and Databases optional
// capabilities) so the same backend serves both the portable API and the
// SDK-compat AlloyDB REST layer.
//
// Mapping into the relationaldb shape:
//
//   - Cluster        → an AlloyDB cluster (PRIMARY or SECONDARY).
//   - Instance       → an AlloyDB instance under a cluster (PRIMARY /
//     READ_POOL / SECONDARY); the portable ClusterID is the cluster name.
//   - ClusterSnapshot → an AlloyDB backup (backups are cluster-scoped).
//   - Snapshot        → NOT supported (AlloyDB has no instance-level backup);
//     the instance-snapshot methods return InvalidArgument.
//
// AlloyDB has no start/stop for clusters or instances, so those return
// InvalidArgument; RebootInstance maps to the instances.restart action.
// AlloyDB-native attributes that don't fit the portable structs (instance
// type, machine vCPU count, availability type, IP, continuous/automated backup,
// maintenance window, secondary-cluster linkage) are kept in side-stores so the
// REST layer can render faithful wire responses.
package alloydb

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

const (
	defaultPort            = 5432
	defaultDatabaseVersion = "POSTGRES_15"
	defaultCPUCount        = 2
	metricsNamespace       = "alloydb.googleapis.com"
	cpuMetricRunning       = 0.25 // GCP reports CPU as a 0.0–1.0 fraction.
	connRunning            = 5.0

	// syntheticInstanceIP is the ipAddress reported when no real database engine
	// backs the instance — preserving the historical behavior of always surfacing
	// an IP. When an engine is wired in, provisionInstanceEngine overrides it with
	// the real reachable host.
	syntheticInstanceIP = "10.0.0.2"

	// Instance types.
	instanceTypePrimary   = "PRIMARY"
	instanceTypeReadPool  = "READ_POOL"
	instanceTypeSecondary = "SECONDARY"

	// Cluster types.
	clusterTypePrimary   = "PRIMARY"
	clusterTypeSecondary = "SECONDARY"
)

var _ rdsdriver.RelationalDB = (*Mock)(nil)

// clusterExtra holds AlloyDB cluster attributes that don't map onto the
// portable rdsdriver.Cluster struct.
type clusterExtra struct {
	ClusterType            string
	DatabaseVersion        string
	Network                string
	AutomatedBackupEnabled bool
	ContinuousBackup       bool
	MaintenanceDay         string
	PrimaryCluster         string // source cluster name, for a SECONDARY cluster
}

// instanceExtra holds AlloyDB instance attributes that don't map onto the
// portable rdsdriver.Instance struct.
type instanceExtra struct {
	InstanceType     string
	CPUCount         int
	NodeCount        int
	AvailabilityType string
	IPAddress        string
	GceZone          string
}

// backupExtra holds AlloyDB backup attributes beyond the portable
// ClusterSnapshot struct.
type backupExtra struct {
	Type string // ON_DEMAND | AUTOMATED | CONTINUOUS
}

// Mock is the in-memory GCP AlloyDB implementation.
type Mock struct {
	mu sync.RWMutex

	// clusters key = cluster ID; instances key = "cluster/instance";
	// backups key = backup ID.
	clusters  *memstore.Store[rdsdriver.Cluster]
	instances *memstore.Store[rdsdriver.Instance]
	backups   *memstore.Store[rdsdriver.ClusterSnapshot]

	// child resources keyed "cluster/name".
	databases *memstore.Store[rdsdriver.Database]
	users     *memstore.Store[rdsdriver.User]

	// AlloyDB-native attribute side-stores (guarded by mu), same keys as above.
	clusterExtra  map[string]clusterExtra
	instanceExtra map[string]instanceExtra
	backupExtra   map[string]backupExtra

	// initialPasswords remembers each cluster's initial-user password (guarded by
	// mu) so its instances can provision a real database against a configured
	// DatabaseEngine. The emulator enforces no auth, so this is local state, not a
	// secret store; it is never logged.
	initialPasswords map[string]string

	opts       *config.Options
	monitoring mondriver.Monitoring
}

// New creates a new AlloyDB mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		clusters:         memstore.New[rdsdriver.Cluster](),
		instances:        memstore.New[rdsdriver.Instance](),
		backups:          memstore.New[rdsdriver.ClusterSnapshot](),
		databases:        memstore.New[rdsdriver.Database](),
		users:            memstore.New[rdsdriver.User](),
		clusterExtra:     make(map[string]clusterExtra),
		instanceExtra:    make(map[string]instanceExtra),
		backupExtra:      make(map[string]backupExtra),
		initialPasswords: make(map[string]string),
		opts:             opts,
	}
}

// SetMonitoring wires a Cloud Monitoring backend for auto-metric emission.
func (m *Mock) SetMonitoring(mon mondriver.Monitoring) {
	m.monitoring = mon
}

// Region returns the region the mock was created with (used by resource
// discovery, since AlloyDB clusters are regional).
func (m *Mock) Region() string {
	return m.opts.Region
}

func (m *Mock) clusterName(id string) string {
	return fmt.Sprintf("projects/%s/locations/%s/clusters/%s", m.opts.ProjectID, m.opts.Region, id)
}

func (m *Mock) instanceName(cluster, instance string) string {
	return m.clusterName(cluster) + "/instances/" + instance
}

func (m *Mock) backupName(id string) string {
	return fmt.Sprintf("projects/%s/locations/%s/backups/%s", m.opts.ProjectID, m.opts.Region, id)
}

func instanceKey(cluster, instance string) string { return cluster + "/" + instance }

// splitInstanceKey resolves an instance identifier that is either the composite
// "{cluster}/{instance}" or a bare "{instance}" (accepted only when unique).
func splitInstanceKey(id string) (cluster, instance string, composite bool) {
	if i := strings.IndexByte(id, '/'); i >= 0 {
		return id[:i], id[i+1:], true
	}

	return "", id, false
}

func copyTags(src map[string]string) map[string]string {
	if src == nil {
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

//nolint:gocritic // value copy is intentional — the result must not alias the store.
func cloneCluster(c rdsdriver.Cluster) rdsdriver.Cluster {
	c.Tags = copyTags(c.Tags)
	c.VPCSecurityGroups = cloneStrings(c.VPCSecurityGroups)
	c.Members = cloneStrings(c.Members)

	return c
}

//nolint:gocritic // value copy is intentional — the result must not alias the store.
func cloneInstance(inst rdsdriver.Instance) rdsdriver.Instance {
	inst.Tags = copyTags(inst.Tags)
	inst.VPCSecurityGroups = cloneStrings(inst.VPCSecurityGroups)
	inst.ReadReplicaTargets = cloneStrings(inst.ReadReplicaTargets)

	return inst
}

//nolint:gocritic // value copy is intentional — the result must not alias the store.
func cloneClusterSnapshot(s rdsdriver.ClusterSnapshot) rdsdriver.ClusterSnapshot {
	s.Tags = copyTags(s.Tags)

	return s
}

// validName rejects an identifier containing '/', which would collide with the
// composite storage keys and produce a resource unreachable via the
// single-segment REST paths.
func validName(kind, name string) error {
	if name == "" {
		return cerrors.Newf(cerrors.InvalidArgument, "%s name is required", kind)
	}

	if strings.Contains(name, "/") {
		return cerrors.Newf(cerrors.InvalidArgument, "%s name %q must not contain '/'", kind, name)
	}

	return nil
}

// emitInstanceMetrics pushes AlloyDB utilization metrics on the
// alloydb.googleapis.com namespace (emitted on instance create, like the
// Cloud SQL sibling).
func (m *Mock) emitInstanceMetrics(cluster, instance string, cpuFrac, connections float64) {
	if m.monitoring == nil {
		return
	}

	now := m.opts.Clock.Now()
	dims := map[string]string{
		"resource_id": m.instanceName(cluster, instance),
		"instance_id": instance,
	}

	_ = m.monitoring.PutMetricData(context.Background(), []mondriver.MetricDatum{
		{Namespace: metricsNamespace, MetricName: "instance/cpu/utilization",
			Value: cpuFrac, Unit: "1", Dimensions: dims, Timestamp: now},
		{Namespace: metricsNamespace, MetricName: "instance/postgres/connections",
			Value: connections, Unit: "1", Dimensions: dims, Timestamp: now},
		{Namespace: metricsNamespace, MetricName: "instance/memory/usage",
			Value: 0.4, Unit: "1", Dimensions: dims, Timestamp: now},
		{Namespace: metricsNamespace, MetricName: "instance/disk/bytes_used",
			Value: 1024, Unit: "By", Dimensions: dims, Timestamp: now},
	})
}
