package sns

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/notification/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// snsSnapshot is the full serialized state of the SNS mock: every topic keyed by
// its name. topicData holds a nested subscriptions store (its value type is the
// fully-exported driver.SubscriptionInfo, so it round-trips through the generic
// memstore helper) plus a message log and an unexported unsubscribe counter,
// both promoted to exported forms. The mutexes, the wired SQS/Lambda deliverers,
// the monitoring backend, and *config.Options are intentionally not captured.
type snsSnapshot struct {
	Topics map[string]*topicSnapshot `json:"topics,omitempty"`
}

// topicSnapshot mirrors topicData, promoting its nested subscription store, its
// message log, and its unexported unsubscribe counter.
type topicSnapshot struct {
	Info          driver.TopicInfo   `json:"info"`
	Subscriptions json.RawMessage    `json:"subscriptions,omitempty"`
	Messages      []publishedMessage `json:"messages,omitempty"`
	Deleted       int                `json:"deleted,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// SNS holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := snsSnapshot{Topics: make(map[string]*topicSnapshot, m.topics.Len())}

	for name, td := range m.topics.All() {
		ts, err := snapshotTopic(td)
		if err != nil {
			return nil, err
		}

		snap.Topics[name] = ts
	}

	if len(snap.Topics) == 0 {
		snap.Topics = nil
	}

	return json.Marshal(snap)
}

func snapshotTopic(td *topicData) (*topicSnapshot, error) {
	td.mu.RLock()
	defer td.mu.RUnlock()

	subs, err := td.subscriptions.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("sns: snapshot subscriptions: %w", err)
	}

	ts := &topicSnapshot{Info: td.info, Subscriptions: subs, Deleted: td.deleted}

	if len(td.messages) > 0 {
		ts.Messages = make([]publishedMessage, len(td.messages))
		copy(ts.Messages, td.messages)
	}

	return ts, nil
}

// Restore rebuilds every topic under its original name, with its subscriptions
// (under their original ids), message log, and unsubscribe counter intact.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap snsSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("sns: parse snapshot: %w", err)
	}

	for name, ts := range snap.Topics {
		td, err := restoreTopic(ts)
		if err != nil {
			return err
		}

		m.topics.Set(name, td)
	}

	return nil
}

func restoreTopic(ts *topicSnapshot) (*topicData, error) {
	td := &topicData{
		info:          ts.Info,
		subscriptions: memstore.New[driver.SubscriptionInfo](),
		deleted:       ts.Deleted,
	}

	if len(ts.Subscriptions) > 0 {
		if err := td.subscriptions.LoadSnapshot(ts.Subscriptions); err != nil {
			return nil, fmt.Errorf("sns: restore subscriptions: %w", err)
		}
	}

	if len(ts.Messages) > 0 {
		td.messages = make([]publishedMessage, len(ts.Messages))
		copy(td.messages, ts.Messages)
	}

	return td, nil
}
