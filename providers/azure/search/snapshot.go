package search

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// searchSnapshot is the full serialized state of the Azure AI Search mock. Every
// store holds a fully-exported driver value type (or the documents store's
// map[string]any) keyed by service[/index]/name, so each round-trips through the
// generic memstore helper — no promotion is needed. Seq is the monotonic etag
// counter, captured so restored resources keep issuing fresh (non-colliding)
// etags. The wired deps (m.opts, m.monitoring) are intentionally not serialized.
type searchSnapshot struct {
	Services     json.RawMessage `json:"services,omitempty"`
	AdminKeys    json.RawMessage `json:"adminKeys,omitempty"`
	QueryKeys    json.RawMessage `json:"queryKeys,omitempty"`
	SharedLinks  json.RawMessage `json:"sharedLinks,omitempty"`
	PrivateConns json.RawMessage `json:"privateConns,omitempty"`
	Indexes      json.RawMessage `json:"indexes,omitempty"`
	Documents    json.RawMessage `json:"documents,omitempty"`
	Indexers     json.RawMessage `json:"indexers,omitempty"`
	IndexerRuns  json.RawMessage `json:"indexerRuns,omitempty"`
	DataSources  json.RawMessage `json:"dataSources,omitempty"`
	Skillsets    json.RawMessage `json:"skillsets,omitempty"`
	SynonymMaps  json.RawMessage `json:"synonymMaps,omitempty"`
	Aliases      json.RawMessage `json:"aliases,omitempty"`
	Seq          int64           `json:"seq,omitempty"`
}

// storeDumps pairs each snapshot field with its store's Snapshot/LoadSnapshot so
// the dump and restore loops stay symmetric over the same store set.
func (m *Mock) storeDumps(snap *searchSnapshot) []struct {
	raw  *json.RawMessage
	dump func() ([]byte, error)
	load func([]byte) error
} {
	return []struct {
		raw  *json.RawMessage
		dump func() ([]byte, error)
		load func([]byte) error
	}{
		{&snap.Services, m.services.Snapshot, m.services.LoadSnapshot},
		{&snap.AdminKeys, m.adminKeys.Snapshot, m.adminKeys.LoadSnapshot},
		{&snap.QueryKeys, m.queryKeys.Snapshot, m.queryKeys.LoadSnapshot},
		{&snap.SharedLinks, m.sharedLinks.Snapshot, m.sharedLinks.LoadSnapshot},
		{&snap.PrivateConns, m.privateConns.Snapshot, m.privateConns.LoadSnapshot},
		{&snap.Indexes, m.indexes.Snapshot, m.indexes.LoadSnapshot},
		{&snap.Documents, m.documents.Snapshot, m.documents.LoadSnapshot},
		{&snap.Indexers, m.indexers.Snapshot, m.indexers.LoadSnapshot},
		{&snap.IndexerRuns, m.indexerRuns.Snapshot, m.indexerRuns.LoadSnapshot},
		{&snap.DataSources, m.dataSources.Snapshot, m.dataSources.LoadSnapshot},
		{&snap.Skillsets, m.skillsets.Snapshot, m.skillsets.LoadSnapshot},
		{&snap.SynonymMaps, m.synonymMaps.Snapshot, m.synonymMaps.LoadSnapshot},
		{&snap.Aliases, m.aliases.Snapshot, m.aliases.LoadSnapshot},
	}
}

// Snapshot captures every service and its data-plane resources as JSON.
// includeAssets is unused — documents are always captured (they are the data
// plane's payload, not a separable bulk asset).
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	var snap searchSnapshot

	for _, d := range m.storeDumps(&snap) {
		b, err := d.dump()
		if err != nil {
			return nil, fmt.Errorf("search: snapshot store: %w", err)
		}

		*d.raw = b
	}

	snap.Seq = m.seq.Load()

	return json.Marshal(snap)
}

// Restore rebuilds every service and data-plane resource under its original
// service[/index]/name key, so cross-references (documents to their index,
// aliases to their index) survive unchanged.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap searchSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("search: parse snapshot: %w", err)
	}

	for _, d := range m.storeDumps(&snap) {
		if len(*d.raw) == 0 {
			continue
		}

		if err := d.load(*d.raw); err != nil {
			return fmt.Errorf("search: restore store: %w", err)
		}
	}

	m.seq.Store(snap.Seq)

	return nil
}
