package wafv2

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/wafv2/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// wafv2Snapshot is the full serialized state of the AWS WAFv2 mock. Every
// memstore store holds an unexported *xxxData whose only stateful field is a
// driver value (webACLData/ipSetData/ruleGroupData/regexSetData) or a
// scope+raw-JSON pair (loggingConfigData), all behind a per-record mutex and
// invisible to json.Marshal, so each store is promoted to an exported snapshot
// form keyed by its composite "scope/id" (or, for logging, the ResourceArn) — the
// keys that keep the REGIONAL and CLOUDFRONT namespaces from colliding after a
// restore. The three mutex-guarded side maps — assoc (protected-resource ARN ->
// web-ACL ARN), policies (rule-group ARN -> permission policy), and apiKeys
// (composite key -> summary) — are captured beside the stores. The per-record and
// create mutexes and the wired opts are intentionally not serialized.
type wafv2Snapshot struct {
	WebACLs  map[string]*webACLSnapshot    `json:"webACLs,omitempty"`
	IPSets   map[string]*ipSetSnapshot     `json:"ipSets,omitempty"`
	RuleGrps map[string]*ruleGroupSnapshot `json:"ruleGrps,omitempty"`
	Regexes  map[string]*regexSetSnapshot  `json:"regexes,omitempty"`
	LogCfgs  map[string]*loggingSnapshot   `json:"logCfgs,omitempty"`

	Assoc    map[string]string               `json:"assoc,omitempty"`
	Policies map[string]string               `json:"policies,omitempty"`
	APIKeys  map[string]driver.APIKeySummary `json:"apiKeys,omitempty"`
}

// webACLSnapshot mirrors webACLData (its driver.WebACL is behind an unexported
// field). The other three resource snapshots follow the same one-field shape.
type webACLSnapshot struct {
	ACL driver.WebACL `json:"acl"`
}

type ipSetSnapshot struct {
	Set driver.IPSet `json:"set"`
}

type ruleGroupSnapshot struct {
	Grp driver.RuleGroup `json:"grp"`
}

type regexSetSnapshot struct {
	Set driver.RegexPatternSet `json:"set"`
}

// loggingSnapshot mirrors loggingConfigData (its scope and raw config are both
// unexported).
type loggingSnapshot struct {
	Scope string          `json:"scope"`
	Cfg   json.RawMessage `json:"cfg,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// WAFv2 is control-plane only and holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := wafv2Snapshot{
		WebACLs:  m.snapshotWebACLs(),
		IPSets:   m.snapshotIPSets(),
		RuleGrps: m.snapshotRuleGroups(),
		Regexes:  m.snapshotRegexes(),
		LogCfgs:  m.snapshotLogCfgs(),
	}

	m.snapshotSideMaps(&snap)

	return json.Marshal(snap)
}

func (m *Mock) snapshotWebACLs() map[string]*webACLSnapshot {
	if m.webACLs.Len() == 0 {
		return nil
	}

	out := make(map[string]*webACLSnapshot, m.webACLs.Len())

	for k, d := range m.webACLs.All() {
		d.mu.RLock()
		out[k] = &webACLSnapshot{ACL: d.acl}
		d.mu.RUnlock()
	}

	return out
}

func (m *Mock) snapshotIPSets() map[string]*ipSetSnapshot {
	if m.ipSets.Len() == 0 {
		return nil
	}

	out := make(map[string]*ipSetSnapshot, m.ipSets.Len())

	for k, d := range m.ipSets.All() {
		d.mu.RLock()
		out[k] = &ipSetSnapshot{Set: d.set}
		d.mu.RUnlock()
	}

	return out
}

func (m *Mock) snapshotRuleGroups() map[string]*ruleGroupSnapshot {
	if m.ruleGrps.Len() == 0 {
		return nil
	}

	out := make(map[string]*ruleGroupSnapshot, m.ruleGrps.Len())

	for k, d := range m.ruleGrps.All() {
		d.mu.RLock()
		out[k] = &ruleGroupSnapshot{Grp: d.grp}
		d.mu.RUnlock()
	}

	return out
}

func (m *Mock) snapshotRegexes() map[string]*regexSetSnapshot {
	if m.regexes.Len() == 0 {
		return nil
	}

	out := make(map[string]*regexSetSnapshot, m.regexes.Len())

	for k, d := range m.regexes.All() {
		d.mu.RLock()
		out[k] = &regexSetSnapshot{Set: d.set}
		d.mu.RUnlock()
	}

	return out
}

func (m *Mock) snapshotLogCfgs() map[string]*loggingSnapshot {
	if m.logCfgs.Len() == 0 {
		return nil
	}

	out := make(map[string]*loggingSnapshot, m.logCfgs.Len())

	for k, d := range m.logCfgs.All() {
		out[k] = &loggingSnapshot{Scope: d.scope, Cfg: d.cfg}
	}

	return out
}

func (m *Mock) snapshotSideMaps(snap *wafv2Snapshot) {
	m.assocMu.RLock()
	snap.Assoc = m.assoc
	m.assocMu.RUnlock()

	m.policyMu.RLock()
	snap.Policies = m.policies
	m.policyMu.RUnlock()

	m.apiKeyMu.RLock()
	snap.APIKeys = m.apiKeys
	m.apiKeyMu.RUnlock()
}

// Restore rebuilds the mock's state under the original identities: every resource
// keeps its (scope,id) composite key, ARN, and lock token, so a restore is
// transparent to clients.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap wafv2Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("wafv2: parse snapshot: %w", err)
	}

	m.restoreStores(&snap)
	m.restoreSideMaps(&snap)

	return nil
}

func (m *Mock) restoreStores(snap *wafv2Snapshot) {
	for k, s := range snap.WebACLs {
		m.webACLs.Set(k, &webACLData{acl: s.ACL})
	}

	for k, s := range snap.IPSets {
		m.ipSets.Set(k, &ipSetData{set: s.Set})
	}

	for k, s := range snap.RuleGrps {
		m.ruleGrps.Set(k, &ruleGroupData{grp: s.Grp})
	}

	for k, s := range snap.Regexes {
		m.regexes.Set(k, &regexSetData{set: s.Set})
	}

	for k, s := range snap.LogCfgs {
		m.logCfgs.Set(k, &loggingConfigData{scope: s.Scope, cfg: s.Cfg})
	}
}

func (m *Mock) restoreSideMaps(snap *wafv2Snapshot) {
	m.assocMu.Lock()
	if snap.Assoc != nil {
		m.assoc = snap.Assoc
	}
	m.assocMu.Unlock()

	m.policyMu.Lock()
	if snap.Policies != nil {
		m.policies = snap.Policies
	}
	m.policyMu.Unlock()

	m.apiKeyMu.Lock()
	if snap.APIKeys != nil {
		m.apiKeys = snap.APIKeys
	}
	m.apiKeyMu.Unlock()
}
