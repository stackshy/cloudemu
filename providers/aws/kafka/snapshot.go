package kafka

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/kafka/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// kafkaSnapshot is the full serialized state of the Amazon MSK mock. The
// clusters/configs/vpcConns/replicators stores hold unexported *xData whose
// payload lives in unexported fields, so each is promoted to an exported form
// keyed by its ARN. clusterNames holds plain strings and round-trips through the
// generic memstore helper. The operations store is NOT serialized: it is an
// index of *clusterData pointers shared with the clusters store, rebuilt on
// restore from each cluster's operations slice so the shared identity (a
// DescribeClusterOperation resolves to the same cluster object) is preserved.
// The AsyncSettle lifecycle windows (a cluster's CREATING/DELETING deadline, a
// VPC connection's CREATING deadline) and the metric sample time are
// serialized, so a restored resource keeps its transient state until its window
// ends and Tick keeps its publishing cadence. The per-record mutexes,
// createMu/reapMu, the wired opts and the monitoring backend are not.
type kafkaSnapshot struct {
	Clusters     map[string]*clusterSnapshot     `json:"clusters,omitempty"`
	ClusterNames json.RawMessage                 `json:"clusterNames,omitempty"`
	Configs      map[string]driver.Configuration `json:"configs,omitempty"`
	VPCConns     map[string]driver.VpcConnection `json:"vpcConns,omitempty"`
	// VPCConnReadyAt holds the CREATING deadline of each VPC connection still
	// inside its window (kept beside VPCConns so older snapshots still load).
	VPCConnReadyAt map[string]time.Time           `json:"vpcConnReadyAt,omitempty"`
	Replicators    map[string]*replicatorSnapshot `json:"replicators,omitempty"`
}

// clusterSnapshot mirrors clusterData (all fields unexported).
type clusterSnapshot struct {
	Cluster       driver.Cluster            `json:"cluster"`
	Operations    []driver.ClusterOperation `json:"operations,omitempty"`
	Topics        map[string]driver.Topic   `json:"topics,omitempty"`
	ScramSecrets  []string                  `json:"scramSecrets,omitempty"`
	Policy        string                    `json:"policy,omitempty"`
	PolicyVersion string                    `json:"policyVersion,omitempty"`
	// Lifecycle windows (AsyncSettle) and the last metric sample time.
	ReadyAt         time.Time `json:"readyAt,omitzero"`
	DeleteAt        time.Time `json:"deleteAt,omitzero"`
	MetricsSampleAt time.Time `json:"metricsSampleAt,omitzero"`
}

// replicatorSnapshot mirrors replicatorData.
type replicatorSnapshot struct {
	Replicator driver.Replicator `json:"replicator"`
	Version    string            `json:"version,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused. MSK holds resource
// metadata, not bulk object bodies. Each record is deep-copied under its own
// lock, so the encoding never reads a map or slice a concurrent writer changes.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := kafkaSnapshot{Clusters: m.clustersForSnapshot()}

	names, err := m.clusterNames.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("kafka: snapshot clusterNames: %w", err)
	}

	snap.ClusterNames = names
	snap.Configs = m.configsForSnapshot()
	snap.VPCConns, snap.VPCConnReadyAt = m.vpcConnsForSnapshot()
	snap.Replicators = m.replicatorsForSnapshot()

	return json.Marshal(snap)
}

// clustersForSnapshot deep-copies every cluster record, or returns nil when
// there are none.
func (m *Mock) clustersForSnapshot() map[string]*clusterSnapshot {
	if m.clusters.Len() == 0 {
		return nil
	}

	out := make(map[string]*clusterSnapshot, m.clusters.Len())

	for arn, cd := range m.clusters.All() {
		cd.mu.RLock()
		out[arn] = cd.snapshotLocked()
		cd.mu.RUnlock()
	}

	return out
}

// snapshotLocked deep-copies a cluster record. The caller holds cd.mu.
func (cd *clusterData) snapshotLocked() *clusterSnapshot {
	cs := &clusterSnapshot{
		Cluster:      snapshotCluster(cd.cluster),
		ScramSecrets: copyStrings(cd.scramSecrets),
		Policy:       cd.policy, PolicyVersion: cd.policyVersion,
		ReadyAt: cd.readyAt, DeleteAt: cd.deleteAt, MetricsSampleAt: cd.metricsSampleAt,
	}

	if cd.operations != nil {
		cs.Operations = make([]driver.ClusterOperation, len(cd.operations))
		for i := range cd.operations {
			cs.Operations[i] = copyOperation(cd.operations[i])
		}
	}

	if cd.topics != nil {
		cs.Topics = make(map[string]driver.Topic, len(cd.topics))
		for name, t := range cd.topics {
			cs.Topics[name] = snapshotTopic(t)
		}
	}

	return cs
}

// configsForSnapshot deep-copies every configuration, or returns nil when there
// are none.
func (m *Mock) configsForSnapshot() map[string]driver.Configuration {
	if m.configs.Len() == 0 {
		return nil
	}

	out := make(map[string]driver.Configuration, m.configs.Len())

	for arn, cd := range m.configs.All() {
		cd.mu.RLock()
		out[arn] = snapshotConfig(cd.config)
		cd.mu.RUnlock()
	}

	return out
}

// vpcConnsForSnapshot deep-copies every VPC connection, plus the CREATING
// deadline of each one still inside its window (nil when none is).
func (m *Mock) vpcConnsForSnapshot() (conns map[string]driver.VpcConnection, readyAt map[string]time.Time) {
	if m.vpcConns.Len() == 0 {
		return nil, nil
	}

	conns = make(map[string]driver.VpcConnection, m.vpcConns.Len())

	for arn, vd := range m.vpcConns.All() {
		vd.mu.RLock()
		conns[arn] = snapshotVpcConnection(vd.vpc)

		if !vd.readyAt.IsZero() {
			if readyAt == nil {
				readyAt = map[string]time.Time{}
			}

			readyAt[arn] = vd.readyAt
		}
		vd.mu.RUnlock()
	}

	return conns, readyAt
}

// replicatorsForSnapshot deep-copies every replicator, or returns nil when
// there are none.
func (m *Mock) replicatorsForSnapshot() map[string]*replicatorSnapshot {
	if m.replicators.Len() == 0 {
		return nil
	}

	out := make(map[string]*replicatorSnapshot, m.replicators.Len())

	for arn, rd := range m.replicators.All() {
		rd.mu.RLock()
		out[arn] = &replicatorSnapshot{Replicator: snapshotReplicator(rd.replicator), Version: rd.version}
		rd.mu.RUnlock()
	}

	return out
}

// Restore rebuilds the mock's state under the original identities: every cluster,
// configuration, VPC-connection, and replicator ARN is preserved, and the
// operations index is reconstructed to share each restored cluster's pointer.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap kafkaSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("kafka: parse snapshot: %w", err)
	}

	for arn, cs := range snap.Clusters {
		// A snapshot from before the provider completed encryptionInfo at create
		// lacks it; backfill MSK's default so the restored cluster reports it.
		if cs.Cluster.ClusterType == driver.ClusterTypeProvisioned {
			cs.Cluster.RawOptions = m.withDefaultEncryption(cs.Cluster.RawOptions)
		}

		cd := &clusterData{
			cluster: cs.Cluster, operations: cs.Operations, topics: cs.Topics,
			scramSecrets: cs.ScramSecrets, policy: cs.Policy, policyVersion: cs.PolicyVersion,
			readyAt: cs.ReadyAt, deleteAt: cs.DeleteAt, metricsSampleAt: cs.MetricsSampleAt,
		}

		m.clusters.Set(arn, cd)

		// Rebuild the operation index against the same clusterData pointer so a
		// DescribeClusterOperation resolves to this exact cluster.
		for _, op := range cd.operations {
			m.operations.Set(op.OperationARN, cd)
		}
	}

	if len(snap.ClusterNames) > 0 {
		if err := m.clusterNames.LoadSnapshot(snap.ClusterNames); err != nil {
			return fmt.Errorf("kafka: restore clusterNames: %w", err)
		}
	}

	for arn := range snap.Configs {
		m.configs.Set(arn, &configData{config: snap.Configs[arn]})
	}

	for arn, vpc := range snap.VPCConns {
		m.vpcConns.Set(arn, &vpcConnData{vpc: vpc, readyAt: snap.VPCConnReadyAt[arn]})
	}

	for arn, rs := range snap.Replicators {
		m.replicators.Set(arn, &replicatorData{replicator: rs.Replicator, version: rs.Version})
	}

	return nil
}
