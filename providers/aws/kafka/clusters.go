package kafka

import (
	"context"
	"sort"
	"strings"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/kafka/driver"
)

// copyBrokerNodeGroupInfo deep-copies a broker node group, or returns nil.
func copyBrokerNodeGroupInfo(b *driver.BrokerNodeGroupInfo) *driver.BrokerNodeGroupInfo {
	if b == nil {
		return nil
	}

	out := *b
	out.ClientSubnets = copyStrings(b.ClientSubnets)
	out.SecurityGroups = copyStrings(b.SecurityGroups)
	out.ZoneIDs = copyStrings(b.ZoneIDs)
	out.RawFields = copyRaw(b.RawFields)

	return &out
}

// snapshotCluster returns a deep copy of a stored cluster so a reader cannot
// alias the Tags/RawOptions maps or the broker-group nested slices.
//
//nolint:gocritic // hugeParam: takes a value by design to snapshot stored state.
func snapshotCluster(c driver.Cluster) driver.Cluster {
	out := c
	out.Tags = copyTags(c.Tags)
	out.RawOptions = copyRaw(c.RawOptions)
	out.BrokerNodeGroupInfo = copyBrokerNodeGroupInfo(c.BrokerNodeGroupInfo)

	return out
}

// validateClusterName enforces MSK's cluster-name length rule.
func validateClusterName(name string) error {
	if len(name) < minClusterNameLen || len(name) > maxClusterNameLen {
		return badRequest("cluster name %q must be between %d and %d characters",
			name, minClusterNameLen, maxClusterNameLen)
	}

	return nil
}

// validateStorageMode rejects a StorageMode outside the modeled enum (empty is
// allowed — the field is optional).
func validateStorageMode(mode string) error {
	switch mode {
	case "", "LOCAL", "TIERED":
		return nil
	default:
		return badRequest("storageMode %q is invalid", mode)
	}
}

// validateEnhancedMonitoring rejects an EnhancedMonitoring value outside the
// modeled enum (empty is allowed — the field is optional).
func validateEnhancedMonitoring(level string) error {
	switch level {
	case "", "DEFAULT", "PER_BROKER", "PER_TOPIC_PER_BROKER", "PER_TOPIC_PER_PARTITION":
		return nil
	default:
		return badRequest("enhancedMonitoring %q is invalid", level)
	}
}

// getCluster resolves a cluster by ARN, returning NotFoundException when absent.
// Use it only for ops that model NotFoundException in the SDK.
func (m *Mock) getCluster(arn string) (*clusterData, error) {
	return m.getClusterErr(arn, notFound)
}

// getClusterBR resolves a cluster by ARN, returning BadRequestException when
// absent. Several ops (GetBootstrapBrokers, ListClusterOperations v1,
// ListClientVpcConnections, RejectClientVpcConnection, PutClusterPolicy,
// ListTopics, UpdateBrokerCount/Storage, UpdateMonitoring) reference a cluster
// but do NOT model NotFoundException, so a missing cluster there is a 400.
func (m *Mock) getClusterBR(arn string) (*clusterData, error) {
	return m.getClusterErr(arn, badRequest)
}

// getClusterErr resolves a cluster by ARN, building the missing-resource error
// with mkErr so each op returns the exception the SDK actually models for it.
func (m *Mock) getClusterErr(arn string, mkErr func(string, ...any) error) (*clusterData, error) {
	cd, ok := m.clusters.Get(arn)
	if !ok {
		return nil, mkErr("cluster not found: %s", arn)
	}

	return cd, nil
}

// CreateCluster provisions a provisioned (v1) cluster that is immediately
// Active. The cluster name is claimed atomically under createMu so a duplicate
// name is rejected with ConflictException.
//
//nolint:gocritic // hugeParam: signature fixed by driver.Kafka (by-value input).
func (m *Mock) CreateCluster(_ context.Context, in driver.CreateClusterInput) (*driver.Cluster, error) {
	if err := validateClusterName(in.ClusterName); err != nil {
		return nil, err
	}

	if in.BrokerNodeGroupInfo == nil {
		return nil, badRequest("brokerNodeGroupInfo is required")
	}

	if in.NumberOfBrokerNodes <= 0 {
		return nil, badRequest("numberOfBrokerNodes must be greater than 0")
	}

	if err := validateStorageMode(in.StorageMode); err != nil {
		return nil, err
	}

	if err := validateEnhancedMonitoring(in.EnhancedMonitoring); err != nil {
		return nil, err
	}

	kafkaVer := in.KafkaVersion
	if kafkaVer == "" {
		kafkaVer = defaultKafkaVer
	}

	m.createMu.Lock()
	defer m.createMu.Unlock()

	if _, dup := m.clusterNames.Get(in.ClusterName); dup {
		return nil, conflict("cluster already exists: %s", in.ClusterName)
	}

	arn := m.clusterARN(in.ClusterName)

	cluster := driver.Cluster{
		ClusterARN:          arn,
		ClusterName:         in.ClusterName,
		ClusterType:         driver.ClusterTypeProvisioned,
		State:               driver.ClusterStateActive,
		CurrentVersion:      "K" + idgen.GenerateID(""),
		KafkaVersion:        kafkaVer,
		NumberOfBrokerNodes: in.NumberOfBrokerNodes,
		BrokerNodeGroupInfo: copyBrokerNodeGroupInfo(in.BrokerNodeGroupInfo),
		StorageMode:         in.StorageMode,
		EnhancedMonitoring:  in.EnhancedMonitoring,
		Tags:                copyTags(in.Tags),
		CreationTime:        m.now(),
		RawOptions:          copyRaw(in.RawOptions),
	}

	m.clusters.Set(arn, &clusterData{cluster: cluster})
	m.clusterNames.Set(in.ClusterName, arn)

	out := snapshotCluster(cluster)

	return &out, nil
}

// DescribeCluster returns a deep copy of the stored cluster.
func (m *Mock) DescribeCluster(_ context.Context, arn string) (*driver.Cluster, error) {
	cd, err := m.getCluster(arn)
	if err != nil {
		return nil, err
	}

	cd.mu.RLock()
	defer cd.mu.RUnlock()

	out := snapshotCluster(cd.cluster)

	return &out, nil
}

// ListClusters lists clusters (optionally filtered by name prefix), sorted by
// name for deterministic paging.
func (m *Mock) ListClusters(
	_ context.Context, namePrefix string, page driver.Page,
) (clusters []driver.Cluster, next string, err error) {
	vals := m.clusters.SortedValues()

	all := make([]driver.Cluster, 0, len(vals))

	for _, cd := range vals {
		cd.mu.RLock()
		snap := snapshotCluster(cd.cluster)
		cd.mu.RUnlock()

		if namePrefix != "" && !strings.HasPrefix(snap.ClusterName, namePrefix) {
			continue
		}

		all = append(all, snap)
	}

	sort.Slice(all, func(i, j int) bool { return all[i].ClusterName < all[j].ClusterName })

	start, end, nextTok, err := m.paginate(len(all), page)
	if err != nil {
		return nil, "", err
	}

	return all[start:end], nextTok, nil
}

// DeleteCluster removes a cluster and frees its name for reuse.
func (m *Mock) DeleteCluster(_ context.Context, arn, _ string) (arnOut, state string, err error) {
	cd, err := m.getCluster(arn)
	if err != nil {
		return "", "", err
	}

	cd.mu.Lock()
	name := cd.cluster.ClusterName
	cd.cluster.State = driver.ClusterStateDeleting
	cd.mu.Unlock()

	m.clusters.Delete(arn)
	m.clusterNames.Delete(name)

	return arn, driver.ClusterStateDeleting, nil
}

// GetBootstrapBrokers returns synthesized broker connection strings for a
// cluster. Real MSK derives these from the cluster's brokers; the emulator
// synthesizes a plausible plaintext/TLS pair deterministically from the name.
func (m *Mock) GetBootstrapBrokers(_ context.Context, arn string) (map[string]string, error) {
	cd, err := m.getClusterBR(arn)
	if err != nil {
		return nil, err
	}

	cd.mu.RLock()
	name := cd.cluster.ClusterName
	cd.mu.RUnlock()

	host := "b-1." + name + ".kafka." + m.opts.Region + ".amazonaws.com"

	return map[string]string{
		"bootstrapBrokerString":        host + ":9092",
		"bootstrapBrokerStringTls":     host + ":9094",
		"bootstrapBrokerStringSaslIam": host + ":9098",
	}, nil
}
