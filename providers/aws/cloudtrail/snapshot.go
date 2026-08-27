package cloudtrail

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/cloudtrail/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// cloudtrailSnapshot is the full serialized state of the AWS CloudTrail mock.
// The trails/eds/channels/dashboards/imports/queries stores each hold an
// unexported *xData whose payload lives in unexported fields, so all six are
// promoted to exported forms keyed by their store key (trail name, EDS/channel
// ARN, dashboard name, import/query id). The name/ARN index stores hold plain
// strings and round-trip through the generic memstore helper. The mu-guarded
// maps (policies, tags, delegated) and the recorded management-event log are
// captured beside them. The per-record mutexes and the wired opts are not
// serialized.
type cloudtrailSnapshot struct {
	Trails      map[string]*trailSnapshot     `json:"trails,omitempty"`
	TrailARNIdx json.RawMessage               `json:"trailArnIdx,omitempty"`
	EDS         map[string]*edsSnapshot       `json:"eds,omitempty"`
	EDSNameIdx  json.RawMessage               `json:"edsNameIdx,omitempty"`
	ChanNameIdx json.RawMessage               `json:"chanNameIdx,omitempty"`
	Channels    map[string]*channelSnapshot   `json:"channels,omitempty"`
	Dashboards  map[string]*dashboardSnapshot `json:"dashboards,omitempty"`
	Imports     map[string]driver.Import      `json:"imports,omitempty"`
	Queries     map[string]driver.Query       `json:"queries,omitempty"`

	Policies  map[string]string            `json:"policies,omitempty"`
	Tags      map[string]map[string]string `json:"tags,omitempty"`
	Delegated []string                     `json:"delegated,omitempty"`
	Events    []driver.Event               `json:"events,omitempty"`
}

// trailSnapshot mirrors trailData (all fields unexported).
type trailSnapshot struct {
	Trail    driver.Trail                   `json:"trail"`
	Status   driver.TrailStatus             `json:"status"`
	Selors   []driver.EventSelector         `json:"selors,omitempty"`
	AdvSel   []driver.AdvancedEventSelector `json:"advSel,omitempty"`
	Insights []driver.InsightSelector       `json:"insights,omitempty"`
}

// edsSnapshot mirrors edsData (all fields unexported).
type edsSnapshot struct {
	EDS               driver.EventDataStore    `json:"eds"`
	Insights          []driver.InsightSelector `json:"insights,omitempty"`
	FederationRoleARN string                   `json:"federationRoleArn,omitempty"`
	FederationStatus  string                   `json:"federationStatus,omitempty"`
	MaxEventSize      string                   `json:"maxEventSize,omitempty"`
}

// channelSnapshot mirrors channelData.
type channelSnapshot struct {
	Channel      driver.Channel `json:"channel"`
	MaxEventSize string         `json:"maxEventSize,omitempty"`
}

// dashboardSnapshot mirrors dashboardData.
type dashboardSnapshot struct {
	Dashboard driver.Dashboard `json:"dashboard"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// CloudTrail holds resource metadata and a bounded event log, not object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := cloudtrailSnapshot{}

	m.snapshotStores(&snap)

	if err := m.snapshotIndexes(&snap); err != nil {
		return nil, err
	}

	m.snapshotScalarState(&snap)

	return json.Marshal(snap)
}

func (m *Mock) snapshotStores(snap *cloudtrailSnapshot) {
	m.snapshotStoresA(snap)
	m.snapshotStoresB(snap)
}

func (m *Mock) snapshotStoresA(snap *cloudtrailSnapshot) {
	if m.trails.Len() > 0 {
		snap.Trails = make(map[string]*trailSnapshot, m.trails.Len())

		for name, td := range m.trails.All() {
			td.mu.RLock()
			snap.Trails[name] = &trailSnapshot{
				Trail: td.trail, Status: td.status, Selors: td.selors,
				AdvSel: td.advSel, Insights: td.insights,
			}
			td.mu.RUnlock()
		}
	}

	if m.eds.Len() > 0 {
		snap.EDS = make(map[string]*edsSnapshot, m.eds.Len())

		for arn, ed := range m.eds.All() {
			ed.mu.RLock()
			snap.EDS[arn] = &edsSnapshot{
				EDS: ed.eds, Insights: ed.insights, FederationRoleARN: ed.federationRoleARN,
				FederationStatus: ed.federationStatus, MaxEventSize: ed.maxEventSize,
			}
			ed.mu.RUnlock()
		}
	}

	if m.channels.Len() > 0 {
		snap.Channels = make(map[string]*channelSnapshot, m.channels.Len())

		for arn, cd := range m.channels.All() {
			cd.mu.RLock()
			snap.Channels[arn] = &channelSnapshot{Channel: cd.channel, MaxEventSize: cd.maxEventSize}
			cd.mu.RUnlock()
		}
	}
}

func (m *Mock) snapshotStoresB(snap *cloudtrailSnapshot) {
	if m.dashboards.Len() > 0 {
		snap.Dashboards = make(map[string]*dashboardSnapshot, m.dashboards.Len())

		for name, dd := range m.dashboards.All() {
			dd.mu.RLock()
			snap.Dashboards[name] = &dashboardSnapshot{Dashboard: dd.dashboard}
			dd.mu.RUnlock()
		}
	}

	if m.imports.Len() > 0 {
		snap.Imports = make(map[string]driver.Import, m.imports.Len())

		for id, imp := range m.imports.All() {
			imp.mu.RLock()
			snap.Imports[id] = imp.imp
			imp.mu.RUnlock()
		}
	}

	if m.queries.Len() > 0 {
		snap.Queries = make(map[string]driver.Query, m.queries.Len())

		for id, q := range m.queries.All() {
			q.mu.RLock()
			snap.Queries[id] = q.q
			q.mu.RUnlock()
		}
	}
}

func (m *Mock) snapshotIndexes(snap *cloudtrailSnapshot) error {
	dumps := []struct {
		dst *json.RawMessage
		fn  func() ([]byte, error)
	}{
		{&snap.TrailARNIdx, m.trailARNIdx.Snapshot},
		{&snap.EDSNameIdx, m.edsNameIdx.Snapshot},
		{&snap.ChanNameIdx, m.chanNameIdx.Snapshot},
	}

	for _, d := range dumps {
		b, err := d.fn()
		if err != nil {
			return fmt.Errorf("cloudtrail: snapshot index: %w", err)
		}

		*d.dst = b
	}

	return nil
}

func (m *Mock) snapshotScalarState(snap *cloudtrailSnapshot) {
	m.policyMu.RLock()
	if len(m.policies) > 0 {
		snap.Policies = m.policies
	}
	m.policyMu.RUnlock()

	m.tagsMu.RLock()
	if len(m.tags) > 0 {
		snap.Tags = m.tags
	}
	m.tagsMu.RUnlock()

	m.orgMu.Lock()
	if len(m.delegated) > 0 {
		snap.Delegated = make([]string, 0, len(m.delegated))
		for acct := range m.delegated {
			snap.Delegated = append(snap.Delegated, acct)
		}
	}
	m.orgMu.Unlock()

	m.eventsMu.RLock()
	if len(m.events) > 0 {
		snap.Events = append([]driver.Event(nil), m.events...)
	}
	m.eventsMu.RUnlock()
}

// Restore rebuilds the mock's state under the original identities: every trail
// name, EDS/channel ARN, dashboard name, import/query id, and the name/ARN
// indexes are preserved so a client's identifiers still resolve.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap cloudtrailSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("cloudtrail: parse snapshot: %w", err)
	}

	m.restoreStores(&snap)

	if err := m.restoreIndexes(&snap); err != nil {
		return err
	}

	m.restoreScalarState(&snap)

	return nil
}

func (m *Mock) restoreStores(snap *cloudtrailSnapshot) {
	for name, ts := range snap.Trails {
		m.trails.Set(name, &trailData{
			trail: ts.Trail, status: ts.Status, selors: ts.Selors,
			advSel: ts.AdvSel, insights: ts.Insights,
		})
	}

	for arn, es := range snap.EDS {
		m.eds.Set(arn, &edsData{
			eds: es.EDS, insights: es.Insights, federationRoleARN: es.FederationRoleARN,
			federationStatus: es.FederationStatus, maxEventSize: es.MaxEventSize,
		})
	}

	for arn, cs := range snap.Channels {
		m.channels.Set(arn, &channelData{channel: cs.Channel, maxEventSize: cs.MaxEventSize})
	}

	for name, ds := range snap.Dashboards {
		m.dashboards.Set(name, &dashboardData{dashboard: ds.Dashboard})
	}

	for id := range snap.Imports {
		m.imports.Set(id, &importData{imp: snap.Imports[id]})
	}

	for id, q := range snap.Queries {
		m.queries.Set(id, &queryData{q: q})
	}
}

func (m *Mock) restoreIndexes(snap *cloudtrailSnapshot) error {
	loads := []struct {
		src json.RawMessage
		fn  func([]byte) error
	}{
		{snap.TrailARNIdx, m.trailARNIdx.LoadSnapshot},
		{snap.EDSNameIdx, m.edsNameIdx.LoadSnapshot},
		{snap.ChanNameIdx, m.chanNameIdx.LoadSnapshot},
	}

	for _, l := range loads {
		if len(l.src) == 0 {
			continue
		}

		if err := l.fn(l.src); err != nil {
			return fmt.Errorf("cloudtrail: restore index: %w", err)
		}
	}

	return nil
}

func (m *Mock) restoreScalarState(snap *cloudtrailSnapshot) {
	if snap.Policies != nil {
		m.policyMu.Lock()
		m.policies = snap.Policies
		m.policyMu.Unlock()
	}

	if snap.Tags != nil {
		m.tagsMu.Lock()
		m.tags = snap.Tags
		m.tagsMu.Unlock()
	}

	if len(snap.Delegated) > 0 {
		m.orgMu.Lock()
		for _, acct := range snap.Delegated {
			m.delegated[acct] = struct{}{}
		}
		m.orgMu.Unlock()
	}

	if len(snap.Events) > 0 {
		m.eventsMu.Lock()
		m.events = snap.Events
		m.eventsMu.Unlock()
	}
}
