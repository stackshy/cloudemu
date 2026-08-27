package cloudfunctions

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// funcsSnapshot is the full serialized state of the Cloud Functions mock. The
// funcs and layers stores hold unexported value types (funcData/layerData) whose
// fields — the function info, its published versions, its aliases (a nested store
// of unexported aliasData), and its layer versions — are all unexported and
// invisible to json.Marshal, so both are promoted to exported forms keyed by
// their resource name. The mappings store holds a fully-exported
// *EventSourceMappingInfo and round-trips through the generic memstore helper.
// The registered Go handlers (funcData.handler and m.handlers), the wired
// monitoring/opts deps, and the mutex are intentionally not serialized: a
// restored function reports its stored config and versions, and a Go handler is
// re-registered by the host process. Uploaded deployment-package bytes are not
// stored in this mock at all (they are pushed to the configured FunctionEngine on
// deploy), so what survives is the function configuration and per-version
// CodeSHA, not raw code bytes.
type funcsSnapshot struct {
	Funcs    map[string]*funcSnapshot  `json:"funcs,omitempty"`
	Layers   map[string]*layerSnapshot `json:"layers,omitempty"`
	Mappings json.RawMessage           `json:"mappings,omitempty"`
}

// funcSnapshot mirrors funcData, promoting its unexported info/versions/aliases
// and concurrency. The live handler func is intentionally omitted.
type funcSnapshot struct {
	Info         driver.FunctionInfo       `json:"info"`
	EngineBacked bool                      `json:"engineBacked,omitempty"`
	Versions     []*versionSnapshot        `json:"versions,omitempty"`
	NextVersion  int                       `json:"nextVersion,omitempty"`
	Aliases      map[string]*aliasSnapshot `json:"aliases,omitempty"`
	Concurrency  *driver.ConcurrencyConfig `json:"concurrency,omitempty"`
}

// versionSnapshot mirrors versionData (all fields unexported).
type versionSnapshot struct {
	Config    driver.FunctionConfig `json:"config"`
	Version   string                `json:"version"`
	CodeSHA   string                `json:"codeSHA,omitempty"`
	CreatedAt string                `json:"createdAt,omitempty"`
}

// aliasSnapshot mirrors aliasData (whose single field is unexported).
type aliasSnapshot struct {
	Alias driver.Alias `json:"alias"`
}

// layerSnapshot mirrors layerData: NextVer plus the nested versions store, which
// holds a fully-exported *driver.LayerVersion and round-trips through the generic
// helper.
type layerSnapshot struct {
	Versions json.RawMessage `json:"versions,omitempty"`
	NextVer  int             `json:"nextVer,omitempty"`
}

// Snapshot captures every function, layer, and mapping as JSON. includeAssets is
// unused — see the funcsSnapshot note on deployment bytes.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	snap := funcsSnapshot{Funcs: m.snapshotFuncs(), Layers: m.snapshotLayers()}

	mappings, err := m.mappings.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("cloudfunctions: snapshot mappings: %w", err)
	}

	snap.Mappings = mappings

	return json.Marshal(snap)
}

// snapshotFuncs promotes every funcData into its exported form. The caller holds
// m.mu.
func (m *Mock) snapshotFuncs() map[string]*funcSnapshot {
	if m.funcs.Len() == 0 {
		return nil
	}

	out := make(map[string]*funcSnapshot, m.funcs.Len())

	for _, name := range m.funcs.Keys() {
		fd, ok := m.funcs.Get(name)
		if !ok {
			continue
		}

		fs := &funcSnapshot{
			Info: fd.info, EngineBacked: fd.engineBacked,
			NextVersion: fd.nextVersion, Concurrency: fd.concurrency,
		}

		for _, v := range fd.versions {
			fs.Versions = append(fs.Versions, &versionSnapshot{
				Config: v.config, Version: v.version, CodeSHA: v.codeSHA, CreatedAt: v.createdAt,
			})
		}

		if fd.aliases != nil && fd.aliases.Len() > 0 {
			fs.Aliases = make(map[string]*aliasSnapshot, fd.aliases.Len())
			for an, ad := range fd.aliases.All() {
				fs.Aliases[an] = &aliasSnapshot{Alias: ad.alias}
			}
		}

		out[name] = fs
	}

	return out
}

// snapshotLayers promotes every layerData, dumping its nested versions store
// through the generic helper. The caller holds m.mu.
func (m *Mock) snapshotLayers() map[string]*layerSnapshot {
	if m.layers.Len() == 0 {
		return nil
	}

	out := make(map[string]*layerSnapshot, m.layers.Len())

	for name, ld := range m.layers.All() {
		ls := &layerSnapshot{NextVer: ld.nextVer}
		if b, err := ld.versions.Snapshot(); err == nil {
			ls.Versions = b
		}

		out[name] = ls
	}

	return out
}

// Restore rebuilds every function, layer, and mapping under its original name, so
// aliases and versions survive unchanged.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap funcsSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("cloudfunctions: parse snapshot: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.restoreFuncs(snap.Funcs)
	m.restoreLayers(snap.Layers)

	if len(snap.Mappings) > 0 {
		if err := m.mappings.LoadSnapshot(snap.Mappings); err != nil {
			return fmt.Errorf("cloudfunctions: restore mappings: %w", err)
		}
	}

	return nil
}

// restoreFuncs reinstates each promoted funcData. The handler stays nil until the
// host re-registers it. The caller holds m.mu.
func (m *Mock) restoreFuncs(funcs map[string]*funcSnapshot) {
	for name, fs := range funcs {
		fd := funcData{
			info: fs.Info, engineBacked: fs.EngineBacked,
			nextVersion: fs.NextVersion, concurrency: fs.Concurrency,
			aliases: memstore.New[*aliasData](),
		}

		for _, v := range fs.Versions {
			fd.versions = append(fd.versions, &versionData{
				config: v.Config, version: v.Version, codeSHA: v.CodeSHA, createdAt: v.CreatedAt,
			})
		}

		for an, as := range fs.Aliases {
			fd.aliases.Set(an, &aliasData{alias: as.Alias})
		}

		m.funcs.Set(name, fd)
	}
}

// restoreLayers reinstates each promoted layerData and its nested versions store.
// The caller holds m.mu.
func (m *Mock) restoreLayers(layers map[string]*layerSnapshot) {
	for name, ls := range layers {
		ld := &layerData{versions: memstore.New[*driver.LayerVersion](), nextVer: ls.NextVer}
		if len(ls.Versions) > 0 {
			_ = ld.versions.LoadSnapshot(ls.Versions)
		}

		m.layers.Set(name, ld)
	}
}
