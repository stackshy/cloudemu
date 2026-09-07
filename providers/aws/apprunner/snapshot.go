package apprunner

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/apprunner/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// apprunnerSnapshot is the full serialized state of the App Runner mock. Every
// store holds exported driver types, so it serializes directly, keyed by the
// resource's ARN. The wired opts are not serialized.
type apprunnerSnapshot struct {
	Services      map[string]driver.Service                    `json:"services,omitempty"`
	AutoScaling   map[string]driver.AutoScalingConfiguration   `json:"autoScaling,omitempty"`
	Connections   map[string]driver.Connection                 `json:"connections,omitempty"`
	VpcConnectors map[string]driver.VpcConnector               `json:"vpcConnectors,omitempty"`
	Observability map[string]driver.ObservabilityConfiguration `json:"observability,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// App Runner is control-plane only and holds no bulk assets.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := apprunnerSnapshot{}

	if m.services.Len() > 0 {
		snap.Services = m.services.All()
	}

	if m.autoScaling.Len() > 0 {
		snap.AutoScaling = m.autoScaling.All()
	}

	if m.connections.Len() > 0 {
		snap.Connections = m.connections.All()
	}

	if m.vpcConnectors.Len() > 0 {
		snap.VpcConnectors = m.vpcConnectors.All()
	}

	if m.observability.Len() > 0 {
		snap.Observability = m.observability.All()
	}

	b, err := json.Marshal(snap)
	if err != nil {
		return nil, fmt.Errorf("apprunner: snapshot: %w", err)
	}

	return b, nil
}

// Restore rebuilds the mock's state under the original identities: every ARN
// (and the computed fields derived from it) is preserved, so a restore is
// transparent to clients.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap apprunnerSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("apprunner: parse snapshot: %w", err)
	}

	for arn := range snap.Services {
		m.services.Set(arn, snap.Services[arn])
	}

	for arn := range snap.AutoScaling {
		m.autoScaling.Set(arn, snap.AutoScaling[arn])
	}

	for arn := range snap.Connections {
		m.connections.Set(arn, snap.Connections[arn])
	}

	for arn := range snap.VpcConnectors {
		m.vpcConnectors.Set(arn, snap.VpcConnectors[arn])
	}

	for arn := range snap.Observability {
		m.observability.Set(arn, snap.Observability[arn])
	}

	return nil
}
