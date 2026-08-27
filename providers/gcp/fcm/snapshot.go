package fcm

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/notification/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// fcmSnapshot is the full serialized state of the FCM mock: every topic keyed by
// name. topicData holds a nested subscriptions store (fully-exported
// driver.SubscriptionInfo, round-tripped through the generic memstore helper) and
// a message log, both promoted to exported forms. The mutexes, the monitoring
// backend, and *config.Options are intentionally not captured.
type fcmSnapshot struct {
	Topics map[string]*topicSnapshot `json:"topics,omitempty"`
}

// topicSnapshot mirrors topicData, promoting its nested subscription store and
// its message log.
type topicSnapshot struct {
	Info          driver.TopicInfo   `json:"info"`
	Subscriptions json.RawMessage    `json:"subscriptions,omitempty"`
	Messages      []publishedMessage `json:"messages,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// FCM holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := fcmSnapshot{Topics: make(map[string]*topicSnapshot, m.topics.Len())}

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
		return nil, fmt.Errorf("fcm: snapshot subscriptions: %w", err)
	}

	ts := &topicSnapshot{Info: td.info, Subscriptions: subs}

	if len(td.messages) > 0 {
		ts.Messages = make([]publishedMessage, len(td.messages))
		copy(ts.Messages, td.messages)
	}

	return ts, nil
}

// Restore rebuilds every topic under its original name, subscriptions under
// their original ids.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap fcmSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("fcm: parse snapshot: %w", err)
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
	}

	if len(ts.Subscriptions) > 0 {
		if err := td.subscriptions.LoadSnapshot(ts.Subscriptions); err != nil {
			return nil, fmt.Errorf("fcm: restore subscriptions: %w", err)
		}
	}

	if len(ts.Messages) > 0 {
		td.messages = make([]publishedMessage, len(ts.Messages))
		copy(td.messages, ts.Messages)
	}

	return td, nil
}
