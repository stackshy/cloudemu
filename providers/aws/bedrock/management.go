package bedrock

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/bedrock/driver"
)

// guardrailDraftVersion is the working version assigned to a freshly created
// guardrail, matching the real Bedrock "DRAFT" version.
const guardrailDraftVersion = "DRAFT"

// --- Guardrails ---

// CreateGuardrail creates a guardrail in the READY state.
//
//nolint:gocritic // cfg matches the driver interface signature; copied once on entry.
func (m *Mock) CreateGuardrail(_ context.Context, cfg driver.GuardrailConfig) (*driver.Guardrail, error) {
	if err := validateGuardrailConfig(cfg); err != nil {
		return nil, err
	}

	if m.guardrails.Has(cfg.Name) {
		return nil, errors.Newf(errors.AlreadyExists, "guardrail %q already exists", cfg.Name)
	}

	id := idgen.GenerateID("gr-")
	now := m.now()
	g := &driver.Guardrail{
		ID:                      id,
		ARN:                     idgen.AWSARN("bedrock", m.opts.Region, m.opts.AccountID, "guardrail/"+id),
		Name:                    cfg.Name,
		Description:             cfg.Description,
		Version:                 guardrailDraftVersion,
		Status:                  driver.GuardrailReady,
		BlockedInputMessaging:   cfg.BlockedInputMessaging,
		BlockedOutputsMessaging: cfg.BlockedOutputsMessaging,
		CreatedAt:               now,
		UpdatedAt:               now,
		GuardrailPolicies:       deepCopyGuardrailPolicies(cfg.GuardrailPolicies),
	}

	if cfg.KMSKeyID != "" {
		g.KMSKeyARN = cfg.KMSKeyID
	}

	m.guardrails.Set(cfg.Name, &guardrailRecord{draft: g, nextVer: 1})
	m.setTags(g.ARN, m.tagsFromMap(cfg.Tags))

	result := *g

	return &result, nil
}

// GetGuardrail returns a guardrail by name or ARN. An empty or "DRAFT" version
// returns the working copy; a numbered version returns that snapshot.
func (m *Mock) GetGuardrail(_ context.Context, identifier, version string) (*driver.Guardrail, error) {
	rec := m.findGuardrailRecord(identifier)
	if rec == nil {
		return nil, errors.Newf(errors.NotFound, "guardrail %q not found", identifier)
	}

	if version != "" && version != guardrailDraftVersion {
		result, ok := rec.versionSnapshot(version)
		if !ok {
			return nil, errors.Newf(errors.NotFound, "guardrail %q version %q not found", identifier, version)
		}

		return &result, nil
	}

	result := rec.draftSnapshot()

	return &result, nil
}

// ListGuardrails lists guardrails. When identifier is set, it returns one
// summary per version (DRAFT plus each numbered snapshot) of that guardrail;
// otherwise it returns one summary per guardrail (its DRAFT).
func (m *Mock) ListGuardrails(_ context.Context, identifier string) ([]driver.Guardrail, error) {
	if identifier != "" {
		rec := m.findGuardrailRecord(identifier)
		if rec == nil {
			return nil, errors.Newf(errors.NotFound, "guardrail %q not found", identifier)
		}

		return rec.allSnapshots(), nil
	}

	all := m.guardrails.All()
	out := make([]driver.Guardrail, 0, len(all))

	for _, rec := range all {
		out = append(out, rec.draftSnapshot())
	}

	return out, nil
}

// UpdateGuardrail updates a guardrail's mutable DRAFT working copy. Numbered
// version snapshots are immutable and left untouched.
//
//nolint:gocritic // cfg matches the driver interface signature; copied once on entry.
func (m *Mock) UpdateGuardrail(_ context.Context, identifier string, cfg driver.GuardrailConfig) (*driver.Guardrail, error) {
	rec := m.findGuardrailRecord(identifier)
	if rec == nil {
		return nil, errors.Newf(errors.NotFound, "guardrail %q not found", identifier)
	}

	rec.mu.Lock()
	g := rec.draft
	oldName := g.Name
	g.Name = orDefault(cfg.Name, g.Name)
	g.Description = cfg.Description
	g.BlockedInputMessaging = orDefault(cfg.BlockedInputMessaging, g.BlockedInputMessaging)
	g.BlockedOutputsMessaging = orDefault(cfg.BlockedOutputsMessaging, g.BlockedOutputsMessaging)
	g.GuardrailPolicies = deepCopyGuardrailPolicies(cfg.GuardrailPolicies)
	g.UpdatedAt = m.now()
	newName := g.Name
	result := cloneGuardrail(g)
	rec.mu.Unlock()

	// Records are keyed by name; re-key when an update renames one so lookups
	// by the new name keep working.
	if newName != oldName {
		m.guardrails.Delete(oldName)
		m.guardrails.Set(newName, rec)
	}

	return &result, nil
}

// DeleteGuardrail deletes a guardrail by name or ARN. An empty version deletes
// the whole guardrail (DRAFT and all snapshots); a specific version deletes
// just that numbered snapshot.
func (m *Mock) DeleteGuardrail(_ context.Context, identifier, version string) error {
	rec := m.findGuardrailRecord(identifier)
	if rec == nil {
		return errors.Newf(errors.NotFound, "guardrail %q not found", identifier)
	}

	if version == "" {
		rec.mu.RLock()
		name := rec.draft.Name
		rec.mu.RUnlock()

		m.guardrails.Delete(name)

		return nil
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()

	for i, v := range rec.versions {
		if v.Version == version {
			rec.versions = append(rec.versions[:i], rec.versions[i+1:]...)

			return nil
		}
	}

	return errors.Newf(errors.NotFound, "guardrail %q version %q not found", identifier, version)
}

func (m *Mock) findGuardrailRecord(id string) *guardrailRecord {
	if rec, ok := m.guardrails.Get(id); ok {
		return rec
	}

	for _, rec := range m.guardrails.All() {
		if rec.draft.ID == id || rec.draft.ARN == id {
			return rec
		}
	}

	return nil
}

func validateGuardrailConfig(cfg driver.GuardrailConfig) error { //nolint:gocritic // cfg by value matches interface
	switch {
	case cfg.Name == "":
		return errors.New(errors.InvalidArgument, "name is required")
	case cfg.BlockedInputMessaging == "":
		return errors.New(errors.InvalidArgument, "blockedInputMessaging is required")
	case cfg.BlockedOutputsMessaging == "":
		return errors.New(errors.InvalidArgument, "blockedOutputsMessaging is required")
	default:
		return nil
	}
}

// --- Provisioned model throughput ---

// CreateProvisionedModelThroughput provisions throughput for a model in the
// InService state.
//
//nolint:gocritic // cfg matches the driver interface signature; copied once on entry.
func (m *Mock) CreateProvisionedModelThroughput(
	_ context.Context, cfg driver.ProvisionedThroughputConfig,
) (*driver.ProvisionedThroughput, error) {
	switch {
	case cfg.ProvisionedModelName == "":
		return nil, errors.New(errors.InvalidArgument, "provisionedModelName is required")
	case cfg.ModelID == "":
		return nil, errors.New(errors.InvalidArgument, "modelId is required")
	case cfg.ModelUnits <= 0:
		return nil, errors.New(errors.InvalidArgument, "modelUnits must be positive")
	}

	modelARN := m.resolveModelARN(cfg.ModelID)
	if modelARN == "" {
		return nil, errors.Newf(errors.InvalidArgument, "model %q not found", cfg.ModelID)
	}

	if m.provisioned.Has(cfg.ProvisionedModelName) {
		return nil, errors.Newf(errors.AlreadyExists, "provisioned model %q already exists", cfg.ProvisionedModelName)
	}

	now := m.now()
	pt := &driver.ProvisionedThroughput{
		ARN:                idgen.AWSARN("bedrock", m.opts.Region, m.opts.AccountID, "provisioned-model/"+idgen.GenerateID("")),
		Name:               cfg.ProvisionedModelName,
		ModelARN:           modelARN,
		DesiredModelARN:    modelARN,
		FoundationModelARN: modelARN,
		ModelUnits:         cfg.ModelUnits,
		DesiredModelUnits:  cfg.ModelUnits,
		Status:             driver.ProvisionedInService,
		CommitmentDuration: cfg.CommitmentDuration,
		CreationTime:       now,
		LastModifiedTime:   now,
	}
	m.provisioned.Set(cfg.ProvisionedModelName, pt)
	m.setTags(pt.ARN, m.tagsFromMap(cfg.Tags))

	result := *pt

	return &result, nil
}

// GetProvisionedModelThroughput returns a provisioned throughput by name or ARN.
func (m *Mock) GetProvisionedModelThroughput(_ context.Context, identifier string) (*driver.ProvisionedThroughput, error) {
	pt := m.findProvisioned(identifier)
	if pt == nil {
		return nil, errors.Newf(errors.NotFound, "provisioned model %q not found", identifier)
	}

	result := *pt

	return &result, nil
}

// ListProvisionedModelThroughputs lists all provisioned throughputs.
func (m *Mock) ListProvisionedModelThroughputs(_ context.Context) ([]driver.ProvisionedThroughput, error) {
	all := m.provisioned.All()
	out := make([]driver.ProvisionedThroughput, 0, len(all))

	for _, pt := range all {
		out = append(out, *pt)
	}

	return out, nil
}

// DeleteProvisionedModelThroughput deletes a provisioned throughput by name or ARN.
func (m *Mock) DeleteProvisionedModelThroughput(_ context.Context, identifier string) error {
	pt := m.findProvisioned(identifier)
	if pt == nil {
		return errors.Newf(errors.NotFound, "provisioned model %q not found", identifier)
	}

	m.provisioned.Delete(pt.Name)

	return nil
}

func (m *Mock) findProvisioned(id string) *driver.ProvisionedThroughput {
	if pt, ok := m.provisioned.Get(id); ok {
		return pt
	}

	for _, pt := range m.provisioned.All() {
		if pt.ARN == id {
			return pt
		}
	}

	return nil
}

// resolveModelARN returns the ARN of a foundation or custom model, or "" if
// the identifier names neither.
func (m *Mock) resolveModelARN(id string) string {
	if fm := m.findFoundation(id); fm != nil {
		return fm.ModelARN
	}

	if cm := m.findCustom(id); cm != nil {
		return cm.ModelARN
	}

	return ""
}

// --- Model invocation logging ---

// PutModelInvocationLoggingConfiguration sets the invocation logging config.
func (m *Mock) PutModelInvocationLoggingConfiguration(_ context.Context, cfg driver.LoggingConfig) error {
	stored := cfg

	m.logMu.Lock()
	m.logging = &stored
	m.logMu.Unlock()

	return nil
}

// GetModelInvocationLoggingConfiguration returns the invocation logging config,
// or nil if none is set.
func (m *Mock) GetModelInvocationLoggingConfiguration(_ context.Context) (*driver.LoggingConfig, error) {
	m.logMu.RLock()
	defer m.logMu.RUnlock()

	if m.logging == nil {
		return nil, nil //nolint:nilnil // an unset logging config is a valid, non-error result
	}

	result := *m.logging

	return &result, nil
}

// DeleteModelInvocationLoggingConfiguration clears the invocation logging config.
func (m *Mock) DeleteModelInvocationLoggingConfiguration(_ context.Context) error {
	m.logMu.Lock()
	m.logging = nil
	m.logMu.Unlock()

	return nil
}

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}

	return v
}
