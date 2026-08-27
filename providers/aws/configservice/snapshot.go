package configservice

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/configservice/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// configSnapshot is the full serialized state of the AWS Config mock. The
// recorders/channels/rules/packs/aggregators/connectors stores hold unexported
// *xData whose payload lives in unexported fields, so all six are promoted to
// exported forms keyed by their store key. The remaining stores hold
// fully-exported *driver types and round-trip through the generic memstore
// helper. The authMu-guarded aggregation authorizations and remediation
// exceptions, plus the token registry, are captured beside them. The per-record
// mutexes and the wired opts are not serialized.
type configSnapshot struct {
	Recorders   map[string]*recorderSnapshot              `json:"recorders,omitempty"`
	Channels    map[string]*channelSnapshot               `json:"channels,omitempty"`
	Rules       map[string]*ruleSnapshot                  `json:"rules,omitempty"`
	Packs       map[string]driver.ConformancePack         `json:"packs,omitempty"`
	Aggregators map[string]driver.ConfigurationAggregator `json:"aggregators,omitempty"`
	Connectors  map[string]*connectorSnapshot             `json:"connectors,omitempty"`

	OrgRules    json.RawMessage `json:"orgRules,omitempty"`
	OrgPacks    json.RawMessage `json:"orgPacks,omitempty"`
	Remediation json.RawMessage `json:"remediation,omitempty"`
	StoredQuery json.RawMessage `json:"storedQuery,omitempty"`
	Retention   json.RawMessage `json:"retention,omitempty"`
	Resources   json.RawMessage `json:"resources,omitempty"`

	Authorizations []driver.AggregationAuthorization        `json:"authorizations,omitempty"`
	RemExceptions  map[string][]driver.RemediationException `json:"remExceptions,omitempty"`
	EvalTokens     map[string]string                        `json:"evalTokens,omitempty"`
}

// recorderSnapshot mirrors recorderData.
type recorderSnapshot struct {
	Rec driver.ConfigurationRecorder `json:"rec"`
}

// channelSnapshot mirrors channelData.
type channelSnapshot struct {
	Ch driver.DeliveryChannel `json:"ch"`
}

// ruleSnapshot mirrors ruleData, including the synthesized evaluations and the
// opaque result token so PutEvaluations still validates after a restore.
type ruleSnapshot struct {
	Rule        driver.ConfigRule   `json:"rule"`
	Evals       []driver.Evaluation `json:"evals,omitempty"`
	ResultToken string              `json:"resultToken,omitempty"`
}

// connectorSnapshot mirrors connectorData (all fields unexported).
type connectorSnapshot struct {
	Name              string `json:"name"`
	ARN               string `json:"arn,omitempty"`
	ConnectorAgentArn string `json:"connectorAgentArn,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// Config holds resource metadata, not bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := configSnapshot{}

	m.snapshotPromoted(&snap)

	if err := m.snapshotGeneric(&snap); err != nil {
		return nil, err
	}

	m.snapshotScalarState(&snap)

	return json.Marshal(snap)
}

func (m *Mock) snapshotPromoted(snap *configSnapshot) {
	m.snapshotPromotedA(snap)
	m.snapshotPromotedB(snap)
}

func (m *Mock) snapshotPromotedA(snap *configSnapshot) {
	if m.recorders.Len() > 0 {
		snap.Recorders = make(map[string]*recorderSnapshot, m.recorders.Len())

		for k, rd := range m.recorders.All() {
			rd.mu.RLock()
			snap.Recorders[k] = &recorderSnapshot{Rec: rd.rec}
			rd.mu.RUnlock()
		}
	}

	if m.channels.Len() > 0 {
		snap.Channels = make(map[string]*channelSnapshot, m.channels.Len())

		for k, cd := range m.channels.All() {
			cd.mu.RLock()
			snap.Channels[k] = &channelSnapshot{Ch: cd.ch}
			cd.mu.RUnlock()
		}
	}

	if m.rules.Len() > 0 {
		snap.Rules = make(map[string]*ruleSnapshot, m.rules.Len())

		for k, rd := range m.rules.All() {
			rd.mu.RLock()
			snap.Rules[k] = &ruleSnapshot{Rule: rd.rule, Evals: rd.evals, ResultToken: rd.resultToken}
			rd.mu.RUnlock()
		}
	}
}

func (m *Mock) snapshotPromotedB(snap *configSnapshot) {
	if m.packs.Len() > 0 {
		snap.Packs = make(map[string]driver.ConformancePack, m.packs.Len())

		for k, pd := range m.packs.All() {
			pd.mu.RLock()
			snap.Packs[k] = pd.pack
			pd.mu.RUnlock()
		}
	}

	if m.aggregators.Len() > 0 {
		snap.Aggregators = make(map[string]driver.ConfigurationAggregator, m.aggregators.Len())

		for k, ad := range m.aggregators.All() {
			ad.mu.RLock()
			snap.Aggregators[k] = ad.agg
			ad.mu.RUnlock()
		}
	}

	if m.connectors.Len() > 0 {
		snap.Connectors = make(map[string]*connectorSnapshot, m.connectors.Len())

		for k, cd := range m.connectors.All() {
			snap.Connectors[k] = &connectorSnapshot{Name: cd.name, ARN: cd.arn, ConnectorAgentArn: cd.connectorAgentArn}
		}
	}
}

func (m *Mock) snapshotGeneric(snap *configSnapshot) error {
	dumps := []struct {
		dst *json.RawMessage
		fn  func() ([]byte, error)
	}{
		{&snap.OrgRules, m.orgRules.Snapshot},
		{&snap.OrgPacks, m.orgPacks.Snapshot},
		{&snap.Remediation, m.remediation.Snapshot},
		{&snap.StoredQuery, m.storedQuery.Snapshot},
		{&snap.Retention, m.retention.Snapshot},
		{&snap.Resources, m.resources.Snapshot},
	}

	for _, d := range dumps {
		b, err := d.fn()
		if err != nil {
			return fmt.Errorf("configservice: snapshot store: %w", err)
		}

		*d.dst = b
	}

	return nil
}

func (m *Mock) snapshotScalarState(snap *configSnapshot) {
	m.authMu.RLock()
	if len(m.authorizations) > 0 {
		snap.Authorizations = append([]driver.AggregationAuthorization(nil), m.authorizations...)
	}

	if len(m.remExceptions) > 0 {
		snap.RemExceptions = m.remExceptions
	}
	m.authMu.RUnlock()

	m.tokenMu.RLock()
	if len(m.evalTokens) > 0 {
		snap.EvalTokens = m.evalTokens
	}
	m.tokenMu.RUnlock()
}

// Restore rebuilds the mock's state under the original identities: every
// recorder/channel/rule/pack/aggregator/connector key and every generic-store
// resource id is preserved so a client's identifiers still resolve.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap configSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("configservice: parse snapshot: %w", err)
	}

	m.restorePromoted(&snap)

	if err := m.restoreGeneric(&snap); err != nil {
		return err
	}

	m.restoreScalarState(&snap)

	return nil
}

func (m *Mock) restorePromoted(snap *configSnapshot) {
	for k, rs := range snap.Recorders {
		m.recorders.Set(k, &recorderData{rec: rs.Rec})
	}

	for k, cs := range snap.Channels {
		m.channels.Set(k, &channelData{ch: cs.Ch})
	}

	for k, rs := range snap.Rules {
		m.rules.Set(k, &ruleData{rule: rs.Rule, evals: rs.Evals, resultToken: rs.ResultToken})
	}

	for k := range snap.Packs {
		m.packs.Set(k, &packData{pack: snap.Packs[k]})
	}

	for k := range snap.Aggregators {
		m.aggregators.Set(k, &aggData{agg: snap.Aggregators[k]})
	}

	for k, cs := range snap.Connectors {
		m.connectors.Set(k, &connectorData{name: cs.Name, arn: cs.ARN, connectorAgentArn: cs.ConnectorAgentArn})
	}
}

func (m *Mock) restoreGeneric(snap *configSnapshot) error {
	loads := []struct {
		src json.RawMessage
		fn  func([]byte) error
	}{
		{snap.OrgRules, m.orgRules.LoadSnapshot},
		{snap.OrgPacks, m.orgPacks.LoadSnapshot},
		{snap.Remediation, m.remediation.LoadSnapshot},
		{snap.StoredQuery, m.storedQuery.LoadSnapshot},
		{snap.Retention, m.retention.LoadSnapshot},
		{snap.Resources, m.resources.LoadSnapshot},
	}

	for _, l := range loads {
		if len(l.src) == 0 {
			continue
		}

		if err := l.fn(l.src); err != nil {
			return fmt.Errorf("configservice: restore store: %w", err)
		}
	}

	return nil
}

func (m *Mock) restoreScalarState(snap *configSnapshot) {
	m.authMu.Lock()
	if len(snap.Authorizations) > 0 {
		m.authorizations = snap.Authorizations
	}

	if snap.RemExceptions != nil {
		m.remExceptions = snap.RemExceptions
	}
	m.authMu.Unlock()

	if snap.EvalTokens != nil {
		m.tokenMu.Lock()
		m.evalTokens = snap.EvalTokens
		m.tokenMu.Unlock()
	}
}
