package networkfirewall

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// networkfirewallSnapshot is the full serialized state of the AWS Network
// Firewall mock. Every memstore store holds a fully-exported *nfdriver pointer
// type (Firewall, FirewallPolicy, RuleGroup), so each round-trips through the
// generic memstore helper under its exact key — firewalls/policies by name, rule
// groups by the "TYPE/name" composite key, so DescribeRuleGroup's type-qualified
// lookup keeps working after a restore. The mu-guarded logging map (firewall
// name -> log types) is captured beside the stores. The wired opts is
// intentionally not serialized.
type networkfirewallSnapshot struct {
	Firewalls  json.RawMessage     `json:"firewalls,omitempty"`
	Policies   json.RawMessage     `json:"policies,omitempty"`
	RuleGroups json.RawMessage     `json:"ruleGroups,omitempty"`
	Logging    map[string][]string `json:"logging,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// Network Firewall is control-plane only and holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	var snap networkfirewallSnapshot
	if err := m.snapshotStores(&snap); err != nil {
		return nil, err
	}

	m.mu.RLock()
	logging := make(map[string][]string, len(m.logging))

	for name, types := range m.logging {
		logging[name] = append([]string(nil), types...)
	}

	m.mu.RUnlock()

	if len(logging) > 0 {
		snap.Logging = logging
	}

	return json.Marshal(snap)
}

func (m *Mock) snapshotStores(snap *networkfirewallSnapshot) error {
	dumps := []struct {
		dst *json.RawMessage
		fn  func() ([]byte, error)
	}{
		{&snap.Firewalls, m.firewalls.Snapshot},
		{&snap.Policies, m.policies.Snapshot},
		{&snap.RuleGroups, m.ruleGroups.Snapshot},
	}

	for _, d := range dumps {
		b, err := d.fn()
		if err != nil {
			return fmt.Errorf("networkfirewall: snapshot store: %w", err)
		}

		*d.dst = b
	}

	return nil
}

// Restore rebuilds the mock's state under the original identities: every
// firewall/policy/rule-group name and ARN (and the ARN cross-references records
// hold) is preserved, so a restore is transparent to clients.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap networkfirewallSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("networkfirewall: parse snapshot: %w", err)
	}

	if err := m.restoreStores(&snap); err != nil {
		return err
	}

	m.mu.Lock()
	if snap.Logging != nil {
		m.logging = snap.Logging
	}
	m.mu.Unlock()

	return nil
}

func (m *Mock) restoreStores(snap *networkfirewallSnapshot) error {
	loads := []struct {
		src json.RawMessage
		fn  func([]byte) error
	}{
		{snap.Firewalls, m.firewalls.LoadSnapshot},
		{snap.Policies, m.policies.LoadSnapshot},
		{snap.RuleGroups, m.ruleGroups.LoadSnapshot},
	}

	for _, l := range loads {
		if len(l.src) == 0 {
			continue
		}

		if err := l.fn(l.src); err != nil {
			return fmt.Errorf("networkfirewall: restore store: %w", err)
		}
	}

	return nil
}
