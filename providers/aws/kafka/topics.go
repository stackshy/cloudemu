package kafka

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/stackshy/cloudemu/v2/services/kafka/driver"
)

const defaultReplicationFactor = 3

// snapshotTopic returns a deep copy of a stored topic so a reader cannot alias
// its RawOptions map.
func snapshotTopic(t driver.Topic) driver.Topic {
	out := t
	out.RawOptions = copyRaw(t.RawOptions)

	return out
}

// createTopicRequest is the modeled CreateTopic body.
type createTopicRequest struct {
	TopicName         string          `json:"topicName"`
	PartitionCount    int32           `json:"partitionCount"`
	ReplicationFactor int32           `json:"replicationFactor"`
	Configs           json.RawMessage `json:"configs"`
}

// CreateTopic adds a topic to a cluster, keyed by name. It resolves the cluster
// first (NotFoundException when absent) and claims the name atomically under the
// cluster lock (ConflictException on a duplicate).
func (m *Mock) CreateTopic(_ context.Context, clusterARN string, body json.RawMessage) (*driver.Topic, error) {
	cd, err := m.getCluster(clusterARN)
	if err != nil {
		return nil, err
	}

	var req createTopicRequest
	if len(body) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, badRequest("invalid CreateTopic body: %v", err)
		}
	}

	if req.TopicName == "" {
		return nil, badRequest("topicName is required")
	}

	rf := req.ReplicationFactor
	if rf <= 0 {
		rf = defaultReplicationFactor
	}

	topic := driver.Topic{
		TopicName:          req.TopicName,
		NumberOfPartitions: req.PartitionCount,
		ReplicationFactor:  rf,
		RawOptions:         topicConfigs(req.Configs),
	}

	cd.mu.Lock()
	defer cd.mu.Unlock()

	if cd.topics == nil {
		cd.topics = map[string]driver.Topic{}
	}

	if _, dup := cd.topics[req.TopicName]; dup {
		return nil, conflict("topic already exists: %s", req.TopicName)
	}

	cd.topics[req.TopicName] = topic

	out := snapshotTopic(topic)

	return &out, nil
}

// topicConfigs carries a non-empty configs blob into the topic's raw options.
func topicConfigs(configs json.RawMessage) map[string]json.RawMessage {
	if len(configs) == 0 {
		return nil
	}

	return map[string]json.RawMessage{"configs": append(json.RawMessage(nil), configs...)}
}

// getTopic resolves a topic within a cluster, holding no lock. The caller must
// hold cd.mu appropriately; getTopic itself only reads under a passed lock.
func (cd *clusterData) getTopic(name string) (driver.Topic, bool) {
	t, ok := cd.topics[name]

	return t, ok
}

// DescribeTopic returns a deep copy of a stored topic.
func (m *Mock) DescribeTopic(_ context.Context, clusterARN, topicName string) (*driver.Topic, error) {
	cd, err := m.getCluster(clusterARN)
	if err != nil {
		return nil, err
	}

	cd.mu.RLock()
	defer cd.mu.RUnlock()

	t, ok := cd.getTopic(topicName)
	if !ok {
		return nil, notFound("topic not found: %s", topicName)
	}

	out := snapshotTopic(t)

	return &out, nil
}

// ListTopics lists a cluster's topics sorted by name, paginated.
func (m *Mock) ListTopics(
	_ context.Context, clusterARN string, page driver.Page,
) (topics []driver.Topic, next string, err error) {
	cd, err := m.getCluster(clusterARN)
	if err != nil {
		return nil, "", err
	}

	cd.mu.RLock()
	all := make([]driver.Topic, 0, len(cd.topics))

	for name := range cd.topics {
		all = append(all, snapshotTopic(cd.topics[name]))
	}

	cd.mu.RUnlock()

	sort.Slice(all, func(i, j int) bool { return all[i].TopicName < all[j].TopicName })

	start, end, nextTok, err := m.paginate(len(all), page)
	if err != nil {
		return nil, "", err
	}

	return all[start:end], nextTok, nil
}

// updateTopicRequest is the modeled UpdateTopic body.
type updateTopicRequest struct {
	PartitionCount int32           `json:"partitionCount"`
	Configs        json.RawMessage `json:"configs"`
}

// UpdateTopic mutates a topic's partition count and/or configs and returns the
// updated topic.
func (m *Mock) UpdateTopic(
	_ context.Context, clusterARN, topicName string, body json.RawMessage,
) (*driver.Topic, error) {
	cd, err := m.getCluster(clusterARN)
	if err != nil {
		return nil, err
	}

	var req updateTopicRequest
	if len(body) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, badRequest("invalid UpdateTopic body: %v", err)
		}
	}

	cd.mu.Lock()
	defer cd.mu.Unlock()

	t, ok := cd.getTopic(topicName)
	if !ok {
		return nil, notFound("topic not found: %s", topicName)
	}

	if req.PartitionCount > 0 {
		t.NumberOfPartitions = req.PartitionCount
	}

	if len(req.Configs) > 0 {
		t.RawOptions = topicConfigs(req.Configs)
	}

	cd.topics[topicName] = t

	out := snapshotTopic(t)

	return &out, nil
}

// DeleteTopic removes a topic from a cluster.
func (m *Mock) DeleteTopic(_ context.Context, clusterARN, topicName string) error {
	cd, err := m.getCluster(clusterARN)
	if err != nil {
		return err
	}

	cd.mu.Lock()
	defer cd.mu.Unlock()

	if _, ok := cd.getTopic(topicName); !ok {
		return notFound("topic not found: %s", topicName)
	}

	delete(cd.topics, topicName)

	return nil
}

// DescribeTopicPartitions returns a synthetic-but-well-formed partition list
// sized to the topic's partition count. Each partition gets a deterministic
// round-robin leader and replica set derived from the replication factor.
func (m *Mock) DescribeTopicPartitions(
	_ context.Context, clusterARN, topicName string, page driver.Page,
) (partitions []json.RawMessage, next string, err error) {
	cd, err := m.getCluster(clusterARN)
	if err != nil {
		return nil, "", err
	}

	cd.mu.RLock()
	t, ok := cd.getTopic(topicName)
	cd.mu.RUnlock()

	if !ok {
		return nil, "", notFound("topic not found: %s", topicName)
	}

	all := synthesizePartitions(t)

	start, end, nextTok, err := m.paginate(len(all), page)
	if err != nil {
		return nil, "", err
	}

	return all[start:end], nextTok, nil
}

// synthesizePartitions builds a deterministic TopicPartitionInfo list for a
// topic: replicas are RF consecutive brokers (1-based, round-robin from the
// partition id) and the leader is the first replica.
func synthesizePartitions(t driver.Topic) []json.RawMessage {
	rf := int(t.ReplicationFactor)
	if rf <= 0 {
		rf = 1
	}

	out := make([]json.RawMessage, 0, t.NumberOfPartitions)

	for p := int32(0); p < t.NumberOfPartitions; p++ {
		replicas := make([]int32, rf)
		for i := 0; i < rf; i++ {
			replicas[i] = int32((int(p)+i)%rf + 1) //nolint:gosec // small broker ids, no overflow.
		}

		info := map[string]any{
			"partition": p,
			"leader":    replicas[0],
			"replicas":  replicas,
			"isr":       replicas,
		}

		b, err := json.Marshal(info)
		if err != nil {
			continue
		}

		out = append(out, b)
	}

	return out
}
