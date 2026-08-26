package kafka

import (
	"context"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/kafka/driver"
)

// Operation-state constants. The emulator completes every mutation immediately
// and deterministically, so a recorded operation is always COMPLETED.
const (
	operationStateCompleted = "COMPLETED"
)

// copyOperation returns a deep copy of an operation record so a reader cannot
// alias the stored RawOptions map or the source/target snapshots.
//
//nolint:gocritic // hugeParam: takes a value to return an alias-free copy.
func copyOperation(op driver.ClusterOperation) driver.ClusterOperation {
	out := op
	out.RawOptions = copyRaw(op.RawOptions)
	out.SourceClusterInfo = copyMutableInfo(op.SourceClusterInfo)
	out.TargetClusterInfo = copyMutableInfo(op.TargetClusterInfo)

	return out
}

// copyMutableInfo returns a copy of a mutable-cluster-info snapshot (all value
// fields, so a shallow struct copy fully de-aliases it), or nil.
func copyMutableInfo(mi *driver.MutableClusterInfo) *driver.MutableClusterInfo {
	if mi == nil {
		return nil
	}

	out := *mi

	return &out
}

// mutableSnapshot captures the modeled mutable attributes of a cluster so an
// operation can record the before/after of a mutation. It reads the EBS volume
// size by value, so the snapshot is stable even after fn rewrites storageInfo.
func mutableSnapshot(c *driver.Cluster) *driver.MutableClusterInfo {
	info := &driver.MutableClusterInfo{
		NumberOfBrokerNodes: c.NumberOfBrokerNodes,
		KafkaVersion:        c.KafkaVersion,
		StorageMode:         c.StorageMode,
		EnhancedMonitoring:  c.EnhancedMonitoring,
	}

	if c.BrokerNodeGroupInfo != nil {
		info.InstanceType = c.BrokerNodeGroupInfo.InstanceType
		if size, ok := ebsVolumeSize(c.BrokerNodeGroupInfo); ok {
			info.EBSVolumeSizeGB = size
		}
	}

	return info
}

// recordOperation appends a COMPLETED operation record of the given type to the
// cluster and indexes it globally by its ARN. The caller MUST already hold
// cd.mu for writing; recording is part of the same atomic mutation so a
// concurrent reader never sees the new cluster state without its operation.
func (m *Mock) recordOperation(
	cd *clusterData, opType string, source, target *driver.MutableClusterInfo,
) driver.ClusterOperation {
	op := driver.ClusterOperation{
		OperationARN:      m.operationARN(),
		ClusterARN:        cd.cluster.ClusterARN,
		OperationType:     opType,
		OperationState:    operationStateCompleted,
		CreationTime:      m.now(),
		SourceClusterInfo: source,
		TargetClusterInfo: target,
	}

	cd.operations = append(cd.operations, op)
	m.operations.Set(op.OperationARN, cd)

	return op
}

// mutateCluster resolves a cluster, verifies the optimistic-concurrency
// currentVersion, applies fn under the cluster write-lock, records an operation
// of opType, and returns a copy of that operation. An empty currentVersion
// skips the version check (matching ops like RebootBroker that take none). A
// missing cluster returns NotFoundException — use it for update ops that model
// NotFoundException.
func (m *Mock) mutateCluster(
	arn, currentVersion, opType string, fn func(c *driver.Cluster),
) (*driver.ClusterOperation, error) {
	return m.mutateClusterErr(arn, currentVersion, opType, notFound, fn)
}

// mutateClusterBR is mutateCluster for the update ops that do NOT model
// NotFoundException (UpdateBrokerCount/Storage, UpdateMonitoring) — a missing
// cluster is a BadRequestException there.
func (m *Mock) mutateClusterBR(
	arn, currentVersion, opType string, fn func(c *driver.Cluster),
) (*driver.ClusterOperation, error) {
	return m.mutateClusterErr(arn, currentVersion, opType, badRequest, fn)
}

func (m *Mock) mutateClusterErr(
	arn, currentVersion, opType string, mkMissing func(string, ...any) error, fn func(c *driver.Cluster),
) (*driver.ClusterOperation, error) {
	cd, err := m.getClusterErr(arn, mkMissing)
	if err != nil {
		return nil, err
	}

	cd.mu.Lock()
	defer cd.mu.Unlock()

	if currentVersion != "" && cd.cluster.CurrentVersion != currentVersion {
		return nil, badRequest(
			"currentVersion %q does not match cluster version %q",
			currentVersion, cd.cluster.CurrentVersion)
	}

	// Snapshot the mutable attributes before and after fn so the recorded
	// operation carries the source/target delta real MSK reports.
	source := mutableSnapshot(&cd.cluster)

	if fn != nil {
		fn(&cd.cluster)
	}

	cd.cluster.CurrentVersion = bumpVersion()

	target := mutableSnapshot(&cd.cluster)

	op := m.recordOperation(cd, opType, source, target)
	out := copyOperation(op)

	return &out, nil
}

// bumpVersion mints a fresh optimistic-concurrency version token so a subsequent
// mutation must present the new value. Deterministic given the idgen sequence.
func bumpVersion() string {
	return "K" + idgen.GenerateID("")
}

// ListClusterOperations lists a cluster's operations, oldest first, paginated.
// The v1 op does NOT model NotFoundException in the aws-sdk-go-v2 smithy model,
// so a missing cluster is a 400 (a 404 would deserialize as an untyped generic
// error). The v2 op DOES model it — see ListClusterOperationsV2.
func (m *Mock) ListClusterOperations(
	_ context.Context, arn string, page driver.Page,
) (ops []driver.ClusterOperation, next string, err error) {
	return m.listOperations(arn, badRequest, page)
}

// ListClusterOperationsV2 shares the operation store with ListClusterOperations;
// the wire layer renders the V2 summary shape. The v2 op models
// NotFoundException, so a missing cluster is a 404.
func (m *Mock) ListClusterOperationsV2(
	_ context.Context, arn string, page driver.Page,
) (ops []driver.ClusterOperation, next string, err error) {
	return m.listOperations(arn, notFound, page)
}

func (m *Mock) listOperations(
	arn string, mkMissing func(string, ...any) error, page driver.Page,
) (ops []driver.ClusterOperation, next string, err error) {
	cd, err := m.getClusterErr(arn, mkMissing)
	if err != nil {
		return nil, "", err
	}

	cd.mu.RLock()
	all := make([]driver.ClusterOperation, len(cd.operations))

	for i := range cd.operations {
		all[i] = copyOperation(cd.operations[i])
	}

	cd.mu.RUnlock()

	start, end, nextTok, err := m.paginate(len(all), page)
	if err != nil {
		return nil, "", err
	}

	return all[start:end], nextTok, nil
}

// DescribeClusterOperation resolves an operation by its ARN.
func (m *Mock) DescribeClusterOperation(_ context.Context, opARN string) (*driver.ClusterOperation, error) {
	return m.describeOperation(opARN)
}

// DescribeClusterOperationV2 shares the store with DescribeClusterOperation.
func (m *Mock) DescribeClusterOperationV2(_ context.Context, opARN string) (*driver.ClusterOperation, error) {
	return m.describeOperation(opARN)
}

func (m *Mock) describeOperation(opARN string) (*driver.ClusterOperation, error) {
	cd, ok := m.operations.Get(opARN)
	if !ok {
		return nil, notFound("cluster operation not found: %s", opARN)
	}

	cd.mu.RLock()
	defer cd.mu.RUnlock()

	for i := range cd.operations {
		if cd.operations[i].OperationARN == opARN {
			out := copyOperation(cd.operations[i])

			return &out, nil
		}
	}

	return nil, notFound("cluster operation not found: %s", opARN)
}
