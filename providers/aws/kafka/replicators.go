package kafka

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/kafka/driver"
)

// Replicator states.
const (
	replicatorStateRunning  = "RUNNING"
	replicatorStateDeleting = "DELETING"
)

// snapshotReplicator returns a deep copy of a stored replicator so a reader
// cannot alias its Tags or RawOptions maps.
//
//nolint:gocritic // hugeParam: takes a value by design to snapshot stored state.
func snapshotReplicator(r driver.Replicator) driver.Replicator {
	out := r
	out.Tags = copyTags(r.Tags)
	out.RawOptions = copyRaw(r.RawOptions)

	return out
}

// createReplicatorRequest is the modeled CreateReplicator body. The cluster and
// replication-info blocks round-trip verbatim through raw options.
type createReplicatorRequest struct {
	ReplicatorName          string            `json:"replicatorName"`
	ServiceExecutionRoleArn string            `json:"serviceExecutionRoleArn"`
	Description             string            `json:"description"`
	KafkaClusters           json.RawMessage   `json:"kafkaClusters"`
	ReplicationInfoList     json.RawMessage   `json:"replicationInfoList"`
	Tags                    map[string]string `json:"tags"`
}

// CreateReplicator mints a replicator in the RUNNING state. The name is unique
// per account: the store is keyed by ARN, so a create-mutex serializes the
// name-scan and insert against a concurrent duplicate-name create.
func (m *Mock) CreateReplicator(_ context.Context, body json.RawMessage) (*driver.Replicator, error) {
	var req createReplicatorRequest
	if len(body) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, badRequest("invalid CreateReplicator body: %v", err)
		}
	}

	if req.ReplicatorName == "" {
		return nil, badRequest("replicatorName is required")
	}

	m.createMu.Lock()
	defer m.createMu.Unlock()

	if m.replicatorNameTaken(req.ReplicatorName) {
		return nil, conflict("replicator already exists: %s", req.ReplicatorName)
	}

	rep := driver.Replicator{
		ReplicatorARN:  m.replicatorARN(req.ReplicatorName),
		ReplicatorName: req.ReplicatorName,
		State:          replicatorStateRunning,
		CreationTime:   m.now(),
		Tags:           copyTags(req.Tags),
		RawOptions:     replicatorRawOptions(req),
	}

	m.replicators.Set(rep.ReplicatorARN, &replicatorData{
		replicator: rep,
		version:    "KRP" + idgen.GenerateID(""),
	})

	out := snapshotReplicator(rep)

	return &out, nil
}

// replicatorNameTaken reports whether a replicator with the given name exists.
// The caller must hold createMu.
func (m *Mock) replicatorNameTaken(name string) bool {
	for _, rd := range m.replicators.SortedValues() {
		rd.mu.RLock()
		match := rd.replicator.ReplicatorName == name
		rd.mu.RUnlock()

		if match {
			return true
		}
	}

	return false
}

// replicatorRawOptions carries the unmodeled cluster/replication-info/role
// blocks into the replicator's raw options so a Describe reflects them.
//
//nolint:gocritic // hugeParam: rendered from a decoded request copy.
func replicatorRawOptions(req createReplicatorRequest) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}

	if len(req.KafkaClusters) > 0 {
		out["kafkaClusters"] = append(json.RawMessage(nil), req.KafkaClusters...)
	}

	if len(req.ReplicationInfoList) > 0 {
		out["replicationInfoList"] = append(json.RawMessage(nil), req.ReplicationInfoList...)
	}

	if req.ServiceExecutionRoleArn != "" {
		out["serviceExecutionRoleArn"], _ = json.Marshal(req.ServiceExecutionRoleArn)
	}

	if req.Description != "" {
		out["replicatorDescription"], _ = json.Marshal(req.Description)
	}

	if len(out) == 0 {
		return nil
	}

	return out
}

// getReplicator resolves a replicator by ARN, NotFoundException when absent.
func (m *Mock) getReplicator(arn string) (*replicatorData, error) {
	rd, ok := m.replicators.Get(arn)
	if !ok {
		return nil, notFound("replicator not found: %s", arn)
	}

	return rd, nil
}

// DescribeReplicator returns a deep copy of a stored replicator, exposing its
// current optimistic-concurrency version through raw options.
func (m *Mock) DescribeReplicator(_ context.Context, arn string) (*driver.Replicator, error) {
	rd, err := m.getReplicator(arn)
	if err != nil {
		return nil, err
	}

	rd.mu.RLock()
	defer rd.mu.RUnlock()

	out := snapshotReplicator(rd.replicator)
	setReplicatorVersion(&out, rd.version)

	return &out, nil
}

// ListReplicators lists replicators (optionally filtered by name prefix) sorted
// by ARN, paginated.
func (m *Mock) ListReplicators(
	_ context.Context, namePrefix string, page driver.Page,
) (reps []driver.Replicator, next string, err error) {
	vals := m.replicators.SortedValues()

	all := make([]driver.Replicator, 0, len(vals))

	for _, rd := range vals {
		rd.mu.RLock()
		snap := snapshotReplicator(rd.replicator)
		setReplicatorVersion(&snap, rd.version)
		rd.mu.RUnlock()

		if namePrefix != "" && !strings.HasPrefix(snap.ReplicatorName, namePrefix) {
			continue
		}

		all = append(all, snap)
	}

	start, end, nextTok, err := m.paginate(len(all), page)
	if err != nil {
		return nil, "", err
	}

	return all[start:end], nextTok, nil
}

// DeleteReplicator removes a replicator and returns its terminal state.
func (m *Mock) DeleteReplicator(_ context.Context, arn, _ string) (arnOut, state string, err error) {
	rd, err := m.getReplicator(arn)
	if err != nil {
		return "", "", err
	}

	rd.mu.Lock()
	rd.replicator.State = replicatorStateDeleting
	rd.mu.Unlock()

	m.replicators.Delete(arn)

	return arn, replicatorStateDeleting, nil
}

// updateReplicationInfoRequest is the modeled UpdateReplicationInfo body.
type updateReplicationInfoRequest struct {
	CurrentVersion           string          `json:"currentVersion"`
	SourceKafkaClusterArn    string          `json:"sourceKafkaClusterArn"`
	TargetKafkaClusterArn    string          `json:"targetKafkaClusterArn"`
	ConsumerGroupReplication json.RawMessage `json:"consumerGroupReplication"`
	TopicReplication         json.RawMessage `json:"topicReplication"`
}

// UpdateReplicationInfo mutates the replication-info for a source/target pair.
// A supplied CurrentVersion must match the stored version (optimistic
// concurrency) or the call is a BadRequestException; the version is bumped on
// success.
func (m *Mock) UpdateReplicationInfo(
	_ context.Context, arn string, body json.RawMessage,
) (*driver.Replicator, error) {
	rd, err := m.getReplicator(arn)
	if err != nil {
		return nil, err
	}

	var req updateReplicationInfoRequest
	if len(body) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, badRequest("invalid UpdateReplicationInfo body: %v", err)
		}
	}

	rd.mu.Lock()
	defer rd.mu.Unlock()

	if req.CurrentVersion != "" && req.CurrentVersion != rd.version {
		return nil, badRequest(
			"currentVersion %q does not match replicator version %q", req.CurrentVersion, rd.version)
	}

	applyReplicationUpdate(&rd.replicator, req)
	rd.version = "KRP" + idgen.GenerateID("")

	out := snapshotReplicator(rd.replicator)
	setReplicatorVersion(&out, rd.version)

	return &out, nil
}

// applyReplicationUpdate records the requested per-pair replication overrides
// into the replicator's raw options under a source→target key.
//
//nolint:gocritic // hugeParam: takes the decoded request by value.
func applyReplicationUpdate(rep *driver.Replicator, req updateReplicationInfoRequest) {
	if rep.RawOptions == nil {
		rep.RawOptions = map[string]json.RawMessage{}
	}

	key := "replicationUpdate:" + req.SourceKafkaClusterArn + "->" + req.TargetKafkaClusterArn

	update := map[string]json.RawMessage{}
	if len(req.TopicReplication) > 0 {
		update["topicReplication"] = req.TopicReplication
	}

	if len(req.ConsumerGroupReplication) > 0 {
		update["consumerGroupReplication"] = req.ConsumerGroupReplication
	}

	if b, err := json.Marshal(update); err == nil {
		rep.RawOptions[key] = b
	}
}

// setReplicatorVersion records the current optimistic-concurrency version in a
// snapshot's raw options so a describe/list caller sees currentVersion.
func setReplicatorVersion(rep *driver.Replicator, version string) {
	if version == "" {
		return
	}

	if rep.RawOptions == nil {
		rep.RawOptions = map[string]json.RawMessage{}
	}

	rep.RawOptions["currentVersion"], _ = json.Marshal(version)
}
