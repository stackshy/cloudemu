package notificationhubs

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/notification/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// nhSnapshot is the full serialized state of the Notification Hubs mock: every
// topic (hub) keyed by name, plus the Azure-only namespace metadata, SAS rules,
// registrations, and PNS credentials stores. topicData holds a nested
// subscriptions store (fully-exported driver.SubscriptionInfo) and a message log,
// both promoted; the four Azure stores hold exported driver value types and
// round-trip through the generic memstore helper. The mutexes, the monitoring
// backend, and *config.Options are intentionally not captured.
type nhSnapshot struct {
	Topics        map[string]*topicSnapshot `json:"topics,omitempty"`
	NSMeta        json.RawMessage           `json:"nsMeta,omitempty"`
	SASRules      json.RawMessage           `json:"sasRules,omitempty"`
	Registrations json.RawMessage           `json:"registrations,omitempty"`
	PNSCreds      json.RawMessage           `json:"pnsCreds,omitempty"`
}

// topicSnapshot mirrors topicData, promoting its nested subscription store and
// its message log.
type topicSnapshot struct {
	Info          driver.TopicInfo   `json:"info"`
	Subscriptions json.RawMessage    `json:"subscriptions,omitempty"`
	Messages      []publishedMessage `json:"messages,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// Notification Hubs holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := nhSnapshot{Topics: make(map[string]*topicSnapshot, m.topics.Len())}

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

	if err := m.snapshotAzureStores(&snap); err != nil {
		return nil, err
	}

	return json.Marshal(snap)
}

func (m *Mock) snapshotAzureStores(snap *nhSnapshot) error {
	dumps := []struct {
		dst *json.RawMessage
		fn  func() ([]byte, error)
	}{
		{&snap.NSMeta, m.nsMeta.Snapshot},
		{&snap.SASRules, m.sasRules.Snapshot},
		{&snap.Registrations, m.registrations.Snapshot},
		{&snap.PNSCreds, m.pnsCreds.Snapshot},
	}

	for _, d := range dumps {
		b, err := d.fn()
		if err != nil {
			return fmt.Errorf("notificationhubs: snapshot store: %w", err)
		}

		*d.dst = b
	}

	return nil
}

func snapshotTopic(td *topicData) (*topicSnapshot, error) {
	td.mu.RLock()
	defer td.mu.RUnlock()

	subs, err := td.subscriptions.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("notificationhubs: snapshot subscriptions: %w", err)
	}

	ts := &topicSnapshot{Info: td.info, Subscriptions: subs}

	if len(td.messages) > 0 {
		ts.Messages = make([]publishedMessage, len(td.messages))
		copy(ts.Messages, td.messages)
	}

	return ts, nil
}

// Restore rebuilds every topic under its original name (subscriptions under
// their original ids) and every Azure store under its original keys.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap nhSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("notificationhubs: parse snapshot: %w", err)
	}

	for name, ts := range snap.Topics {
		td, err := restoreTopic(ts)
		if err != nil {
			return err
		}

		m.topics.Set(name, td)
	}

	return m.restoreAzureStores(&snap)
}

func (m *Mock) restoreAzureStores(snap *nhSnapshot) error {
	loads := []struct {
		src json.RawMessage
		fn  func([]byte) error
	}{
		{snap.NSMeta, m.nsMeta.LoadSnapshot},
		{snap.SASRules, m.sasRules.LoadSnapshot},
		{snap.Registrations, m.registrations.LoadSnapshot},
		{snap.PNSCreds, m.pnsCreds.LoadSnapshot},
	}

	for _, l := range loads {
		if len(l.src) == 0 {
			continue
		}

		if err := l.fn(l.src); err != nil {
			return fmt.Errorf("notificationhubs: restore store: %w", err)
		}
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
			return nil, fmt.Errorf("notificationhubs: restore subscriptions: %w", err)
		}
	}

	if len(ts.Messages) > 0 {
		td.messages = make([]publishedMessage, len(ts.Messages))
		copy(td.messages, ts.Messages)
	}

	return td, nil
}
