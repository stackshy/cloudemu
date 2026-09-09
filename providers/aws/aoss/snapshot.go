package aoss

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/aoss/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// aossSnapshot is the full serialized state of the OpenSearch Serverless mock.
// The stores hold exported driver types, so they serialize directly, keyed by
// collection id and policy "type/name". The wired opts are not serialized.
type aossSnapshot struct {
	Collections      map[string]driver.Collection `json:"collections,omitempty"`
	SecurityPolicies map[string]driver.Policy     `json:"securityPolicies,omitempty"`
	AccessPolicies   map[string]driver.Policy     `json:"accessPolicies,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// OpenSearch Serverless is control-plane only and holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := aossSnapshot{}

	if m.collections.Len() > 0 {
		snap.Collections = m.collections.All()
	}

	if m.securityPolicies.Len() > 0 {
		snap.SecurityPolicies = m.securityPolicies.All()
	}

	if m.accessPolicies.Len() > 0 {
		snap.AccessPolicies = m.accessPolicies.All()
	}

	b, err := json.Marshal(snap)
	if err != nil {
		return nil, fmt.Errorf("aoss: snapshot: %w", err)
	}

	return b, nil
}

// Restore rebuilds the mock's state under the original identities: every
// collection id (and the arn and endpoints derived from it) and every policy
// version is preserved, so a restore is transparent to clients.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap aossSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("aoss: parse snapshot: %w", err)
	}

	for id := range snap.Collections {
		m.collections.Set(id, snap.Collections[id])
	}

	for key := range snap.SecurityPolicies {
		m.securityPolicies.Set(key, snap.SecurityPolicies[key])
	}

	for key := range snap.AccessPolicies {
		m.accessPolicies.Set(key, snap.AccessPolicies[key])
	}

	return nil
}
