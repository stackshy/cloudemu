package managedkafka

import (
	"context"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	mkdriver "github.com/stackshy/cloudemu/v2/services/managedkafka/driver"
)

// CreateTopic validates and stores a new topic under an existing cluster. Topic
// creation is synchronous in the real API (it returns the Topic, not an LRO).
func (m *Mock) CreateTopic(_ context.Context, t *mkdriver.Topic) (*mkdriver.Topic, error) {
	if err := validateTopicID(t.ID); err != nil {
		return nil, err
	}

	if err := validateTopic(t); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.clusters.Has(clusterName(t.Project, t.Location, t.ClusterID)) {
		return nil, clusterNotFound(t.Project, t.Location, t.ClusterID)
	}

	key := topicName(t.Project, t.Location, t.ClusterID, t.ID)
	if m.topics.Has(key) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "topic %q already exists", key)
	}

	stored := cloneTopic(t)
	m.topics.Set(key, stored)

	out := cloneTopic(&stored)

	return &out, nil
}

// GetTopic returns a topic by identity, cloned. A missing parent cluster is
// NOT_FOUND on the cluster.
func (m *Mock) GetTopic(_ context.Context, project, location, clusterID, id string) (*mkdriver.Topic, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.clusters.Has(clusterName(project, location, clusterID)) {
		return nil, clusterNotFound(project, location, clusterID)
	}

	t, ok := m.topics.Get(topicName(project, location, clusterID, id))
	if !ok {
		return nil, topicNotFound(project, location, clusterID, id)
	}

	out := cloneTopic(&t)

	return &out, nil
}

// ListTopics returns every topic in a cluster, ordered by name.
func (m *Mock) ListTopics(_ context.Context, project, location, clusterID string) ([]mkdriver.Topic, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	parent := clusterName(project, location, clusterID)
	if !m.clusters.Has(parent) {
		return nil, clusterNotFound(project, location, clusterID)
	}

	prefix := parent + "/" + topicsColl + "/"
	all := m.topics.SortedValues()
	out := make([]mkdriver.Topic, 0, len(all))

	for i := range all {
		if strings.HasPrefix(topicName(all[i].Project, all[i].Location, all[i].ClusterID, all[i].ID), prefix) {
			out = append(out, cloneTopic(&all[i]))
		}
	}

	return out, nil
}

// UpdateTopic applies the masked fields of t to the stored topic. partitionCount
// may only increase; replicationFactor is immutable.
func (m *Mock) UpdateTopic(_ context.Context, t *mkdriver.Topic, mask []string) (*mkdriver.Topic, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.clusters.Has(clusterName(t.Project, t.Location, t.ClusterID)) {
		return nil, clusterNotFound(t.Project, t.Location, t.ClusterID)
	}

	key := topicName(t.Project, t.Location, t.ClusterID, t.ID)

	stored, ok := m.topics.Get(key)
	if !ok {
		return nil, topicNotFound(t.Project, t.Location, t.ClusterID, t.ID)
	}

	next := cloneTopic(&stored)
	if err := applyTopicMask(&next, t, mask); err != nil {
		return nil, err
	}

	if next.PartitionCount < stored.PartitionCount {
		return nil, cerrors.Newf(cerrors.InvalidArgument,
			"partition_count can only be increased (current %d, requested %d)", stored.PartitionCount, next.PartitionCount)
	}

	if err := validateTopic(&next); err != nil {
		return nil, err
	}

	m.topics.Set(key, next)

	out := cloneTopic(&next)

	return &out, nil
}

// DeleteTopic removes a topic. The real API returns Empty synchronously.
func (m *Mock) DeleteTopic(_ context.Context, project, location, clusterID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.clusters.Has(clusterName(project, location, clusterID)) {
		return clusterNotFound(project, location, clusterID)
	}

	if !m.topics.Delete(topicName(project, location, clusterID, id)) {
		return topicNotFound(project, location, clusterID, id)
	}

	return nil
}

// topicNotFound builds the NOT_FOUND error carrying the full resource name.
func topicNotFound(project, location, clusterID, id string) error {
	return cerrors.Newf(cerrors.NotFound, "topic %q not found", topicName(project, location, clusterID, id))
}
