package bedrock

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/bedrock/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// bedrockSnapshot is the full serialized state of the Bedrock mock. Every store
// whose value type is fully exported round-trips through the generic memstore
// helper keyed by its resource id; guardrails carry an exported form because
// guardrailRecord has unexported fields and a mutex that json.Marshal cannot
// see. The seeded foundation-model catalog is intentionally not serialized — a
// fresh mock re-seeds an identical catalog in New() — and neither is the wired
// *config.Options or the monitoring backend.
type bedrockSnapshot struct {
	Jobs                 json.RawMessage               `json:"jobs,omitempty"`
	Models               json.RawMessage               `json:"models,omitempty"`
	Guardrails           map[string]*guardrailSnapshot `json:"guardrails,omitempty"`
	Provisioned          json.RawMessage               `json:"provisioned,omitempty"`
	Tags                 json.RawMessage               `json:"tags,omitempty"`
	AsyncInvokes         json.RawMessage               `json:"asyncInvokes,omitempty"`
	ImportJobs           json.RawMessage               `json:"importJobs,omitempty"`
	CopyJobs             json.RawMessage               `json:"copyJobs,omitempty"`
	EvalJobs             json.RawMessage               `json:"evalJobs,omitempty"`
	InferenceProfiles    json.RawMessage               `json:"inferenceProfiles,omitempty"`
	PromptRouters        json.RawMessage               `json:"promptRouters,omitempty"`
	ARPolicies           json.RawMessage               `json:"arPolicies,omitempty"`
	MarketplaceEndpoints json.RawMessage               `json:"marketplaceEndpoints,omitempty"`
	FMAgreements         json.RawMessage               `json:"fmAgreements,omitempty"`
	Logging              *driver.LoggingConfig         `json:"logging,omitempty"`
}

// guardrailSnapshot mirrors guardrailRecord, promoting its unexported working
// copy, version history, and next-version counter to exported fields so they
// survive JSON. The per-record mutex is deliberately excluded.
type guardrailSnapshot struct {
	Draft    *driver.Guardrail   `json:"draft,omitempty"`
	Versions []*driver.Guardrail `json:"versions,omitempty"`
	NextVer  int                 `json:"nextVer,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// Bedrock holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := bedrockSnapshot{Guardrails: m.snapshotGuardrails()}

	if err := m.snapshotStores(&snap); err != nil {
		return nil, err
	}

	m.logMu.RLock()
	snap.Logging = m.logging
	m.logMu.RUnlock()

	return json.Marshal(snap)
}

// snapshotGuardrails promotes each stored guardrailRecord to its exported form.
func (m *Mock) snapshotGuardrails() map[string]*guardrailSnapshot {
	if m.guardrails.Len() == 0 {
		return nil
	}

	out := make(map[string]*guardrailSnapshot, m.guardrails.Len())

	for name, rec := range m.guardrails.All() {
		rec.mu.RLock()
		out[name] = &guardrailSnapshot{Draft: rec.draft, Versions: rec.versions, NextVer: rec.nextVer}
		rec.mu.RUnlock()
	}

	return out
}

func (m *Mock) snapshotStores(snap *bedrockSnapshot) error {
	dumps := []struct {
		dst *json.RawMessage
		fn  func() ([]byte, error)
	}{
		{&snap.Jobs, m.jobs.Snapshot},
		{&snap.Models, m.models.Snapshot},
		{&snap.Provisioned, m.provisioned.Snapshot},
		{&snap.Tags, m.tags.Snapshot},
		{&snap.AsyncInvokes, m.asyncInvokes.Snapshot},
		{&snap.ImportJobs, m.importJobs.Snapshot},
		{&snap.CopyJobs, m.copyJobs.Snapshot},
		{&snap.EvalJobs, m.evalJobs.Snapshot},
		{&snap.InferenceProfiles, m.inferenceProfiles.Snapshot},
		{&snap.PromptRouters, m.promptRouters.Snapshot},
		{&snap.ARPolicies, m.arPolicies.Snapshot},
		{&snap.MarketplaceEndpoints, m.marketplaceEndpoints.Snapshot},
		{&snap.FMAgreements, m.fmAgreements.Snapshot},
	}

	for _, d := range dumps {
		b, err := d.fn()
		if err != nil {
			return fmt.Errorf("bedrock: snapshot store: %w", err)
		}

		*d.dst = b
	}

	return nil
}

// Restore rebuilds the mock's state under the original identities: every job,
// model, guardrail, and registry resource is reinstated under its stored id.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap bedrockSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("bedrock: parse snapshot: %w", err)
	}

	m.restoreGuardrails(snap.Guardrails)

	if err := m.restoreStores(&snap); err != nil {
		return err
	}

	if snap.Logging != nil {
		m.logMu.Lock()
		m.logging = snap.Logging
		m.logMu.Unlock()
	}

	return nil
}

// restoreGuardrails reinstates each guardrail record under its original name.
func (m *Mock) restoreGuardrails(guardrails map[string]*guardrailSnapshot) {
	for name, s := range guardrails {
		m.guardrails.Set(name, &guardrailRecord{draft: s.Draft, versions: s.Versions, nextVer: s.NextVer})
	}
}

func (m *Mock) restoreStores(snap *bedrockSnapshot) error {
	loads := []struct {
		src json.RawMessage
		fn  func([]byte) error
	}{
		{snap.Jobs, m.jobs.LoadSnapshot},
		{snap.Models, m.models.LoadSnapshot},
		{snap.Provisioned, m.provisioned.LoadSnapshot},
		{snap.Tags, m.tags.LoadSnapshot},
		{snap.AsyncInvokes, m.asyncInvokes.LoadSnapshot},
		{snap.ImportJobs, m.importJobs.LoadSnapshot},
		{snap.CopyJobs, m.copyJobs.LoadSnapshot},
		{snap.EvalJobs, m.evalJobs.LoadSnapshot},
		{snap.InferenceProfiles, m.inferenceProfiles.LoadSnapshot},
		{snap.PromptRouters, m.promptRouters.LoadSnapshot},
		{snap.ARPolicies, m.arPolicies.LoadSnapshot},
		{snap.MarketplaceEndpoints, m.marketplaceEndpoints.LoadSnapshot},
		{snap.FMAgreements, m.fmAgreements.LoadSnapshot},
	}

	for _, l := range loads {
		if len(l.src) == 0 {
			continue
		}

		if err := l.fn(l.src); err != nil {
			return fmt.Errorf("bedrock: restore store: %w", err)
		}
	}

	return nil
}
