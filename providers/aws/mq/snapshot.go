package mq

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/mq/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// mqSnapshot is the full serialized state of the Amazon MQ mock. The stores hold
// exported driver types, so they serialize directly, keyed by broker id and
// configuration id. The wired opts are not serialized.
type mqSnapshot struct {
	Brokers        map[string]driver.Broker        `json:"brokers,omitempty"`
	Configurations map[string]driver.Configuration `json:"configurations,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// MQ is control-plane only and holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := mqSnapshot{}

	if m.brokers.Len() > 0 {
		snap.Brokers = m.brokers.All()
	}

	if m.configs.Len() > 0 {
		snap.Configurations = m.configs.All()
	}

	b, err := json.Marshal(snap)
	if err != nil {
		return nil, fmt.Errorf("mq: snapshot: %w", err)
	}

	return b, nil
}

// Restore rebuilds the mock's state under the original identities: every broker
// id and configuration id (and the ARN and computed fields derived from them)
// is preserved, so a restore is transparent to clients.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap mqSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("mq: parse snapshot: %w", err)
	}

	for id := range snap.Brokers {
		m.brokers.Set(id, snap.Brokers[id])
	}

	for id := range snap.Configurations {
		m.configs.Set(id, snap.Configurations[id])
	}

	return nil
}
