package acr

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/containerregistry/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// acrSnapshot is the full serialized state of the ACR mock: the data-plane repo
// catalog (repos, keyed by name, each with its images/layers, scan results,
// lifecycle policy, and settings) and the ARM management-plane resources
// (registries keyed by "{rg}/{name}", webhooks, replications). repoData,
// registryData, and imageData are unexported, so they are promoted to exported
// forms; the scan/webhook/replication stores hold exported driver values and
// round-trip through the generic memstore helper. The mutex and the wired
// options/monitoring are intentionally not serialized.
type acrSnapshot struct {
	Repos        map[string]*repoDataSnapshot     `json:"repos,omitempty"`
	Registries   map[string]*registryDataSnapshot `json:"registries,omitempty"`
	Webhooks     json.RawMessage                  `json:"webhooks,omitempty"`
	Replications json.RawMessage                  `json:"replications,omitempty"`
}

// repoDataSnapshot is the exported form of repoData.
type repoDataSnapshot struct {
	Info          driver.Repository             `json:"info"`
	Images        map[string]*imageDataSnapshot `json:"images,omitempty"`
	Scans         json.RawMessage               `json:"scans,omitempty"`
	Policy        *driver.LifecyclePolicy       `json:"policy,omitempty"`
	ScanOnPush    bool                          `json:"scanOnPush,omitempty"`
	TagMutability string                        `json:"tagMutability,omitempty"`
	// Attrs is the repository's changeableAttributes. A nil pointer (a
	// snapshot taken before this field existed) restores to fully enabled.
	Attrs *changeableAttrsSnapshot `json:"attrs,omitempty"`
	// TagAttrs holds each PATCHed tag's changeableAttributes, keyed by tag
	// name. A tag absent here restores to fully enabled.
	TagAttrs map[string]changeableAttrsSnapshot `json:"tagAttrs,omitempty"`
}

// imageDataSnapshot is the exported form of imageData.
type imageDataSnapshot struct {
	Detail driver.ImageDetail `json:"detail"`
	Layers []driver.LayerInfo `json:"layers,omitempty"`
	// Attrs is the manifest's changeableAttributes. A nil pointer (a snapshot
	// taken before this field existed) restores to fully enabled.
	Attrs *changeableAttrsSnapshot `json:"attrs,omitempty"`
}

// changeableAttrsSnapshot is the exported, JSON-serializable form of
// changeableAttrs.
type changeableAttrsSnapshot struct {
	DeleteEnabled bool `json:"deleteEnabled"`
	WriteEnabled  bool `json:"writeEnabled"`
	ListEnabled   bool `json:"listEnabled"`
	ReadEnabled   bool `json:"readEnabled"`
}

func (a changeableAttrs) toSnapshot() changeableAttrsSnapshot {
	return changeableAttrsSnapshot{
		DeleteEnabled: a.deleteEnabled,
		WriteEnabled:  a.writeEnabled,
		ListEnabled:   a.listEnabled,
		ReadEnabled:   a.readEnabled,
	}
}

func (s changeableAttrsSnapshot) toInternal() changeableAttrs {
	return changeableAttrs{
		deleteEnabled: s.DeleteEnabled,
		writeEnabled:  s.WriteEnabled,
		listEnabled:   s.ListEnabled,
		readEnabled:   s.ReadEnabled,
	}
}

// attrsOrDefault resolves a possibly-absent snapshot pointer to fully enabled,
// so restoring a snapshot taken before changeableAttributes existed does not
// spuriously lock every resource.
func attrsOrDefault(s *changeableAttrsSnapshot) changeableAttrs {
	if s == nil {
		return defaultChangeableAttrs()
	}

	return s.toInternal()
}

// registryDataSnapshot is the exported form of registryData.
type registryDataSnapshot struct {
	Reg       driver.AzureRegistry `json:"reg"`
	Password  string               `json:"password,omitempty"`
	Password2 string               `json:"password2,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// ACR stores image metadata/layers, not bulk blobs.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := acrSnapshot{
		Repos:      make(map[string]*repoDataSnapshot, m.repos.Len()),
		Registries: make(map[string]*registryDataSnapshot, m.registries.Len()),
	}

	for name, rd := range m.repos.All() {
		rs, err := snapshotRepo(rd)
		if err != nil {
			return nil, err
		}

		snap.Repos[name] = rs
	}

	for key, rg := range m.registries.All() {
		snap.Registries[key] = &registryDataSnapshot{Reg: rg.reg, Password: rg.password, Password2: rg.password2}
	}

	if err := m.snapshotStores(&snap); err != nil {
		return nil, err
	}

	return json.Marshal(snap)
}

func snapshotRepo(rd *repoData) (*repoDataSnapshot, error) {
	attrs := rd.attrs.toSnapshot()

	rs := &repoDataSnapshot{
		Info:          rd.info,
		Images:        make(map[string]*imageDataSnapshot, rd.images.Len()),
		Policy:        rd.policy,
		ScanOnPush:    rd.scanOnPush,
		TagMutability: rd.tagMutability,
		Attrs:         &attrs,
	}

	if len(rd.tagAttrs) > 0 {
		rs.TagAttrs = make(map[string]changeableAttrsSnapshot, len(rd.tagAttrs))
		for tag, a := range rd.tagAttrs {
			rs.TagAttrs[tag] = a.toSnapshot()
		}
	}

	for tag, img := range rd.images.All() {
		imgAttrs := img.attrs.toSnapshot()
		rs.Images[tag] = &imageDataSnapshot{Detail: img.detail, Layers: img.layers, Attrs: &imgAttrs}
	}

	scans, err := rd.scans.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("acr: snapshot scans: %w", err)
	}

	rs.Scans = scans

	return rs, nil
}

func (m *Mock) snapshotStores(snap *acrSnapshot) error {
	wh, err := m.webhooks.Snapshot()
	if err != nil {
		return fmt.Errorf("acr: snapshot webhooks: %w", err)
	}

	rp, err := m.replications.Snapshot()
	if err != nil {
		return fmt.Errorf("acr: snapshot replications: %w", err)
	}

	snap.Webhooks = wh
	snap.Replications = rp

	return nil
}

// Restore rebuilds every repo and registry under its original key with its images,
// scans, and settings intact.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap acrSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("acr: parse snapshot: %w", err)
	}

	for name, rs := range snap.Repos {
		rd, err := restoreRepo(rs)
		if err != nil {
			return err
		}

		m.repos.Set(name, rd)
	}

	for key, rs := range snap.Registries {
		m.registries.Set(key, &registryData{reg: rs.Reg, password: rs.Password, password2: rs.Password2})
	}

	return m.restoreStores(&snap)
}

func restoreRepo(rs *repoDataSnapshot) (*repoData, error) {
	rd := &repoData{
		info:          rs.Info,
		images:        memstore.New[*imageData](),
		scans:         memstore.New[*driver.ScanResult](),
		policy:        rs.Policy,
		scanOnPush:    rs.ScanOnPush,
		tagMutability: rs.TagMutability,
		attrs:         attrsOrDefault(rs.Attrs),
	}

	if len(rs.TagAttrs) > 0 {
		rd.tagAttrs = make(map[string]changeableAttrs, len(rs.TagAttrs))
		for tag, a := range rs.TagAttrs {
			rd.tagAttrs[tag] = a.toInternal()
		}
	}

	for tag, is := range rs.Images {
		rd.images.Set(tag, &imageData{detail: is.Detail, layers: is.Layers, attrs: attrsOrDefault(is.Attrs)})
	}

	if len(rs.Scans) > 0 {
		if err := rd.scans.LoadSnapshot(rs.Scans); err != nil {
			return nil, fmt.Errorf("acr: restore scans: %w", err)
		}
	}

	return rd, nil
}

func (m *Mock) restoreStores(snap *acrSnapshot) error {
	if len(snap.Webhooks) > 0 {
		if err := m.webhooks.LoadSnapshot(snap.Webhooks); err != nil {
			return fmt.Errorf("acr: restore webhooks: %w", err)
		}
	}

	if len(snap.Replications) > 0 {
		if err := m.replications.LoadSnapshot(snap.Replications); err != nil {
			return fmt.Errorf("acr: restore replications: %w", err)
		}
	}

	return nil
}
