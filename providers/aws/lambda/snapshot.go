package lambda

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// lambdaSnapshot is the full serialized state of the AWS Lambda mock. The funcs
// store holds an unexported funcData whose payload (function info, published
// versions carrying their deployment-package config, aliases, resource policies,
// concurrency, URL and AWS-only config) lives in unexported fields, so it is
// promoted to an exported form keyed by function name. The layers store holds an
// unexported *layerData with a nested version store, also promoted; mappings
// holds a fully-exported *driver type and round-trips through the generic
// memstore helper. The live in-process handler funcs (funcData.handler and the
// handlers registry) and the wired opts/monitoring are NOT serialized — they are
// re-registered by the host process, not persistable state. On restore a
// function's handler is re-linked from the handlers registry if one is present.
type lambdaSnapshot struct {
	Funcs    map[string]*funcSnapshot  `json:"funcs,omitempty"`
	Layers   map[string]*layerSnapshot `json:"layers,omitempty"`
	Mappings json.RawMessage           `json:"mappings,omitempty"`
}

// funcSnapshot mirrors funcData. Versions carry each published version's config
// and code identity (CodeSHA256) — not the raw deployment-package bytes, which
// the mock does not retain — so republished code is still identified after a
// restore; aliases are captured by name.
type funcSnapshot struct {
	Info         driver.FunctionInfo                              `json:"info"`
	EngineBacked bool                                             `json:"engineBacked,omitempty"`
	Versions     []*versionSnapshot                               `json:"versions,omitempty"`
	NextVersion  int                                              `json:"nextVersion,omitempty"`
	Aliases      map[string]driver.Alias                          `json:"aliases,omitempty"`
	Concurrency  *driver.ConcurrencyConfig                        `json:"concurrency,omitempty"`
	Policies     map[string]map[string]driver.PermissionStatement `json:"policies,omitempty"`
	// URLConfigs is the Function URL config per qualifier (see funcData.urlConfigs).
	URLConfigs map[string]*driver.FunctionURLConfig `json:"urlConfigs,omitempty"`
	// URLConfig is the legacy single-URL shape a snapshot taken before Function
	// URLs gained qualifier scoping used ("urlConfig", singular). Never written
	// (snapshotFunc only populates URLConfigs), but still read on restore so an
	// old on-disk snapshot's Function URL config isn't silently dropped — see
	// restoreFunc.
	URLConfig *driver.FunctionURLConfig `json:"urlConfig,omitempty"`
	AWSConfig driver.AWSFunctionConfig  `json:"awsConfig"`
	// EventInvokeConfigs is the async-invoke config per qualifier (retries,
	// event age, OnSuccess/OnFailure destinations).
	EventInvokeConfigs map[string]driver.EventInvokeConfig `json:"eventInvokeConfigs,omitempty"`
	// ProvisionedConcurrencyConfigs is the provisioned-concurrency config per
	// qualifier (a published version or alias name).
	ProvisionedConcurrencyConfigs map[string]driver.ProvisionedConcurrencyConfig `json:"provisionedConcurrencyConfigs,omitempty"`
}

// versionSnapshot mirrors versionData (all fields unexported). Config carries the
// published version's configuration and its code identity (CodeSHA256), not the
// raw deployment-package bytes.
type versionSnapshot struct {
	Config     driver.FunctionConfig `json:"config"`
	Version    string                `json:"version"`
	CodeSHA    string                `json:"codeSha,omitempty"`
	RevisionID string                `json:"revisionId,omitempty"`
	CreatedAt  string                `json:"createdAt,omitempty"`
}

// layerSnapshot mirrors layerData; its version store holds a fully-exported
// *driver.LayerVersion and round-trips through the generic helper.
type layerSnapshot struct {
	Versions json.RawMessage `json:"versions,omitempty"`
	NextVer  int             `json:"nextVer,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// published versions retain only their code identity (CodeSHA256) and config,
// not raw deployment-package bytes, so there are no bulk object bodies to gate.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := lambdaSnapshot{}

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

		for name, ld := range m.layers.All() {
			vers, err := ld.versions.Snapshot()
			if err != nil {
				return nil, fmt.Errorf("lambda: snapshot layer versions: %w", err)
			}

			snap.Layers[name] = &layerSnapshot{Versions: vers, NextVer: ld.nextVer}
		}
	}

	mappings, err := m.mappings.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("lambda: snapshot mappings: %w", err)
	}

	snap.Mappings = mappings

	return json.Marshal(snap)
}

func snapshotFunc(fd *funcData) *funcSnapshot {
	fs := &funcSnapshot{
		Info: fd.info, EngineBacked: fd.engineBacked, NextVersion: fd.nextVersion,
		Concurrency: fd.concurrency, Policies: fd.policies, URLConfigs: fd.urlConfigs,
		AWSConfig: fd.awsConfig, EventInvokeConfigs: fd.eventInvokeConfigs,
		ProvisionedConcurrencyConfigs: fd.provisionedConcurrencyConfigs,
	}

	for _, v := range fd.versions {
		fs.Versions = append(fs.Versions, &versionSnapshot{
			Config: v.config, Version: v.version, CodeSHA: v.codeSHA,
			RevisionID: v.revisionID, CreatedAt: v.createdAt,
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

// Restore rebuilds the mock's state under the original identities: every
// function name (and the ARNs/versions/aliases it carries), layer name, and
// event-source-mapping UUID is preserved so a client's identifiers still resolve.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap lambdaSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("lambda: parse snapshot: %w", err)
	}

	for name, fs := range snap.Funcs {
		m.funcs.Set(name, m.restoreFunc(name, fs))
	}

	for name, ls := range snap.Layers {
		ld := &layerData{versions: memstore.New[*driver.LayerVersion](), nextVer: ls.NextVer}
		if len(ls.Versions) > 0 {
			if err := ld.versions.LoadSnapshot(ls.Versions); err != nil {
				return fmt.Errorf("lambda: restore layer versions: %w", err)
			}
		}

		m.layers.Set(name, ld)
	}

	if len(snap.Mappings) > 0 {
		if err := m.mappings.LoadSnapshot(snap.Mappings); err != nil {
			return fmt.Errorf("lambda: restore mappings: %w", err)
		}
	}

	return nil
}

func (m *Mock) restoreFunc(name string, fs *funcSnapshot) funcData {
	fd := funcData{
		info: fs.Info, engineBacked: fs.EngineBacked, nextVersion: fs.NextVersion,
		aliases: memstore.New[*aliasData](), concurrency: fs.Concurrency,
		policies: fs.Policies, urlConfigs: legacyURLConfigs(fs), awsConfig: fs.AWSConfig,
		eventInvokeConfigs:            fs.EventInvokeConfigs,
		provisionedConcurrencyConfigs: fs.ProvisionedConcurrencyConfigs,
	}

	// Re-link the live handler if the host process has registered one for this
	// function; a freshly built mock has none, matching a cold process start.
	m.handlersMu.RLock()
	fd.handler = m.handlers[name]
	m.handlersMu.RUnlock()

	for _, v := range fs.Versions {
		fd.versions = append(fd.versions, &versionData{
			config: v.Config, version: v.Version, codeSHA: v.CodeSHA,
			revisionID: v.RevisionID, createdAt: v.CreatedAt,
		})
	}

	for k, a := range fs.Aliases {
		fd.aliases.Set(k, &aliasData{alias: a})
	}

	return fd
}

// legacyURLConfigs returns fs.URLConfigs, migrating a pre-qualifier-scoping
// snapshot's legacy singular "urlConfig" field (fs.URLConfig) into the new
// per-qualifier map when the snapshot predates it — otherwise a Function URL
// config in an old on-disk snapshot would silently vanish on restore, since
// the new map field simply isn't present in that JSON.
func legacyURLConfigs(fs *funcSnapshot) map[string]*driver.FunctionURLConfig {
	if len(fs.URLConfigs) > 0 || fs.URLConfig == nil {
		return fs.URLConfigs
	}

	return map[string]*driver.FunctionURLConfig{policyKey(fs.URLConfig.Qualifier): fs.URLConfig}
}
