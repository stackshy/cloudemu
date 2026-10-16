package networkfirewall

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	nfdriver "github.com/stackshy/cloudemu/v2/services/networkfirewall/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// networkfirewallSnapshot is the full serialized state of the AWS Network
// Firewall mock. Every memstore store holds a fully-exported *nfdriver pointer
// type, so each round-trips through the generic memstore helper under its exact
// key: firewalls/policies/TLS configs by name, rule groups by the "TYPE/name"
// composite key (so DescribeRuleGroup's type-qualified lookup keeps working
// after a restore), VPC endpoint associations by ARN, flow operations and
// analysis reports by id. The mu-guarded logging and resource-policy maps are
// captured beside the stores. The wired opts is intentionally not serialized.
// Fields added after the first release are additive, so older snapshots load.
type networkfirewallSnapshot struct {
	Firewalls       json.RawMessage `json:"firewalls,omitempty"`
	Policies        json.RawMessage `json:"policies,omitempty"`
	RuleGroups      json.RawMessage `json:"ruleGroups,omitempty"`
	TLSConfigs      json.RawMessage `json:"tlsConfigs,omitempty"`
	Associations    json.RawMessage `json:"associations,omitempty"`
	FlowOperations  json.RawMessage `json:"flowOperations,omitempty"`
	AnalysisReports json.RawMessage `json:"analysisReports,omitempty"`
	// Logging maps a firewall name to its logging configuration. Snapshots
	// written before the monitoring-dashboard switch stored a bare destination
	// array per firewall; Restore accepts both shapes.
	Logging          map[string]json.RawMessage `json:"logging,omitempty"`
	ResourcePolicies map[string]string          `json:"resourcePolicies,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused. Network Firewall is
// control-plane only and holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var snap networkfirewallSnapshot
	if err := m.snapshotStores(&snap); err != nil {
		return nil, err
	}

	if len(m.logging) > 0 {
		snap.Logging = make(map[string]json.RawMessage, len(m.logging))

		for name, cfg := range m.logging {
			b, err := json.Marshal(cfg)
			if err != nil {
				return nil, fmt.Errorf("networkfirewall: snapshot logging: %w", err)
			}

			snap.Logging[name] = b
		}
	}

	if len(m.resourcePolicies) > 0 {
		snap.ResourcePolicies = make(map[string]string, len(m.resourcePolicies))
		for arn, p := range m.resourcePolicies {
			snap.ResourcePolicies[arn] = p
		}
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
		{&snap.TLSConfigs, m.tlsConfigs.Snapshot},
		{&snap.Associations, m.associations.Snapshot},
		{&snap.FlowOperations, m.flowOperations.Snapshot},
		{&snap.AnalysisReports, m.analysisReports.Snapshot},
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
// resource name and ARN (and the ARN cross-references records hold) is
// preserved, so a restore is transparent to clients. State written before update
// tokens existed gets a freshly minted token per resource.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap networkfirewallSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("networkfirewall: parse snapshot: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.restoreStores(&snap); err != nil {
		return err
	}

	logging, err := decodeLogging(snap.Logging)
	if err != nil {
		return err
	}

	if logging != nil {
		m.logging = logging
	}

	if snap.ResourcePolicies != nil {
		m.resourcePolicies = snap.ResourcePolicies
	}

	m.backfillRestoredState()

	return nil
}

// decodeLogging parses the snapshot's logging map, accepting the current object
// shape and the older bare destination array.
func decodeLogging(raw map[string]json.RawMessage) (map[string]*nfdriver.LoggingConfiguration, error) {
	if raw == nil {
		return nil, nil //nolint:nilnil // nil means "no logging in the snapshot"
	}

	out := make(map[string]*nfdriver.LoggingConfiguration, len(raw))

	for name, b := range raw {
		var cfg nfdriver.LoggingConfiguration

		if len(b) > 0 && b[0] == '[' {
			if err := json.Unmarshal(b, &cfg.Destinations); err != nil {
				return nil, fmt.Errorf("networkfirewall: restore logging: %w", err)
			}
		} else if err := json.Unmarshal(b, &cfg); err != nil {
			return nil, fmt.Errorf("networkfirewall: restore logging: %w", err)
		}

		out[name] = &cfg
	}

	return out, nil
}

func (m *Mock) restoreStores(snap *networkfirewallSnapshot) error {
	loads := []struct {
		src json.RawMessage
		fn  func([]byte) error
	}{
		{snap.Firewalls, m.firewalls.LoadSnapshot},
		{snap.Policies, m.policies.LoadSnapshot},
		{snap.RuleGroups, m.ruleGroups.LoadSnapshot},
		{snap.TLSConfigs, m.tlsConfigs.LoadSnapshot},
		{snap.Associations, m.associations.LoadSnapshot},
		{snap.FlowOperations, m.flowOperations.LoadSnapshot},
		{snap.AnalysisReports, m.analysisReports.LoadSnapshot},
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

// backfillRestoredState gives state saved before this release what it lacks: an
// update token and an ACTIVE status on every resource that has them.
func (m *Mock) backfillRestoredState() {
	for _, f := range m.firewalls.SortedValues() {
		ensureToken(&f.UpdateToken)
	}

	for _, p := range m.policies.SortedValues() {
		ensureToken(&p.UpdateToken)
		ensureActive(&p.Status)
	}

	for _, rg := range m.ruleGroups.SortedValues() {
		ensureToken(&rg.UpdateToken)
		ensureActive(&rg.Status)
	}

	for _, t := range m.tlsConfigs.SortedValues() {
		ensureToken(&t.UpdateToken)
		ensureActive(&t.Status)
	}
}

func ensureToken(token *string) {
	if *token == "" {
		*token = newToken()
	}
}

func ensureActive(status *string) {
	if *status == "" {
		*status = statusActive
	}
}
