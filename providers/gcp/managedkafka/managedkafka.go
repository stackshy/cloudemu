// Package managedkafka provides an in-memory mock of the Google Cloud Managed
// Service for Apache Kafka control plane (managedkafka.googleapis.com/v1). It
// models clusters, the topics nested under them, and the long-running
// operations cluster mutations return. It is control-plane only: there are no
// brokers and no produce/consume data plane.
package managedkafka

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/internal/settle"
	mkdriver "github.com/stackshy/cloudemu/v2/services/managedkafka/driver"
)

var _ mkdriver.ManagedKafka = (*Mock)(nil)

const (
	clustersColl = "clusters"
	topicsColl   = "topics"

	// stateActive is the steady state a cluster reports. stateCreating is the
	// transient state a new cluster reports for a settle window under
	// config.Options.AsyncSettle (real Managed Kafka passes through CREATING);
	// with AsyncSettle off (the default) a new cluster is ACTIVE at once.
	stateActive   = "ACTIVE"
	stateCreating = "CREATING"

	// defaultKafkaVersion is the version the real API assigns when a create
	// leaves kafkaVersion unset.
	defaultKafkaVersion = "3.7.x"

	// apiVersion is the OperationMetadata.apiVersion every operation reports.
	apiVersion = "v1"

	// maxOperations bounds the operation store: the oldest operation is evicted
	// once more than this many exist, so a long-lived emulator does not grow
	// without bound. Every operation is done when returned, so an evicted name
	// only matters to a caller that polls it far later (it is then NOT_FOUND,
	// as a garbage-collected real operation is).
	maxOperations = 1000

	opCreate = "create"
	opUpdate = "update"
	opDelete = "delete"

	opNameMarker = "/operations/operation-"
)

// Mock is the in-memory Managed Kafka control-plane implementation. Clusters and
// topics are keyed by their full GCP resource names.
type Mock struct {
	mu sync.RWMutex

	clusters   *memstore.Store[mkdriver.Cluster]
	topics     *memstore.Store[mkdriver.Topic]
	operations *memstore.Store[mkdriver.Operation]

	// creating overlays a transient CREATING window (keyed by cluster name) on
	// the stored ACTIVE state; inert unless config.Options.AsyncSettle is set.
	creating *settle.Set

	opSeq atomic.Uint64
	opts  *config.Options
}

// New creates a new Managed Kafka mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		clusters:   memstore.New[mkdriver.Cluster](),
		topics:     memstore.New[mkdriver.Topic](),
		operations: memstore.New[mkdriver.Operation](),
		creating:   settle.NewSet(),
		opts:       opts,
	}
}

// clusterName builds the full cluster resource name.
func clusterName(project, location, id string) string {
	return "projects/" + project + "/locations/" + location + "/" + clustersColl + "/" + id
}

// topicName builds the full topic resource name.
func topicName(project, location, clusterID, id string) string {
	return clusterName(project, location, clusterID) + "/" + topicsColl + "/" + id
}

// newOp records a completed operation scoped to the project+location it acted in
// and returns it, evicting the oldest operation past maxOperations. The caller
// holds the write lock.
func (m *Mock) newOp(project, location, opType, target string) *mkdriver.Operation {
	now := m.opts.Clock.Now().UTC()
	scope := "projects/" + project + "/locations/" + location
	op := mkdriver.Operation{
		Name:       fmt.Sprintf("%s%s%d-%s", scope, opNameMarker, m.opSeq.Add(1), idgen.UUID()),
		Done:       true,
		TargetName: target,
		Type:       opType,
		APIVersion: apiVersion,
		CreateTime: now,
		EndTime:    now,
	}
	m.operations.Set(op.Name, op)
	m.evictOldestOps()

	return &op
}

// evictOldestOps drops the lowest-sequence operations until at most
// maxOperations remain. The caller holds the write lock.
func (m *Mock) evictOldestOps() {
	for m.operations.Len() > maxOperations {
		oldest, oldestSeq := "", uint64(0)

		for _, k := range m.operations.Keys() {
			if seq := opSeqOf(k); oldest == "" || seq < oldestSeq {
				oldest, oldestSeq = k, seq
			}
		}

		m.operations.Delete(oldest)
	}
}

// opSeqOf parses the sequence number out of an operation name
// ".../operations/operation-{seq}-{uuid}"; an unparseable name sorts first.
func opSeqOf(name string) uint64 {
	_, rest, ok := strings.Cut(name, opNameMarker)
	if !ok {
		return 0
	}

	digits, _, _ := strings.Cut(rest, "-")

	seq, err := strconv.ParseUint(digits, 10, 64)
	if err != nil {
		return 0
	}

	return seq
}

// observe returns a clone of stored with its settle window overlaid on State.
func (m *Mock) observe(key string, stored *mkdriver.Cluster) mkdriver.Cluster {
	out := cloneCluster(stored)
	out.State = m.creating.State(key, m.opts.Clock.Now(), out.State)

	return out
}

// CreateCluster validates and stores a new cluster (defaulting kafkaVersion and
// rebalanceConfig.mode as the real API does) and returns the completed LRO. The
// cluster reports ACTIVE, or CREATING for a settle window under AsyncSettle.
func (m *Mock) CreateCluster(_ context.Context, c *mkdriver.Cluster) (*mkdriver.Cluster, *mkdriver.Operation, error) {
	if err := validateClusterID(c.ID); err != nil {
		return nil, nil, err
	}

	stored := cloneCluster(c)
	applyClusterDefaults(&stored)

	if err := validateCluster(&stored); err != nil {
		return nil, nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	key := clusterName(c.Project, c.Location, c.ID)
	if m.clusters.Has(key) {
		return nil, nil, cerrors.Newf(cerrors.AlreadyExists, "cluster %q already exists", key)
	}

	now := m.opts.Clock.Now().UTC()
	stored.State = stateActive
	stored.CreateTime = now
	stored.UpdateTime = now
	m.clusters.Set(key, stored)
	m.creating.Begin(key, stateCreating, now, m.opts.SettleDuration(settle.DefaultClusterSettle))

	op := m.newOp(c.Project, c.Location, opCreate, key)
	out := m.observe(key, &stored)

	return &out, op, nil
}

// GetCluster returns a cluster by identity, cloned.
func (m *Mock) GetCluster(_ context.Context, project, location, id string) (*mkdriver.Cluster, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	key := clusterName(project, location, id)

	c, ok := m.clusters.Get(key)
	if !ok {
		return nil, clusterNotFound(project, location, id)
	}

	out := m.observe(key, &c)

	return &out, nil
}

// ListClusters returns every cluster in a project+location, ordered by name.
func (m *Mock) ListClusters(_ context.Context, project, location string) ([]mkdriver.Cluster, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	prefix := "projects/" + project + "/locations/" + location + "/" + clustersColl + "/"
	all := m.clusters.SortedValues()
	out := make([]mkdriver.Cluster, 0, len(all))

	for i := range all {
		if key := clusterName(all[i].Project, all[i].Location, all[i].ID); strings.HasPrefix(key, prefix) {
			out = append(out, m.observe(key, &all[i]))
		}
	}

	return out, nil
}

// UpdateCluster applies the masked fields of c to the stored cluster,
// re-validates the result, and returns the completed LRO. Unknown, immutable and
// output-only mask paths are rejected with INVALID_ARGUMENT before anything
// changes.
func (m *Mock) UpdateCluster(_ context.Context, c *mkdriver.Cluster, mask []string) (
	*mkdriver.Cluster, *mkdriver.Operation, error,
) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := clusterName(c.Project, c.Location, c.ID)

	stored, ok := m.clusters.Get(key)
	if !ok {
		return nil, nil, clusterNotFound(c.Project, c.Location, c.ID)
	}

	next := cloneCluster(&stored)
	if err := applyClusterMask(&next, c, mask); err != nil {
		return nil, nil, err
	}

	applyClusterDefaults(&next)

	if err := validateCluster(&next); err != nil {
		return nil, nil, err
	}

	next.UpdateTime = m.opts.Clock.Now().UTC()
	m.clusters.Set(key, next)

	op := m.newOp(c.Project, c.Location, opUpdate, key)
	out := m.observe(key, &next)

	return &out, op, nil
}

// DeleteCluster removes a cluster together with every topic under it and returns
// the completed LRO.
func (m *Mock) DeleteCluster(_ context.Context, project, location, id string) (*mkdriver.Operation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := clusterName(project, location, id)
	if !m.clusters.Has(key) {
		return nil, clusterNotFound(project, location, id)
	}

	m.clusters.Delete(key)
	m.creating.Clear(key)

	prefix := key + "/" + topicsColl + "/"
	for _, k := range m.topics.Keys() {
		if strings.HasPrefix(k, prefix) {
			m.topics.Delete(k)
		}
	}

	return m.newOp(project, location, opDelete, key), nil
}

// GetOperation returns a long-running operation this mock created, by name. An
// unknown (never created, or evicted) name is NOT_FOUND, as in the real API.
func (m *Mock) GetOperation(_ context.Context, name string) (*mkdriver.Operation, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	op, ok := m.operations.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "operation %q not found", name)
	}

	return &op, nil
}

// clusterNotFound builds the NOT_FOUND error carrying the full resource name.
func clusterNotFound(project, location, id string) error {
	return cerrors.Newf(cerrors.NotFound, "cluster %q not found", clusterName(project, location, id))
}
