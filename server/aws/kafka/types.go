package kafka

import (
	"encoding/json"
	"time"

	"github.com/stackshy/cloudemu/v2/services/kafka/driver"
)

// timeRFC3339 renders a time as the ISO-8601 string MSK deserializers parse via
// smithytime.ParseDateTime; a zero time renders as null.
func timeRFC3339(t time.Time) any {
	if t.IsZero() {
		return nil
	}

	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}

// mergeRaw marshals base, then overlays any raw option blocks so unmodeled
// blocks appear alongside the modeled fields in the response.
func mergeRaw(base map[string]any, raw map[string]json.RawMessage) map[string]json.RawMessage {
	b, _ := json.Marshal(base)

	var out map[string]json.RawMessage
	_ = json.Unmarshal(b, &out)

	for k, v := range raw {
		if _, taken := out[k]; taken {
			continue
		}

		out[k] = v
	}

	return out
}

// overlayRaw copies raw[srcKey] into out[dstKey] as its decoded JSON value when
// present, so a stored raw block surfaces under the response's typed field name.
func overlayRaw(out map[string]any, raw map[string]json.RawMessage, srcKey, dstKey string) {
	v, ok := raw[srcKey]
	if !ok || len(v) == 0 {
		return
	}

	out[dstKey] = v
}

// brokerNodeGroupInfoJSON is the wire shape of a broker node group.
type brokerNodeGroupInfoJSON struct {
	ClientSubnets        []string `json:"clientSubnets,omitempty"`
	InstanceType         string   `json:"instanceType,omitempty"`
	BrokerAZDistribution string   `json:"brokerAZDistribution,omitempty"`
	SecurityGroups       []string `json:"securityGroups,omitempty"`
	ZoneIDs              []string `json:"zoneIds,omitempty"`
}

// bngToDriver converts a wire broker node group to the driver type, carrying
// any unmodeled sub-block (storageInfo, connectivityInfo) as a raw field.
func bngToDriver(raw json.RawMessage) *driver.BrokerNodeGroupInfo {
	if len(raw) == 0 {
		return nil
	}

	var wire brokerNodeGroupInfoJSON
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil
	}

	return &driver.BrokerNodeGroupInfo{
		ClientSubnets:        wire.ClientSubnets,
		InstanceType:         wire.InstanceType,
		BrokerAZDistribution: wire.BrokerAZDistribution,
		SecurityGroups:       wire.SecurityGroups,
		ZoneIDs:              wire.ZoneIDs,
		RawFields:            rawFieldsExcept(raw, modeledBNGFields()),
	}
}

func modeledBNGFields() map[string]struct{} {
	return map[string]struct{}{
		"clientSubnets": {}, "instanceType": {}, "brokerAZDistribution": {},
		"securityGroups": {}, "zoneIds": {},
	}
}

// bngToWire renders a driver broker node group as its wire map, overlaying any
// unmodeled raw fields.
func bngToWire(b *driver.BrokerNodeGroupInfo) map[string]json.RawMessage {
	if b == nil {
		return nil
	}

	base := map[string]any{
		"clientSubnets":        b.ClientSubnets,
		"instanceType":         b.InstanceType,
		"brokerAZDistribution": b.BrokerAZDistribution,
		"securityGroups":       b.SecurityGroups,
		"zoneIds":              b.ZoneIDs,
	}

	return mergeRaw(base, b.RawFields)
}

// clusterToWire renders a driver cluster as the clusterInfo wire shape,
// overlaying any unmodeled top-level option blocks.
func clusterToWire(c *driver.Cluster) map[string]json.RawMessage {
	base := map[string]any{
		"clusterArn":          c.ClusterARN,
		"clusterName":         c.ClusterName,
		"clusterType":         c.ClusterType,
		"state":               c.State,
		"currentVersion":      c.CurrentVersion,
		"numberOfBrokerNodes": c.NumberOfBrokerNodes,
		"creationTime":        timeRFC3339(c.CreationTime),
	}

	if c.KafkaVersion != "" {
		base["currentBrokerSoftwareInfo"] = map[string]any{"kafkaVersion": c.KafkaVersion}
	}

	if c.StorageMode != "" {
		base["storageMode"] = c.StorageMode
	}

	if c.EnhancedMonitoring != "" {
		base["enhancedMonitoring"] = c.EnhancedMonitoring
	}

	if c.Tags != nil {
		base["tags"] = c.Tags
	}

	if c.BrokerNodeGroupInfo != nil {
		base["brokerNodeGroupInfo"] = bngToWire(c.BrokerNodeGroupInfo)
	}

	return mergeRaw(base, c.RawOptions)
}

// clusterToWireV2 renders a driver cluster as the v2 clusterInfo (types.Cluster)
// wire shape: a provisioned or serverless block nested under the common
// top-level fields. A v1-created cluster renders here too (same store).
func clusterToWireV2(c *driver.Cluster) map[string]json.RawMessage {
	base := map[string]any{
		"clusterArn":     c.ClusterARN,
		"clusterName":    c.ClusterName,
		"clusterType":    c.ClusterType,
		"state":          c.State,
		"currentVersion": c.CurrentVersion,
		"creationTime":   timeRFC3339(c.CreationTime),
	}

	if c.Tags != nil {
		base["tags"] = c.Tags
	}

	if c.ClusterType == driver.ClusterTypeServerless {
		base["serverless"] = serverlessBlock(c)
	} else {
		base["provisioned"] = provisionedBlock(c)
	}

	return mergeRaw(base, c.RawOptions)
}

// provisionedBlock renders the v2 "provisioned" sub-object of a cluster.
func provisionedBlock(c *driver.Cluster) map[string]any {
	block := map[string]any{
		"numberOfBrokerNodes": c.NumberOfBrokerNodes,
	}

	if c.KafkaVersion != "" {
		block["currentBrokerSoftwareInfo"] = map[string]any{"kafkaVersion": c.KafkaVersion}
	}

	if c.StorageMode != "" {
		block["storageMode"] = c.StorageMode
	}

	if c.EnhancedMonitoring != "" {
		block["enhancedMonitoring"] = c.EnhancedMonitoring
	}

	if c.BrokerNodeGroupInfo != nil {
		block["brokerNodeGroupInfo"] = bngToWire(c.BrokerNodeGroupInfo)
	}

	return block
}

// serverlessBlock renders the v2 "serverless" sub-object, promoting the stored
// raw serverless block when present.
func serverlessBlock(c *driver.Cluster) json.RawMessage {
	if raw, ok := c.RawOptions["serverless"]; ok && len(raw) > 0 {
		return raw
	}

	return json.RawMessage("{}")
}

// operationToWire renders a cluster operation as the ClusterOperationInfo wire
// shape (v1). endTime equals creationTime since the emulator completes
// operations instantly.
//
//nolint:gocritic // hugeParam: rendered from a value copy.
func operationToWire(op driver.ClusterOperation) map[string]any {
	return map[string]any{
		"clusterArn":     op.ClusterARN,
		"operationArn":   op.OperationARN,
		"operationState": op.OperationState,
		"operationType":  op.OperationType,
		"creationTime":   timeRFC3339(op.CreationTime),
		"endTime":        timeRFC3339(op.CreationTime),
	}
}

// operationToWireV2 renders a cluster operation as the ClusterOperationV2 /
// summary wire shape, carrying the cluster type.
//
//nolint:gocritic // hugeParam: rendered from a value copy.
func operationToWireV2(op driver.ClusterOperation, clusterType string) map[string]any {
	return map[string]any{
		"clusterArn":     op.ClusterARN,
		"clusterType":    clusterType,
		"operationArn":   op.OperationARN,
		"operationState": op.OperationState,
		"operationType":  op.OperationType,
		"startTime":      timeRFC3339(op.CreationTime),
		"endTime":        timeRFC3339(op.CreationTime),
	}
}

// nodeToWire renders a broker node as the NodeInfo wire shape.
func nodeToWire(n driver.Node) map[string]json.RawMessage {
	base := map[string]any{
		"nodeARN":      n.NodeARN,
		"nodeType":     n.NodeType,
		"instanceType": n.InstanceType,
	}

	return mergeRaw(base, n.RawOptions)
}

// revisionToWire renders a configuration revision as its wire shape.
func revisionToWire(r driver.ConfigurationRevision) map[string]any {
	return map[string]any{
		"revision":     r.Revision,
		"description":  r.Description,
		"creationTime": timeRFC3339(r.CreationTime),
	}
}

// configToWire renders a driver configuration as the Configuration wire shape.
func configToWire(c *driver.Configuration) map[string]any {
	return map[string]any{
		"arn":            c.ARN,
		"name":           c.Name,
		"description":    c.Description,
		"state":          c.State,
		"kafkaVersions":  c.KafkaVersions,
		"creationTime":   timeRFC3339(c.CreationTime),
		"latestRevision": revisionToWire(c.LatestRevision),
	}
}

// createClusterRequest is the CreateCluster (v1) request body. Modeled fields
// are promoted; brokerNodeGroupInfo and every other block round-trip raw.
type createClusterRequest struct {
	ClusterName         string            `json:"clusterName"`
	KafkaVersion        string            `json:"kafkaVersion"`
	NumberOfBrokerNodes int32             `json:"numberOfBrokerNodes"`
	BrokerNodeGroupInfo json.RawMessage   `json:"brokerNodeGroupInfo"`
	StorageMode         string            `json:"storageMode"`
	EnhancedMonitoring  string            `json:"enhancedMonitoring"`
	Tags                map[string]string `json:"tags"`
}

// modeledClusterFields lists the top-level blocks promoted to typed driver
// fields; every other key round-trips as a raw option.
func modeledClusterFields() map[string]struct{} {
	return map[string]struct{}{
		"clusterName": {}, "kafkaVersion": {}, "numberOfBrokerNodes": {},
		"brokerNodeGroupInfo": {}, "storageMode": {}, "enhancedMonitoring": {},
		"tags": {},
	}
}

// createConfigurationRequest is the CreateConfiguration request body. The
// SDK base64-encodes serverProperties on the wire; encoding/json decodes a
// JSON string of base64 into []byte automatically.
type createConfigurationRequest struct {
	Name             string   `json:"name"`
	Description      string   `json:"description"`
	KafkaVersions    []string `json:"kafkaVersions"`
	ServerProperties []byte   `json:"serverProperties"`
}

// updateConfigurationRequest is the UpdateConfiguration request body.
type updateConfigurationRequest struct {
	Description      string `json:"description"`
	ServerProperties []byte `json:"serverProperties"`
}

// rawFieldsExcept returns every top-level key of body not in the modeled set,
// so unmodeled blocks round-trip. Returns nil when none remain.
func rawFieldsExcept(body []byte, modeled map[string]struct{}) map[string]json.RawMessage {
	var all map[string]json.RawMessage
	if err := json.Unmarshal(body, &all); err != nil {
		return nil
	}

	out := make(map[string]json.RawMessage)

	for k, v := range all {
		if _, skip := modeled[k]; skip {
			continue
		}

		out[k] = v
	}

	if len(out) == 0 {
		return nil
	}

	return out
}
