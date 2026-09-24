package notifications

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// notificationsSnapshot is the full serialized state of the OCI Notifications
// mock. Each store is dumped keyed by its resource OCID, so a subscription's
// TopicID and a delivery's subscription key still resolve after a restore. A
// PENDING subscription keeps its ConfirmationToken, so a confirmation issued
// before the restart still lands.
//
// Deliveries are snapshotted rather than dropped: they are the emulator's
// stand-in for the endpoint inbox real ONS pushes to, and the only thing
// Deliveries reads. Losing them on restart would report an ACTIVE subscription
// as having received nothing, which is silent data loss rather than
// transience.
//
// topicData is unexported but every field on it is exported, as is every field
// of Subscription and Message, so all three stores round-trip through the
// generic memstore helper. The mutex, *config.Options and the monitoring
// backend are wiring, not state, and are not serialized.
type notificationsSnapshot struct {
	Topics        json.RawMessage `json:"topics,omitempty"`
	Subscriptions json.RawMessage `json:"subscriptions,omitempty"`
	Deliveries    json.RawMessage `json:"deliveries,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// Notifications holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var snap notificationsSnapshot

	for _, d := range m.snapshotDumps(&snap) {
		b, err := d.fn()
		if err != nil {
			return nil, fmt.Errorf("notifications: snapshot store: %w", err)
		}

		*d.dst = b
	}

	return json.Marshal(snap)
}

// Restore rebuilds the mock's state under the original identities: every OCID,
// etag and confirmation token is preserved.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap notificationsSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("notifications: parse snapshot: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, d := range m.snapshotDumps(&snap) {
		if len(*d.dst) == 0 {
			continue
		}

		if err := d.load(*d.dst); err != nil {
			return fmt.Errorf("notifications: restore store: %w", err)
		}
	}

	return nil
}

// storeDump pairs a snapshot field with its store's dump and load functions, so
// Snapshot and Restore share one table and cannot drift apart.
type storeDump struct {
	dst  *json.RawMessage
	fn   func() ([]byte, error)
	load func([]byte) error
}

// snapshotDumps lists every store alongside the snapshot field it maps to.
func (m *Mock) snapshotDumps(snap *notificationsSnapshot) []storeDump {
	return []storeDump{
		{&snap.Topics, m.topics.Snapshot, m.topics.LoadSnapshot},
		{&snap.Subscriptions, m.subs.Snapshot, m.subs.LoadSnapshot},
		{&snap.Deliveries, m.deliveries.Snapshot, m.deliveries.LoadSnapshot},
	}
}
