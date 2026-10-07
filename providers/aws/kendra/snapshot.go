package kendra

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/kendra/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// kendraSnapshot is the full serialized state of the Kendra mock. The stores
// hold exported driver types, so they serialize directly, keyed by index id and
// data source "indexId/dataSourceId". The wired opts are not serialized.
type kendraSnapshot struct {
	Indexes        map[string]driver.Index                      `json:"indexes,omitempty"`
	DataSources    map[string]driver.DataSource                 `json:"dataSources,omitempty"`
	Documents      map[string]storedDocument                    `json:"documents,omitempty"`
	SyncJobs       map[string]syncJobRecord                     `json:"syncJobs,omitempty"`
	Faqs           map[string]driver.Faq                        `json:"faqs,omitempty"`
	Thesauri       map[string]driver.Thesaurus                  `json:"thesauri,omitempty"`
	BlockLists     map[string]driver.BlockList                  `json:"blockLists,omitempty"`
	Experiences    map[string]driver.Experience                 `json:"experiences,omitempty"`
	AccessControls map[string]driver.AccessControlConfiguration `json:"accessControls,omitempty"`
	Featured       map[string]driver.FeaturedResultsSet         `json:"featured,omitempty"`
	Mappings       map[string]principalMapping                  `json:"mappings,omitempty"`
	Suggestions    map[string]driver.SuggestionsConfig          `json:"suggestions,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused. Kendra is
// control-plane only and holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := kendraSnapshot{}

	if m.indexes.Len() > 0 {
		snap.Indexes = m.indexes.All()
	}

	if m.dataSources.Len() > 0 {
		snap.DataSources = m.dataSources.All()
	}

	snap.Documents = allOrNil(m.documents)
	snap.SyncJobs = allOrNil(m.syncJobs)
	snap.Faqs = allOrNil(m.faqs)
	snap.Thesauri = allOrNil(m.thesauri)
	snap.BlockLists = allOrNil(m.blockLists)
	snap.Experiences = allOrNil(m.experiences)
	snap.AccessControls = allOrNil(m.accessControls)
	snap.Featured = allOrNil(m.featured)
	snap.Mappings = allOrNil(m.mappings)
	snap.Suggestions = allOrNil(m.suggestions)

	b, err := json.Marshal(snap)
	if err != nil {
		return nil, fmt.Errorf("kendra: snapshot: %w", err)
	}

	return b, nil
}

// Restore rebuilds the mock's state under the original identities: every index
// id (and the arn Terraform derives from it) and every data source id is
// preserved, so a restore is transparent to clients.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap kendraSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("kendra: parse snapshot: %w", err)
	}

	for id := range snap.Indexes {
		m.indexes.Set(id, snap.Indexes[id])
	}

	for key := range snap.DataSources {
		m.dataSources.Set(key, snap.DataSources[key])
	}

	restoreInto(m.documents, snap.Documents)
	restoreInto(m.syncJobs, snap.SyncJobs)
	restoreInto(m.faqs, snap.Faqs)
	restoreInto(m.thesauri, snap.Thesauri)
	restoreInto(m.blockLists, snap.BlockLists)
	restoreInto(m.experiences, snap.Experiences)
	restoreInto(m.accessControls, snap.AccessControls)
	restoreInto(m.featured, snap.Featured)
	restoreInto(m.mappings, snap.Mappings)
	restoreInto(m.suggestions, snap.Suggestions)

	return nil
}

// allOrNil returns a store's entries, or nil when it is empty so the snapshot
// omits it.
func allOrNil[V any](s *memstore.Store[V]) map[string]V {
	if s.Len() == 0 {
		return nil
	}

	return s.All()
}

// restoreInto loads snapshot entries into a store under their original keys.
func restoreInto[V any](s *memstore.Store[V], entries map[string]V) {
	for k, v := range entries {
		s.Set(k, v)
	}
}
