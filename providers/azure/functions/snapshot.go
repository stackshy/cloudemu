package functions

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// functionsSnapshot is the full serialized state of the Azure Functions mock.
// The funcs store holds an unexported funcData whose payload (function info,
// published versions carrying their deployment-package config, and aliases)
// lives in unexported fields, so it is promoted to an exported form keyed by
// function name; layers is promoted likewise for its nested version store. The
// mappings/plans/sites stores hold fully-exported types and round-trip through
// the generic memstore helper. The live in-process handler funcs (funcData.handler
// and the handlers registry) and the wired opts/monitoring are NOT serialized —
// they are re-registered by the host process. On restore a function's handler is
// re-linked from the handlers registry if one is present.
type functionsSnapshot struct {
	Funcs    map[string]*funcSnapshot  `json:"funcs,omitempty"`
	Layers   map[string]*layerSnapshot `json:"layers,omitempty"`
	Mappings json.RawMessage           `json:"mappings,omitempty"`
	Plans    json.RawMessage           `json:"plans,omitempty"`
	Sites    json.RawMessage           `json:"sites,omitempty"`
}

// funcSnapshot mirrors funcData. The mock does not retain raw deployment-package
// bytes (they are deployed to the external FunctionEngine and discarded); what
// survives is the code IDENTITY — CodeSHA256 on info and each version's config —
// plus the aliases, captured by name.
type funcSnapshot struct {
	Info         driver.FunctionInfo       `json:"info"`
	EngineBacked bool                      `json:"engineBacked,omitempty"`
	Versions     []*versionSnapshot        `json:"versions,omitempty"`
	NextVersion  int                       `json:"nextVersion,omitempty"`
	Aliases      map[string]driver.Alias   `json:"aliases,omitempty"`
	Concurrency  *driver.ConcurrencyConfig `json:"concurrency,omitempty"`
}

// versionSnapshot mirrors versionData (all fields unexported).
type versionSnapshot struct {
	Config    driver.FunctionConfig `json:"config"`
	Version   string                `json:"version"`
	CodeSHA   string                `json:"codeSha,omitempty"`
	CreatedAt string                `json:"createdAt,omitempty"`
}

// layerSnapshot mirrors layerData; its version store holds a fully-exported
// *driver.LayerVersion and round-trips through the generic helper.
type layerSnapshot struct {
	Versions json.RawMessage `json:"versions,omitempty"`
	NextVer  int             `json:"nextVer,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// each published version's config (the code identity) is always captured so
// republished functions survive a restore.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := functionsSnapshot{}

	if m.funcs.Len() > 0 {
		snap.Funcs = make(map[string]*funcSnapshot, m.funcs.Len())

		for _, name := range m.funcs.Keys() {
			fd, ok := m.funcs.Get(name)
			if !ok {
				continue
			}

			snap.Funcs[name] = snapshotFunc(&fd)
		}
	}

	if m.layers.Len() > 0 {
		snap.Layers = make(map[string]*layerSnapshot, m.layers.Len())

		for _, name := range m.layers.Keys() {
			ld, ok := m.layers.Get(name)
			if !ok {
				continue
			}

			vers, err := ld.versions.Snapshot()
			if err != nil {
				return nil, fmt.Errorf("functions: snapshot layer versions: %w", err)
			}

			snap.Layers[name] = &layerSnapshot{Versions: vers, NextVer: ld.nextVer}
		}
	}

	if err := m.snapshotGenericStores(&snap); err != nil {
		return nil, err
	}

	return json.Marshal(snap)
}

func (m *Mock) snapshotGenericStores(snap *functionsSnapshot) error {
	dumps := []struct {
		dst *json.RawMessage
		fn  func() ([]byte, error)
	}{
		{&snap.Mappings, m.mappings.Snapshot},
		{&snap.Plans, m.plans.Snapshot},
		{&snap.Sites, m.sites.Snapshot},
	}

	for _, d := range dumps {
		b, err := d.fn()
		if err != nil {
			return fmt.Errorf("functions: snapshot store: %w", err)
		}

		*d.dst = b
	}

	return nil
}

func snapshotFunc(fd *funcData) *funcSnapshot {
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
		fs.Aliases = make(map[string]driver.Alias, fd.aliases.Len())
		for k, ad := range fd.aliases.All() {
			fs.Aliases[k] = ad.alias
		}
	}

	return fs
}

// Restore rebuilds the mock's state under the original identities: every function
// name, layer name, event-source-mapping UUID, App Service plan and site key is
// preserved so a client's identifiers still resolve.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap functionsSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("functions: parse snapshot: %w", err)
	}

	for name, fs := range snap.Funcs {
		m.funcs.Set(name, m.restoreFunc(name, fs))
	}

	for name, ls := range snap.Layers {
		ld := &layerData{versions: memstore.New[*driver.LayerVersion](), nextVer: ls.NextVer}
		if len(ls.Versions) > 0 {
			if err := ld.versions.LoadSnapshot(ls.Versions); err != nil {
				return fmt.Errorf("functions: restore layer versions: %w", err)
			}
		}

		m.layers.Set(name, ld)
	}

	return m.restoreGenericStores(&snap)
}

func (m *Mock) restoreGenericStores(snap *functionsSnapshot) error {
	loads := []struct {
		src json.RawMessage
		fn  func([]byte) error
	}{
		{snap.Mappings, m.mappings.LoadSnapshot},
		{snap.Plans, m.plans.LoadSnapshot},
		{snap.Sites, m.sites.LoadSnapshot},
	}

	for _, l := range loads {
		if len(l.src) == 0 {
			continue
		}

		if err := l.fn(l.src); err != nil {
			return fmt.Errorf("functions: restore store: %w", err)
		}
	}

	return nil
}

func (m *Mock) restoreFunc(name string, fs *funcSnapshot) funcData {
	fd := funcData{
		info: fs.Info, engineBacked: fs.EngineBacked, nextVersion: fs.NextVersion,
		aliases: memstore.New[*aliasData](), concurrency: fs.Concurrency,
	}

	m.handlersMu.RLock()
	fd.handler = m.handlers[name]
	m.handlersMu.RUnlock()

	for _, v := range fs.Versions {
		fd.versions = append(fd.versions, &versionData{
			config: v.Config, version: v.Version, codeSHA: v.CodeSHA, createdAt: v.CreatedAt,
		})
	}

	for k, a := range fs.Aliases {
		fd.aliases.Set(k, &aliasData{alias: a})
	}

	return fd
}
