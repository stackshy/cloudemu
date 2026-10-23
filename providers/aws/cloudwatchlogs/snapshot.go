package cloudwatchlogs

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"time"

	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/providers/aws/cloudwatchlogs/dataprotection"
	"github.com/stackshy/cloudemu/v2/services/logging/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// logsSnapshot is the full serialized state of the CloudWatch Logs mock. The
// groups store holds an unexported *logGroup whose nested stores (streams, and
// the metric/subscription filter stores) must be captured, so it is promoted to
// an exported snapshot form keyed by group name. The wired deps (opts,
// monitoring, lambda, exportStore) are not serialized.
type logsSnapshot struct {
	Groups            map[string]*logGroupSnapshot `json:"groups,omitempty"`
	QueryResultKmsKey string                       `json:"queryResultKmsKey,omitempty"`
	FilterPeriods     map[string]filterPeriod      `json:"filterPeriods,omitempty"`
	Queries           json.RawMessage              `json:"queries,omitempty"`
	ExportTasks       json.RawMessage              `json:"exportTasks,omitempty"`
	AccountPolicies   []driver.AccountPolicy       `json:"accountPolicies,omitempty"`
}

// logGroupSnapshot mirrors logGroup. Its metric- and subscription-filter stores
// hold fully-exported driver pointer types, so they round-trip through the
// generic memstore helper; streams carries an exported form because logStream
// has an unexported mutex.
type logGroupSnapshot struct {
	Info          driver.LogGroupInfo           `json:"info"`
	KmsKeyID      string                        `json:"kmsKeyId,omitempty"`
	LogGroupClass string                        `json:"logGroupClass,omitempty"`
	Streams       map[string]*logStreamSnapshot `json:"streams,omitempty"`
	MetricFilters json.RawMessage               `json:"metricFilters,omitempty"`
	SubFilters    json.RawMessage               `json:"subFilters,omitempty"`
	// DataProtection is the group's data protection policy; events keep
	// their original text in driver.LogEvent.Unmasked.
	DataProtection *dataProtectionSnapshot `json:"dataProtection,omitempty"`
}

type dataProtectionSnapshot struct {
	Document        string    `json:"document"`
	LastUpdatedTime time.Time `json:"lastUpdatedTime"`
}

// logStreamSnapshot mirrors logStream, promoting its fields past the unexported
// mutex.
type logStreamSnapshot struct {
	Info    driver.LogStreamInfo `json:"info"`
	Events  []driver.LogEvent    `json:"events,omitempty"`
	Uploads uint64               `json:"uploads,omitempty"`
}

// Snapshot captures every log group's state as JSON. includeAssets is unused —
// log events are the payload and are always captured.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	m.mu.RLock()
	snap := logsSnapshot{QueryResultKmsKey: m.queryResultKmsKey, FilterPeriods: maps.Clone(m.periods)}

	for _, ap := range m.accountPolicies {
		snap.AccountPolicies = append(snap.AccountPolicies, ap.info)
	}
	m.mu.RUnlock()

	queries, err := m.queries.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("cloudwatchlogs: snapshot queries: %w", err)
	}

	snap.Queries = queries

	exports, err := m.exports.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("cloudwatchlogs: snapshot export tasks: %w", err)
	}

	snap.ExportTasks = exports

	if m.groups.Len() == 0 {
		return json.Marshal(snap)
	}

	snap.Groups = make(map[string]*logGroupSnapshot, m.groups.Len())

	for name, g := range m.groups.All() {
		gs, err := snapshotGroup(g)
		if err != nil {
			return nil, err
		}

		snap.Groups[name] = gs
	}

	return json.Marshal(snap)
}

func snapshotGroup(g *logGroup) (*logGroupSnapshot, error) {
	gs := &logGroupSnapshot{Info: g.info, KmsKeyID: g.kmsKeyID, LogGroupClass: g.class}

	if gp := g.dataProtection.Load(); gp != nil {
		gs.DataProtection = &dataProtectionSnapshot{Document: gp.document, LastUpdatedTime: gp.updated}
	}

	mf, err := g.metricFilters.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("cloudwatchlogs: snapshot metric filters: %w", err)
	}

	sf, err := g.subFilters.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("cloudwatchlogs: snapshot subscription filters: %w", err)
	}

	gs.MetricFilters = mf
	gs.SubFilters = sf

	if g.streams.Len() > 0 {
		gs.Streams = make(map[string]*logStreamSnapshot, g.streams.Len())

		for sName, s := range g.streams.All() {
			s.mu.RLock()
			gs.Streams[sName] = &logStreamSnapshot{
				Info: s.info, Events: append([]driver.LogEvent(nil), s.events...), Uploads: s.uploads,
			}
			s.mu.RUnlock()
		}
	}

	return gs, nil
}

// Restore rebuilds every log group under its original name with its streams,
// events, and metric/subscription filters intact.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap logsSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("cloudwatchlogs: parse snapshot: %w", err)
	}

	m.mu.Lock()
	m.queryResultKmsKey = snap.QueryResultKmsKey

	m.periods = map[string]filterPeriod{}
	maps.Copy(m.periods, snap.FilterPeriods)
	m.mu.Unlock()

	if err := m.restoreAccountPolicies(snap.AccountPolicies); err != nil {
		return err
	}

	if len(snap.Queries) > 0 {
		if err := m.queries.LoadSnapshot(snap.Queries); err != nil {
			return fmt.Errorf("cloudwatchlogs: restore queries: %w", err)
		}
	}

	if len(snap.ExportTasks) > 0 {
		if err := m.exports.LoadSnapshot(snap.ExportTasks); err != nil {
			return fmt.Errorf("cloudwatchlogs: restore export tasks: %w", err)
		}
	}

	for name, gs := range snap.Groups {
		g, err := restoreGroup(gs)
		if err != nil {
			return err
		}

		m.groups.Set(name, g)
	}

	return nil
}

func restoreGroup(gs *logGroupSnapshot) (*logGroup, error) {
	g := &logGroup{
		info:          gs.Info,
		kmsKeyID:      gs.KmsKeyID,
		class:         gs.LogGroupClass,
		streams:       memstore.New[*logStream](),
		metricFilters: memstore.New[*driver.MetricFilterInfo](),
		subFilters:    memstore.New[*driver.SubscriptionFilterInfo](),
	}

	if len(gs.MetricFilters) > 0 {
		if err := g.metricFilters.LoadSnapshot(gs.MetricFilters); err != nil {
			return nil, fmt.Errorf("cloudwatchlogs: restore metric filters: %w", err)
		}
	}

	if len(gs.SubFilters) > 0 {
		if err := g.subFilters.LoadSnapshot(gs.SubFilters); err != nil {
			return nil, fmt.Errorf("cloudwatchlogs: restore subscription filters: %w", err)
		}
	}

	for sName, ss := range gs.Streams {
		g.streams.Set(sName, &logStream{info: ss.Info, events: ss.Events, uploads: ss.Uploads})
	}

	if dp := gs.DataProtection; dp != nil {
		p, err := dataprotection.Parse(dp.Document)
		if err != nil {
			return nil, fmt.Errorf("cloudwatchlogs: restore data protection policy: %w", err)
		}

		g.dataProtection.Store(&groupPolicy{document: dp.Document, updated: dp.LastUpdatedTime, policy: p})
	}

	return g, nil
}

// restoreAccountPolicies rebuilds the account policies, re-parsing each
// document into the form ingest uses.
func (m *Mock) restoreAccountPolicies(infos []driver.AccountPolicy) error {
	policies := make(map[string]*accountPolicy, len(infos))

	for i := range infos {
		in := &driver.PutAccountPolicyInput{
			PolicyName: infos[i].PolicyName, PolicyType: infos[i].PolicyType, PolicyDocument: infos[i].PolicyDocument,
			Scope: infos[i].Scope, SelectionCriteria: infos[i].SelectionCriteria,
		}

		ap, err := m.buildAccountPolicy(in, false)
		if err != nil {
			return fmt.Errorf("cloudwatchlogs: restore account policy %s: %w", infos[i].PolicyName, err)
		}

		ap.info = infos[i]
		policies[accountPolicyKey(ap.info.PolicyType, ap.info.PolicyName)] = ap
	}

	m.mu.Lock()
	m.accountPolicies = policies
	m.mu.Unlock()

	return nil
}
