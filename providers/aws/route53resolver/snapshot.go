package route53resolver

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// route53resolverSnapshot is the full serialized state of the AWS Route 53
// Resolver mock. Every memstore store holds a fully-exported value type — a
// *driver pointer type for each resource, or a plain string / []string /
// []driver.Tag for the policy, firewall-domain, tag, and idempotency stores — so
// each round-trips through the generic memstore helper under its exact key. The
// composite keys matter and are preserved: fwRules is keyed by
// "group|domainList|qtype", tags by resource ARN, and idem by
// "kind|creatorRequestID", so a replayed idempotent create still resolves after a
// restore. The wired opts and the serializing mutex are intentionally not
// serialized.
type route53resolverSnapshot struct {
	Endpoints    json.RawMessage `json:"endpoints,omitempty"`
	Rules        json.RawMessage `json:"rules,omitempty"`
	RuleAssocs   json.RawMessage `json:"ruleAssocs,omitempty"`
	RulePolicies json.RawMessage `json:"rulePolicies,omitempty"`
	QLCs         json.RawMessage `json:"qlcs,omitempty"`
	QLCAssocs    json.RawMessage `json:"qlcAssocs,omitempty"`
	QLCPolicies  json.RawMessage `json:"qlcPolicies,omitempty"`
	RslvrConfigs json.RawMessage `json:"rslvrConfigs,omitempty"`
	DnssecCfgs   json.RawMessage `json:"dnssecCfgs,omitempty"`
	FwDomLists   json.RawMessage `json:"fwDomLists,omitempty"`
	FwDomains    json.RawMessage `json:"fwDomains,omitempty"`
	FwRuleGroups json.RawMessage `json:"fwRuleGroups,omitempty"`
	FwRules      json.RawMessage `json:"fwRules,omitempty"`
	FwAssocs     json.RawMessage `json:"fwAssocs,omitempty"`
	FwConfigs    json.RawMessage `json:"fwConfigs,omitempty"`
	FwPolicies   json.RawMessage `json:"fwPolicies,omitempty"`
	Outposts     json.RawMessage `json:"outposts,omitempty"`
	Tags         json.RawMessage `json:"tags,omitempty"`
	Idem         json.RawMessage `json:"idem,omitempty"`
}

// storeRefs pairs each snapshot field with its store's Snapshot/LoadSnapshot
// entry points, so dump and load walk the identical, symmetric list.
func (m *Mock) storeRefs(snap *route53resolverSnapshot) []struct {
	dst  *json.RawMessage
	dump func() ([]byte, error)
	load func([]byte) error
} {
	return []struct {
		dst  *json.RawMessage
		dump func() ([]byte, error)
		load func([]byte) error
	}{
		{&snap.Endpoints, m.endpoints.Snapshot, m.endpoints.LoadSnapshot},
		{&snap.Rules, m.rules.Snapshot, m.rules.LoadSnapshot},
		{&snap.RuleAssocs, m.ruleAssocs.Snapshot, m.ruleAssocs.LoadSnapshot},
		{&snap.RulePolicies, m.rulePolicies.Snapshot, m.rulePolicies.LoadSnapshot},
		{&snap.QLCs, m.qlcs.Snapshot, m.qlcs.LoadSnapshot},
		{&snap.QLCAssocs, m.qlcAssocs.Snapshot, m.qlcAssocs.LoadSnapshot},
		{&snap.QLCPolicies, m.qlcPolicies.Snapshot, m.qlcPolicies.LoadSnapshot},
		{&snap.RslvrConfigs, m.rslvrConfigs.Snapshot, m.rslvrConfigs.LoadSnapshot},
		{&snap.DnssecCfgs, m.dnssecCfgs.Snapshot, m.dnssecCfgs.LoadSnapshot},
		{&snap.FwDomLists, m.fwDomLists.Snapshot, m.fwDomLists.LoadSnapshot},
		{&snap.FwDomains, m.fwDomains.Snapshot, m.fwDomains.LoadSnapshot},
		{&snap.FwRuleGroups, m.fwRuleGroups.Snapshot, m.fwRuleGroups.LoadSnapshot},
		{&snap.FwRules, m.fwRules.Snapshot, m.fwRules.LoadSnapshot},
		{&snap.FwAssocs, m.fwAssocs.Snapshot, m.fwAssocs.LoadSnapshot},
		{&snap.FwConfigs, m.fwConfigs.Snapshot, m.fwConfigs.LoadSnapshot},
		{&snap.FwPolicies, m.fwPolicies.Snapshot, m.fwPolicies.LoadSnapshot},
		{&snap.Outposts, m.outposts.Snapshot, m.outposts.LoadSnapshot},
		{&snap.Tags, m.tags.Snapshot, m.tags.LoadSnapshot},
		{&snap.Idem, m.idem.Snapshot, m.idem.LoadSnapshot},
	}
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// Route 53 Resolver is control-plane only and holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var snap route53resolverSnapshot

	for _, r := range m.storeRefs(&snap) {
		b, err := r.dump()
		if err != nil {
			return nil, fmt.Errorf("route53resolver: snapshot store: %w", err)
		}

		*r.dst = b
	}

	return json.Marshal(snap)
}

// Restore rebuilds the mock's state under the original identities: every
// endpoint/rule/config id, ARN, and composite key (and the id cross-references
// records hold) is preserved, so a restore is transparent to clients.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap route53resolverSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("route53resolver: parse snapshot: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, r := range m.storeRefs(&snap) {
		if len(*r.dst) == 0 {
			continue
		}

		if err := r.load(*r.dst); err != nil {
			return fmt.Errorf("route53resolver: restore store: %w", err)
		}
	}

	return nil
}
