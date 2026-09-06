package applicationgateway

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

// Compile-time check that Mock is persistable.
var _ snapshot.Snapshottable = (*Mock)(nil)

// gwSnapshot is the full serialized state of the Application Gateway mock. The
// single store holds a fully-exported driver value type (generic-JSON maps
// included) and round-trips through the generic memstore helper; the mutex and
// *config.Options are intentionally not captured.
type gwSnapshot struct {
	Gateways json.RawMessage `json:"gateways,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// the gateway mock holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	b, err := m.gws.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("azure application gateway: snapshot store: %w", err)
	}

	return json.Marshal(gwSnapshot{Gateways: b})
}

// Restore rebuilds the mock's state under the original identities: every
// resource-group/name key round-trips unchanged.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap gwSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("azure application gateway: parse snapshot: %w", err)
	}

	if len(snap.Gateways) == 0 {
		return nil
	}

	if err := m.gws.LoadSnapshot(snap.Gateways); err != nil {
		return fmt.Errorf("azure application gateway: restore store: %w", err)
	}

	return nil
}
