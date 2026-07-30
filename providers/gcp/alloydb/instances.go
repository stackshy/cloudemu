package alloydb

import (
	"context"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

// CreateInstance creates an AlloyDB instance under an existing cluster. The
// base path creates a PRIMARY instance; READ_POOL / SECONDARY instances are
// created via the AlloyDB-native capability. cfg.ClusterID is the cluster;
// cfg.ID is the instance name; cfg.InstanceClass carries the machine size.
//
//nolint:gocritic // cfg matches the driver interface signature.
func (m *Mock) CreateInstance(_ context.Context, cfg rdsdriver.InstanceConfig) (*rdsdriver.Instance, error) {
	if err := validName("instance", cfg.ID); err != nil {
		return nil, err
	}

	if cfg.ClusterID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "ClusterID (AlloyDB cluster) is required to create an instance")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	cluster, ok := m.clusters.Get(cfg.ClusterID)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "AlloyDB cluster %q not found", cfg.ClusterID)
	}

	key := instanceKey(cfg.ClusterID, cfg.ID)
	if _, ok := m.instances.Get(key); ok {
		return nil, cerrors.Newf(cerrors.AlreadyExists,
			"AlloyDB instance %q already exists in cluster %q", cfg.ID, cfg.ClusterID)
	}

	zone := cfg.AvailabilityZone
	if zone == "" {
		zone = m.opts.Region
	}

	inst := rdsdriver.Instance{
		ID:               cfg.ID,
		ARN:              m.instanceName(cfg.ClusterID, cfg.ID),
		Engine:           cluster.Engine,
		EngineVersion:    cluster.EngineVersion,
		InstanceClass:    cfg.InstanceClass,
		Endpoint:         m.instanceName(cfg.ClusterID, cfg.ID) + ".alloydb",
		Port:             defaultPort,
		State:            rdsdriver.StateAvailable,
		ClusterID:        cfg.ClusterID,
		AvailabilityZone: zone,
		CreatedAt:        m.opts.Clock.Now().UTC(),
		Tags:             copyTags(cfg.Tags),
	}

	m.instances.Set(key, inst)
	m.instanceExtra[key] = instanceExtra{
		InstanceType:     instanceTypePrimary,
		CPUCount:         defaultCPUCount,
		AvailabilityType: "REGIONAL",
		IPAddress:        "10.0.0.2",
		GceZone:          zone,
	}

	cluster.Members = append(cluster.Members, cfg.ID)
	m.clusters.Set(cfg.ClusterID, cluster)

	m.emitInstanceMetrics(cfg.ClusterID, cfg.ID, cpuMetricRunning, connRunning)

	out := cloneInstance(inst)

	return &out, nil
}

// lookupInstanceKey resolves an id (composite "{cluster}/{instance}" or bare
// "{instance}") to its storage key. Bare names resolve only when unique across
// clusters. Caller holds m.mu (read or write).
func (m *Mock) lookupInstanceKey(id string) (string, error) {
	cluster, instance, composite := splitInstanceKey(id)
	if composite {
		key := instanceKey(cluster, instance)
		if !m.instances.Has(key) {
			return "", cerrors.Newf(cerrors.NotFound, "AlloyDB instance %q not found", id)
		}

		return key, nil
	}

	var (
		match string
		count int
	)

	for _, key := range m.instances.Keys() {
		if strings.HasSuffix(key, "/"+instance) {
			match = key
			count++
		}
	}

	switch count {
	case 0:
		return "", cerrors.Newf(cerrors.NotFound, "AlloyDB instance %q not found", id)
	case 1:
		return match, nil
	default:
		return "", cerrors.Newf(cerrors.InvalidArgument, "AlloyDB instance %q is ambiguous; qualify as cluster/instance", id)
	}
}

// DescribeInstances returns all instances when ids is empty, else the named
// ones (each composite "{cluster}/{instance}" or unique bare "{instance}").
func (m *Mock) DescribeInstances(_ context.Context, ids []string) ([]rdsdriver.Instance, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(ids) == 0 {
		all := m.instances.All()
		out := make([]rdsdriver.Instance, 0, len(all))

		//nolint:gocritic // map values are large structs; copy is unavoidable when materializing.
		for _, v := range all {
			out = append(out, cloneInstance(v))
		}

		return out, nil
	}

	out := make([]rdsdriver.Instance, 0, len(ids))

	for _, id := range ids {
		key, err := m.lookupInstanceKey(id)
		if err != nil {
			return nil, err
		}

		inst, _ := m.instances.Get(key)
		out = append(out, cloneInstance(inst))
	}

	return out, nil
}

// ModifyInstance applies the non-zero fields of input to an existing instance.
//
//nolint:gocritic // input matches the driver interface signature.
func (m *Mock) ModifyInstance(
	_ context.Context, id string, input rdsdriver.ModifyInstanceInput,
) (*rdsdriver.Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key, err := m.lookupInstanceKey(id)
	if err != nil {
		return nil, err
	}

	inst, _ := m.instances.Get(key)

	if input.InstanceClass != "" {
		inst.InstanceClass = input.InstanceClass
	}

	if input.Tags != nil {
		inst.Tags = copyTags(input.Tags)
	}

	m.instances.Set(key, inst)

	out := cloneInstance(inst)

	return &out, nil
}

// DeleteInstance removes an instance and detaches it from its cluster.
func (m *Mock) DeleteInstance(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	key, err := m.lookupInstanceKey(id)
	if err != nil {
		return err
	}

	inst, _ := m.instances.Get(key)
	m.instances.Delete(key)
	delete(m.instanceExtra, key)

	if cluster, ok := m.clusters.Get(inst.ClusterID); ok {
		cluster.Members = removeStr(cluster.Members, inst.ID)
		m.clusters.Set(inst.ClusterID, cluster)
	}

	return nil
}

func removeStr(items []string, s string) []string {
	out := items[:0:0]

	for _, v := range items {
		if v != s {
			out = append(out, v)
		}
	}

	return out
}

// RebootInstance maps to the AlloyDB instances.restart action.
func (m *Mock) RebootInstance(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, err := m.lookupInstanceKey(id); err != nil {
		return err
	}

	return nil
}

// StartInstance is unsupported — AlloyDB instances cannot be stopped/started.
func (*Mock) StartInstance(_ context.Context, _ string) error {
	return cerrors.New(cerrors.InvalidArgument, "AlloyDB does not support starting instances")
}

// StopInstance is unsupported — AlloyDB instances cannot be stopped/started.
func (*Mock) StopInstance(_ context.Context, _ string) error {
	return cerrors.New(cerrors.InvalidArgument, "AlloyDB does not support stopping instances")
}

// ---- Instance-level snapshots: unsupported (AlloyDB backs up clusters) ----

// CreateSnapshot is unsupported — AlloyDB backups are cluster-scoped.
func (*Mock) CreateSnapshot(_ context.Context, _ rdsdriver.SnapshotConfig) (*rdsdriver.Snapshot, error) {
	return nil, cerrors.New(cerrors.InvalidArgument, "AlloyDB has no instance-level snapshots; use cluster backups")
}

// DescribeSnapshots returns an empty list — AlloyDB has no instance snapshots.
func (*Mock) DescribeSnapshots(_ context.Context, _ []string, _ string) ([]rdsdriver.Snapshot, error) {
	return []rdsdriver.Snapshot{}, nil
}

// DeleteSnapshot is unsupported — AlloyDB has no instance-level snapshots.
func (*Mock) DeleteSnapshot(_ context.Context, _ string) error {
	return cerrors.New(cerrors.InvalidArgument, "AlloyDB has no instance-level snapshots; use cluster backups")
}

// RestoreInstanceFromSnapshot is unsupported — restore is cluster-scoped.
func (*Mock) RestoreInstanceFromSnapshot(
	_ context.Context, _ rdsdriver.RestoreInstanceInput,
) (*rdsdriver.Instance, error) {
	return nil, cerrors.New(cerrors.InvalidArgument, "AlloyDB restore is cluster-scoped; use RestoreClusterFromSnapshot")
}
