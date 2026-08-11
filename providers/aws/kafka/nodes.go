package kafka

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/stackshy/cloudemu/v2/services/kafka/driver"
)

// ListNodes returns one synthetic broker node per broker in the cluster, sized
// to the cluster's NumberOfBrokerNodes. Broker IDs are 1..N; endpoints and the
// client subnet are derived deterministically from the cluster name.
func (m *Mock) ListNodes(
	_ context.Context, arn string, page driver.Page,
) (nodes []driver.Node, next string, err error) {
	cd, err := m.getCluster(arn)
	if err != nil {
		return nil, "", err
	}

	cd.mu.RLock()
	c := snapshotCluster(cd.cluster)
	cd.mu.RUnlock()

	all := m.synthNodes(&c)

	start, end, nextTok, err := m.paginate(len(all), page)
	if err != nil {
		return nil, "", err
	}

	return all[start:end], nextTok, nil
}

// synthNodes builds the modeled broker-node list for a cluster.
func (m *Mock) synthNodes(c *driver.Cluster) []driver.Node {
	count := int(c.NumberOfBrokerNodes)
	if count < 0 {
		count = 0
	}

	instance := "kafka.m5.large"
	if c.BrokerNodeGroupInfo != nil && c.BrokerNodeGroupInfo.InstanceType != "" {
		instance = c.BrokerNodeGroupInfo.InstanceType
	}

	nodes := make([]driver.Node, 0, count)

	for i := 1; i <= count; i++ {
		id := strconv.Itoa(i)
		endpoint := "b-" + id + "." + c.ClusterName + ".kafka." + m.opts.Region + ".amazonaws.com"

		nodes = append(nodes, driver.Node{
			NodeARN:      c.ClusterARN + "/node/" + id,
			NodeType:     "BROKER",
			InstanceType: instance,
			RawOptions: map[string]json.RawMessage{
				"brokerNodeInfo": brokerNodeInfoJSON(i, endpoint, c.KafkaVersion),
			},
		})
	}

	return nodes
}

// brokerNodeInfoJSON builds the raw brokerNodeInfo block for a synthetic node.
// brokerId is a JSON number (the SDK models it as *float64).
func brokerNodeInfoJSON(brokerID int, endpoint, kafkaVersion string) json.RawMessage {
	info := map[string]any{
		"brokerId":     brokerID,
		"clientSubnet": "subnet-" + strconv.Itoa(brokerID),
		"endpoints":    []string{endpoint},
	}

	if kafkaVersion != "" {
		info["currentBrokerSoftwareInfo"] = map[string]any{"kafkaVersion": kafkaVersion}
	}

	b, err := json.Marshal(info)
	if err != nil {
		return json.RawMessage("{}")
	}

	return b
}
