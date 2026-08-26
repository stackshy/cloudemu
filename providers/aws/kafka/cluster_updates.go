package kafka

import (
	"context"
	"encoding/json"

	"github.com/stackshy/cloudemu/v2/services/kafka/driver"
)

// Operation-type constants matching the OperationType values real MSK reports.
const (
	opTypeUpdateBrokerCount   = "UPDATE_BROKER_COUNT"
	opTypeUpdateBrokerStorage = "UPDATE_BROKER_STORAGE"
	opTypeUpdateBrokerType    = "UPDATE_BROKER_TYPE"
	opTypeUpdateStorage       = "UPDATE_STORAGE"
	opTypeUpdateClusterConfig = "UPDATE_CLUSTER_CONFIGURATION"
	opTypeUpdateKafkaVersion  = "UPDATE_CLUSTER_KAFKA_VERSION"
	opTypeUpdateConnectivity  = "UPDATE_CONNECTIVITY"
	opTypeUpdateMonitoring    = "UPDATE_MONITORING"
	opTypeUpdateSecurity      = "UPDATE_SECURITY"
	opTypeUpdateRebalancing   = "UPDATE_REBALANCING"
	opTypeRebootBroker        = "REBOOT_NODE"
)

// UpdateBrokerCount sets the cluster's broker count and records an operation.
func (m *Mock) UpdateBrokerCount(
	_ context.Context, arn, currentVersion string, target int32,
) (*driver.ClusterOperation, error) {
	cd, err := m.getClusterBR(arn)
	if err != nil {
		return nil, err
	}

	cd.mu.RLock()
	current := cd.cluster.NumberOfBrokerNodes
	azs := numAZs(cd.cluster.BrokerNodeGroupInfo)
	cd.mu.RUnlock()

	// Real MSK: broker count can only increase, and the total must be a multiple
	// of the number of Availability Zones (client subnets).
	if target <= current {
		return nil, badRequest(
			"targetNumberOfBrokerNodes %d must be greater than the current count %d", target, current)
	}

	if azs > 0 && target%azs != 0 {
		return nil, badRequest(
			"targetNumberOfBrokerNodes %d must be a multiple of the %d Availability Zones", target, azs)
	}

	return m.mutateClusterBR(arn, currentVersion, opTypeUpdateBrokerCount, func(c *driver.Cluster) {
		c.NumberOfBrokerNodes = target
	})
}

// UpdateBrokerStorage rewrites the broker EBS volume size from the request's
// targetBrokerEBSVolumeInfo[ALL].volumeSizeGB so a subsequent DescribeCluster
// reflects the new size (real MSK surfaces it under
// brokerNodeGroupInfo.storageInfo.ebsStorageInfo.volumeSize).
func (m *Mock) UpdateBrokerStorage(
	_ context.Context, arn, currentVersion string, body json.RawMessage,
) (*driver.ClusterOperation, error) {
	return m.mutateClusterBR(arn, currentVersion, opTypeUpdateBrokerStorage, func(c *driver.Cluster) {
		if size, ok := targetVolumeSizeGB(body); ok {
			applyStorageVolume(c, size)
		}
	})
}

// UpdateBrokerType sets the modeled broker instance type.
func (m *Mock) UpdateBrokerType(
	_ context.Context, arn, currentVersion, targetType string,
) (*driver.ClusterOperation, error) {
	return m.mutateCluster(arn, currentVersion, opTypeUpdateBrokerType, func(c *driver.Cluster) {
		if c.BrokerNodeGroupInfo == nil {
			c.BrokerNodeGroupInfo = &driver.BrokerNodeGroupInfo{}
		}

		c.BrokerNodeGroupInfo.InstanceType = targetType
	})
}

// UpdateStorage applies a storage-mode/volume change, mutating the modeled
// storageMode when the request supplies one.
func (m *Mock) UpdateStorage(
	_ context.Context, arn, currentVersion string, body json.RawMessage,
) (*driver.ClusterOperation, error) {
	if err := validateStorageMode(stringField(body, "storageMode")); err != nil {
		return nil, err
	}

	return m.mutateCluster(arn, currentVersion, opTypeUpdateStorage, func(c *driver.Cluster) {
		if mode := stringField(body, "storageMode"); mode != "" {
			c.StorageMode = mode
		}

		if size, ok := int64Field(body, "volumeSizeGB"); ok {
			applyStorageVolume(c, size)
		}
	})
}

// UpdateClusterConfiguration carries the target configuration into raw options.
func (m *Mock) UpdateClusterConfiguration(
	_ context.Context, arn, currentVersion string, body json.RawMessage,
) (*driver.ClusterOperation, error) {
	return m.mutateCluster(arn, currentVersion, opTypeUpdateClusterConfig, func(c *driver.Cluster) {
		setRawOption(c, "configurationInfo", body)
	})
}

// UpdateClusterKafkaVersion sets the modeled Kafka version.
func (m *Mock) UpdateClusterKafkaVersion(
	_ context.Context, arn, currentVersion string, body json.RawMessage,
) (*driver.ClusterOperation, error) {
	return m.mutateCluster(arn, currentVersion, opTypeUpdateKafkaVersion, func(c *driver.Cluster) {
		if v := stringField(body, "targetKafkaVersion"); v != "" {
			c.KafkaVersion = v
		}
	})
}

// UpdateConnectivity carries the connectivity change into raw options.
func (m *Mock) UpdateConnectivity(
	_ context.Context, arn, currentVersion string, body json.RawMessage,
) (*driver.ClusterOperation, error) {
	return m.mutateCluster(arn, currentVersion, opTypeUpdateConnectivity, func(c *driver.Cluster) {
		setRawOption(c, "connectivityInfo", rawField(body, "connectivityInfo", body))
	})
}

// UpdateMonitoring sets the modeled enhancedMonitoring level and carries the
// open-monitoring/logging blocks into raw options.
func (m *Mock) UpdateMonitoring(
	_ context.Context, arn, currentVersion string, body json.RawMessage,
) (*driver.ClusterOperation, error) {
	if err := validateEnhancedMonitoring(stringField(body, "enhancedMonitoring")); err != nil {
		return nil, err
	}

	return m.mutateClusterBR(arn, currentVersion, opTypeUpdateMonitoring, func(c *driver.Cluster) {
		if lvl := stringField(body, "enhancedMonitoring"); lvl != "" {
			c.EnhancedMonitoring = lvl
		}

		setRawOption(c, "monitoringUpdate", body)
	})
}

// UpdateSecurity carries the security change into raw options.
func (m *Mock) UpdateSecurity(
	_ context.Context, arn, currentVersion string, body json.RawMessage,
) (*driver.ClusterOperation, error) {
	return m.mutateCluster(arn, currentVersion, opTypeUpdateSecurity, func(c *driver.Cluster) {
		setRawOption(c, "securityUpdate", body)
	})
}

// UpdateRebalancing carries the rebalancing change into raw options.
func (m *Mock) UpdateRebalancing(
	_ context.Context, arn, currentVersion string, body json.RawMessage,
) (*driver.ClusterOperation, error) {
	return m.mutateCluster(arn, currentVersion, opTypeUpdateRebalancing, func(c *driver.Cluster) {
		setRawOption(c, "rebalancing", rawField(body, "rebalancing", body))
	})
}

// RebootBroker records a reboot operation. It takes no currentVersion, so the
// version check is skipped; the version is still bumped for consistency.
func (m *Mock) RebootBroker(
	_ context.Context, arn string, _ []string,
) (*driver.ClusterOperation, error) {
	return m.mutateCluster(arn, "", opTypeRebootBroker, nil)
}

// setRawOption stores body under key in the cluster's RawOptions, allocating the
// map on first use. A nil body is ignored.
func setRawOption(c *driver.Cluster, key string, body json.RawMessage) {
	if len(body) == 0 {
		return
	}

	if c.RawOptions == nil {
		c.RawOptions = map[string]json.RawMessage{}
	}

	c.RawOptions[key] = append(json.RawMessage(nil), body...)
}

// stringField extracts a top-level string field from a JSON body, or "".
func stringField(body json.RawMessage, key string) string {
	if len(body) == 0 {
		return ""
	}

	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return ""
	}

	raw, ok := m[key]
	if !ok {
		return ""
	}

	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}

	return s
}

// int64Field extracts a top-level integer field from a JSON body, reporting
// whether one was present.
func int64Field(body json.RawMessage, key string) (int64, bool) {
	if len(body) == 0 {
		return 0, false
	}

	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return 0, false
	}

	raw, ok := m[key]
	if !ok {
		return 0, false
	}

	var n int64
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0, false
	}

	return n, true
}

// targetVolumeSizeGB extracts the requested EBS volume size from an
// UpdateBrokerStorage body's targetBrokerEBSVolumeInfo, taking the ALL-broker
// entry (the only value real MSK allows).
func targetVolumeSizeGB(body json.RawMessage) (int64, bool) {
	raw := rawField(body, "targetBrokerEBSVolumeInfo", nil)
	if len(raw) == 0 {
		return 0, false
	}

	var entries []struct {
		KafkaBrokerNodeID string `json:"kafkaBrokerNodeId"`
		VolumeSizeGB      *int64 `json:"volumeSizeGB"`
	}

	if err := json.Unmarshal(raw, &entries); err != nil {
		return 0, false
	}

	for _, e := range entries {
		if (e.KafkaBrokerNodeID == "ALL" || e.KafkaBrokerNodeID == "") && e.VolumeSizeGB != nil {
			return *e.VolumeSizeGB, true
		}
	}

	return 0, false
}

// applyStorageVolume rewrites brokerNodeGroupInfo.storageInfo.ebsStorageInfo.volumeSize
// to sizeGB, allocating the storageInfo/ebsStorageInfo blocks when absent and
// preserving any sibling fields (e.g. provisionedThroughput). A non-positive
// size or a cluster with no broker node group is a no-op.
func applyStorageVolume(c *driver.Cluster, sizeGB int64) {
	if sizeGB <= 0 || c.BrokerNodeGroupInfo == nil {
		return
	}

	bng := c.BrokerNodeGroupInfo

	storageInfo := decodeObject(bng.RawFields["storageInfo"])
	ebs := decodeObject(storageInfo["ebsStorageInfo"])

	size, _ := json.Marshal(sizeGB)
	ebs["volumeSize"] = size
	storageInfo["ebsStorageInfo"], _ = json.Marshal(ebs)

	if bng.RawFields == nil {
		bng.RawFields = map[string]json.RawMessage{}
	}

	bng.RawFields["storageInfo"], _ = json.Marshal(storageInfo)
}

// decodeObject unmarshals a raw JSON object into a field map, returning a fresh
// empty map when the input is absent or not an object.
func decodeObject(raw json.RawMessage) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	if len(raw) == 0 {
		return out
	}

	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]json.RawMessage{}
	}

	return out
}

// rawField returns the raw JSON of a top-level field, or fallback when absent.
func rawField(body json.RawMessage, key string, fallback json.RawMessage) json.RawMessage {
	if len(body) == 0 {
		return fallback
	}

	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return fallback
	}

	if raw, ok := m[key]; ok {
		return raw
	}

	return fallback
}
