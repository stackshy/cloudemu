package kafka

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/kafka/driver"
)

// CreateClusterV2 provisions either a provisioned or a serverless cluster,
// storing a single cluster with its ClusterType so v1 and v2 reads see the same
// object. Exactly one of Provisioned/Serverless is set; a request with neither
// (or both) is a BadRequestException.
func (m *Mock) CreateClusterV2(_ context.Context, in driver.CreateClusterV2Input) (*driver.Cluster, error) {
	if err := validateClusterName(in.ClusterName); err != nil {
		return nil, err
	}

	provisioned := in.Provisioned != nil
	serverless := len(in.Serverless) > 0

	if provisioned == serverless {
		return nil, badRequest("exactly one of provisioned or serverless must be set")
	}

	if provisioned {
		return m.createClusterV2Provisioned(in)
	}

	return m.createClusterV2Serverless(in)
}

func (m *Mock) createClusterV2Provisioned(in driver.CreateClusterV2Input) (*driver.Cluster, error) {
	p := in.Provisioned

	if p.BrokerNodeGroupInfo == nil {
		return nil, badRequest("provisioned.brokerNodeGroupInfo is required")
	}

	if p.NumberOfBrokerNodes <= 0 {
		return nil, badRequest("provisioned.numberOfBrokerNodes must be greater than 0")
	}

	if err := validateBrokerNodeGroup(p.BrokerNodeGroupInfo); err != nil {
		return nil, err
	}

	if err := validateStorageMode(p.StorageMode); err != nil {
		return nil, err
	}

	if err := validateEnhancedMonitoring(p.EnhancedMonitoring); err != nil {
		return nil, err
	}

	kafkaVer := p.KafkaVersion
	if kafkaVer == "" {
		kafkaVer = defaultKafkaVer
	}

	cluster := driver.Cluster{
		ClusterName:         in.ClusterName,
		ClusterType:         driver.ClusterTypeProvisioned,
		KafkaVersion:        kafkaVer,
		NumberOfBrokerNodes: p.NumberOfBrokerNodes,
		BrokerNodeGroupInfo: copyBrokerNodeGroupInfo(p.BrokerNodeGroupInfo),
		StorageMode:         p.StorageMode,
		EnhancedMonitoring:  p.EnhancedMonitoring,
		Tags:                copyTags(in.Tags),
		RawOptions:          copyRaw(p.RawOptions),
	}

	return m.insertCluster(cluster)
}

func (m *Mock) createClusterV2Serverless(in driver.CreateClusterV2Input) (*driver.Cluster, error) {
	raw := copyRaw(in.RawOptions)
	if raw == nil {
		raw = map[string]json.RawMessage{}
	}

	raw["serverless"] = append(json.RawMessage(nil), in.Serverless...)

	cluster := driver.Cluster{
		ClusterName: in.ClusterName,
		ClusterType: driver.ClusterTypeServerless,
		Tags:        copyTags(in.Tags),
		RawOptions:  raw,
	}

	return m.insertCluster(cluster)
}

// insertCluster finishes minting a cluster (ARN, version, state, creation time)
// and stores it atomically under createMu, rejecting a duplicate name.
//
//nolint:gocritic // hugeParam: takes the assembled cluster by value to finalize.
func (m *Mock) insertCluster(cluster driver.Cluster) (*driver.Cluster, error) {
	m.createMu.Lock()
	defer m.createMu.Unlock()

	if _, dup := m.clusterNames.Get(cluster.ClusterName); dup {
		return nil, conflict("cluster already exists: %s", cluster.ClusterName)
	}

	cluster.ClusterARN = m.clusterARN(cluster.ClusterName)
	cluster.State = driver.ClusterStateActive
	cluster.CurrentVersion = "K" + idgen.GenerateID("")
	cluster.CreationTime = m.now()

	m.clusters.Set(cluster.ClusterARN, &clusterData{cluster: cluster})
	m.clusterNames.Set(cluster.ClusterName, cluster.ClusterARN)

	out := snapshotCluster(cluster)

	return &out, nil
}

// DescribeClusterV2 returns the same stored cluster as DescribeCluster; the wire
// layer renders the v2 shape. A cluster created via v1 is describable here.
func (m *Mock) DescribeClusterV2(ctx context.Context, arn string) (*driver.Cluster, error) {
	return m.DescribeCluster(ctx, arn)
}

// ListClustersV2 lists clusters filtered by name prefix and (optionally) cluster
// type, sharing the same underlying store as ListClusters.
func (m *Mock) ListClustersV2(
	_ context.Context, namePrefix, clusterType string, page driver.Page,
) (clusters []driver.Cluster, next string, err error) {
	if vErr := validateClusterTypeFilter(clusterType); vErr != nil {
		return nil, "", vErr
	}

	vals := m.clusters.SortedValues()

	all := make([]driver.Cluster, 0, len(vals))

	for _, cd := range vals {
		cd.mu.RLock()
		snap := snapshotCluster(cd.cluster)
		cd.mu.RUnlock()

		if namePrefix != "" && !strings.HasPrefix(snap.ClusterName, namePrefix) {
			continue
		}

		if clusterType != "" && !strings.EqualFold(snap.ClusterType, clusterType) {
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

// validateClusterTypeFilter rejects a clusterTypeFilter that is neither a known
// cluster type nor empty.
func validateClusterTypeFilter(clusterType string) error {
	switch {
	case clusterType == "",
		strings.EqualFold(clusterType, driver.ClusterTypeProvisioned),
		strings.EqualFold(clusterType, driver.ClusterTypeServerless):
		return nil
	default:
		return badRequest("invalid clusterTypeFilter: %q", clusterType)
	}
}
