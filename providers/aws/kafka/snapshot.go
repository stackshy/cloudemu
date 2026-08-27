package kafka

import (
	"context"
	"encoding/json"
	"fmt"

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
// The per-record mutexes, createMu, and the wired opts are not serialized.
type kafkaSnapshot struct {
	Clusters     map[string]*clusterSnapshot     `json:"clusters,omitempty"`
	ClusterNames json.RawMessage                 `json:"clusterNames,omitempty"`
	Configs      map[string]driver.Configuration `json:"configs,omitempty"`
	VPCConns     map[string]driver.VpcConnection `json:"vpcConns,omitempty"`
	Replicators  map[string]*replicatorSnapshot  `json:"replicators,omitempty"`
}

// clusterSnapshot mirrors clusterData (all fields unexported).
type clusterSnapshot struct {
	Cluster       driver.Cluster            `json:"cluster"`
	Operations    []driver.ClusterOperation `json:"operations,omitempty"`
	Topics        map[string]driver.Topic   `json:"topics,omitempty"`
	ScramSecrets  []string                  `json:"scramSecrets,omitempty"`
	Policy        string                    `json:"policy,omitempty"`
	PolicyVersion string                    `json:"policyVersion,omitempty"`
}

// replicatorSnapshot mirrors replicatorData.
type replicatorSnapshot struct {
	Replicator driver.Replicator `json:"replicator"`
	Version    string            `json:"version,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// MSK holds resource metadata, not bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := kafkaSnapshot{}

	if m.clusters.Len() > 0 {
		snap.Clusters = make(map[string]*clusterSnapshot, m.clusters.Len())

		for arn, cd := range m.clusters.All() {
			cd.mu.RLock()
			snap.Clusters[arn] = &clusterSnapshot{
				Cluster: cd.cluster, Operations: cd.operations, Topics: cd.topics,
				ScramSecrets: cd.scramSecrets, Policy: cd.policy, PolicyVersion: cd.policyVersion,
			}
			cd.mu.RUnlock()
		}
	}

	names, err := m.clusterNames.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("kafka: snapshot clusterNames: %w", err)
	}

	snap.ClusterNames = names

	if m.configs.Len() > 0 {
		snap.Configs = make(map[string]driver.Configuration, m.configs.Len())

		for arn, cd := range m.configs.All() {
			cd.mu.RLock()
			snap.Configs[arn] = cd.config
			cd.mu.RUnlock()
		}
	}

	if m.vpcConns.Len() > 0 {
		snap.VPCConns = make(map[string]driver.VpcConnection, m.vpcConns.Len())

		for arn, vd := range m.vpcConns.All() {
			vd.mu.RLock()
			snap.VPCConns[arn] = vd.vpc
			vd.mu.RUnlock()
		}
	}

	if m.replicators.Len() > 0 {
		snap.Replicators = make(map[string]*replicatorSnapshot, m.replicators.Len())

		for arn, rd := range m.replicators.All() {
			rd.mu.RLock()
			snap.Replicators[arn] = &replicatorSnapshot{Replicator: rd.replicator, Version: rd.version}
			rd.mu.RUnlock()
		}
	}

	return json.Marshal(snap)
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
		cd := &clusterData{
			cluster: cs.Cluster, operations: cs.Operations, topics: cs.Topics,
			scramSecrets: cs.ScramSecrets, policy: cs.Policy, policyVersion: cs.PolicyVersion,
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
		m.vpcConns.Set(arn, &vpcConnData{vpc: vpc})
	}

	for arn, rs := range snap.Replicators {
		m.replicators.Set(arn, &replicatorData{replicator: rs.Replicator, version: rs.Version})
	}

	return nil
}
