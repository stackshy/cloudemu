package bedrock

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/bedrock/driver"
)

// --- Inference profiles ---

// CreateInferenceProfile creates an application inference profile. It is ready
// immediately: recorded ACTIVE with type APPLICATION. copyFrom must be a
// foundation-model ARN or a system inference profile ARN. A profile copied from
// a system profile tracks every model the system profile routes to.
func (m *Mock) CreateInferenceProfile(_ context.Context, cfg driver.InferenceProfileConfig) (*driver.InferenceProfile, error) {
	switch {
	case cfg.Name == "":
		return nil, errors.New(errors.InvalidArgument, "inferenceProfileName is required")
	case cfg.ModelSourceCopyFrom == "":
		return nil, errors.New(errors.InvalidArgument, "modelSource.copyFrom is required")
	}

	models := m.copySourceModels(cfg.ModelSourceCopyFrom)
	if models == nil {
		return nil, errors.Newf(errors.InvalidArgument,
			"modelSource.copyFrom %q is not a foundation model ARN or a system inference profile ARN", cfg.ModelSourceCopyFrom)
	}

	for _, existing := range m.inferenceProfiles.SortedValues() {
		if existing.Name == cfg.Name {
			return nil, errors.Newf(errors.AlreadyExists, "inference profile %q already exists", cfg.Name)
		}
	}

	now := m.now()
	id := idgen.BedrockInferenceProfileID()
	arn := idgen.AWSARN("bedrock", m.opts.Region, m.opts.AccountID, "application-inference-profile/"+id)

	profile := &driver.InferenceProfile{
		ARN:         arn,
		ID:          id,
		Name:        cfg.Name,
		Models:      models,
		Status:      driver.InferenceProfileStatusActive,
		Type:        driver.InferenceProfileTypeApplication,
		Description: cfg.Description,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	m.inferenceProfiles.Set(id, profile)
	m.setTags(arn, m.tagsFromMap(cfg.Tags))

	result := cloneInferenceProfile(profile)

	return &result, nil
}

// copySourceModels returns the model ARNs an application profile copied from
// arn tracks, or nil when arn is not a valid copy source.
func (m *Mock) copySourceModels(arn string) []string {
	for i := range m.foundation {
		if m.foundation[i].ModelARN == arn {
			return []string{arn}
		}
	}

	for i := range m.sysProfiles {
		if m.sysProfiles[i].ARN == arn {
			return append([]string(nil), m.sysProfiles[i].Models...)
		}
	}

	return nil
}

// GetInferenceProfile returns a system or application inference profile by ID
// or ARN.
func (m *Mock) GetInferenceProfile(_ context.Context, identifier string) (*driver.InferenceProfile, error) {
	p := m.findSystemProfile(identifier)
	if p == nil {
		p = m.findAppProfile(identifier)
	}

	if p == nil {
		return nil, errors.Newf(errors.NotFound, "inference profile %q not found", identifier)
	}

	result := cloneInferenceProfile(p)

	return &result, nil
}

// findSystemProfile returns the SYSTEM_DEFINED profile with this ID or ARN.
func (m *Mock) findSystemProfile(id string) *driver.InferenceProfile {
	for i := range m.sysProfiles {
		if m.sysProfiles[i].ID == id || m.sysProfiles[i].ARN == id {
			return &m.sysProfiles[i]
		}
	}

	return nil
}

// findAppProfile returns the application profile with this ID or ARN.
func (m *Mock) findAppProfile(id string) *driver.InferenceProfile {
	if p, ok := m.inferenceProfiles.Get(id); ok {
		return p
	}

	for _, p := range m.inferenceProfiles.All() {
		if p.ARN == id {
			return p
		}
	}

	return nil
}

// ListInferenceProfiles lists inference profiles of one type. An empty type
// lists SYSTEM_DEFINED profiles only, which is what AWS does.
func (m *Mock) ListInferenceProfiles(_ context.Context, typeEquals string) ([]driver.InferenceProfile, error) {
	switch typeEquals {
	case "", driver.InferenceProfileTypeSystemDefined:
		out := make([]driver.InferenceProfile, 0, len(m.sysProfiles))
		for i := range m.sysProfiles {
			out = append(out, cloneInferenceProfile(&m.sysProfiles[i]))
		}

		return out, nil
	case driver.InferenceProfileTypeApplication:
		all := m.inferenceProfiles.SortedValues()
		out := make([]driver.InferenceProfile, 0, len(all))

		for _, p := range all {
			out = append(out, cloneInferenceProfile(p))
		}

		return out, nil
	default:
		return nil, errors.Newf(errors.InvalidArgument,
			"1 validation error detected: Value '%s' at 'typeEquals' failed to satisfy constraint: "+
				"Member must satisfy enum value set: [SYSTEM_DEFINED, APPLICATION]", typeEquals)
	}
}

// DeleteInferenceProfile deletes an application inference profile by ID or
// ARN. System profiles are owned by Bedrock and cannot be deleted.
func (m *Mock) DeleteInferenceProfile(_ context.Context, identifier string) error {
	if m.findSystemProfile(identifier) != nil {
		return errors.Newf(errors.InvalidArgument, "inference profile %q is system defined and cannot be deleted", identifier)
	}

	p := m.findAppProfile(identifier)
	if p == nil {
		return errors.Newf(errors.NotFound, "inference profile %q not found", identifier)
	}

	m.inferenceProfiles.Delete(p.ID)

	return nil
}

// --- Prompt routers ---

// CreatePromptRouter creates a prompt router. It is ready immediately: recorded
// AVAILABLE with type custom.
//
//nolint:gocritic // cfg matches the driver interface signature; copied once on entry.
func (m *Mock) CreatePromptRouter(_ context.Context, cfg driver.PromptRouterConfig) (*driver.PromptRouter, error) {
	switch {
	case cfg.Name == "":
		return nil, errors.New(errors.InvalidArgument, "promptRouterName is required")
	case len(cfg.Models) == 0:
		return nil, errors.New(errors.InvalidArgument, "models is required")
	case cfg.ResponseQualityDifference == nil:
		return nil, errors.New(errors.InvalidArgument, "routingCriteria.responseQualityDifference is required")
	case cfg.FallbackModelARN == "":
		return nil, errors.New(errors.InvalidArgument, "fallbackModel.modelArn is required")
	}

	for _, existing := range m.promptRouters.SortedValues() {
		if existing.Name == cfg.Name {
			return nil, errors.Newf(errors.AlreadyExists, "prompt router %q already exists", cfg.Name)
		}
	}

	now := m.now()
	id := idgen.GenerateID("")
	arn := idgen.AWSARN("bedrock", m.opts.Region, m.opts.AccountID, "prompt-router/"+id)

	router := &driver.PromptRouter{
		ARN:                       arn,
		Name:                      cfg.Name,
		Models:                    append([]string(nil), cfg.Models...),
		ResponseQualityDifference: cfg.ResponseQualityDifference,
		FallbackModelARN:          cfg.FallbackModelARN,
		Status:                    driver.PromptRouterStatusAvailable,
		Type:                      driver.PromptRouterTypeCustom,
		Description:               cfg.Description,
		CreatedAt:                 now,
		UpdatedAt:                 now,
	}
	m.promptRouters.Set(arn, router)
	m.setTags(arn, m.tagsFromMap(cfg.Tags))

	result := *router

	return &result, nil
}

// GetPromptRouter returns a prompt router by its ARN.
func (m *Mock) GetPromptRouter(_ context.Context, promptRouterARN string) (*driver.PromptRouter, error) {
	router, ok := m.promptRouters.Get(promptRouterARN)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "prompt router %q not found", promptRouterARN)
	}

	result := clonePromptRouter(router)

	return &result, nil
}

// ListPromptRouters lists all prompt routers.
func (m *Mock) ListPromptRouters(_ context.Context) ([]driver.PromptRouter, error) {
	all := m.promptRouters.SortedValues()
	out := make([]driver.PromptRouter, 0, len(all))

	for _, router := range all {
		out = append(out, clonePromptRouter(router))
	}

	return out, nil
}

// DeletePromptRouter deletes a prompt router by its ARN.
func (m *Mock) DeletePromptRouter(_ context.Context, promptRouterARN string) error {
	if !m.promptRouters.Has(promptRouterARN) {
		return errors.Newf(errors.NotFound, "prompt router %q not found", promptRouterARN)
	}

	m.promptRouters.Delete(promptRouterARN)

	return nil
}

// --- Automated reasoning policies ---

// CreateAutomatedReasoningPolicy creates an automated reasoning policy at the
// DRAFT version.
//
//nolint:gocritic // cfg matches the driver interface signature; copied once on entry.
func (m *Mock) CreateAutomatedReasoningPolicy(
	_ context.Context, cfg driver.AutomatedReasoningPolicyConfig,
) (*driver.AutomatedReasoningPolicy, error) {
	if cfg.Name == "" {
		return nil, errors.New(errors.InvalidArgument, "name is required")
	}

	for _, existing := range m.arPolicies.SortedValues() {
		if existing.Name == cfg.Name {
			return nil, errors.Newf(errors.AlreadyExists, "automated reasoning policy %q already exists", cfg.Name)
		}
	}

	now := m.now()
	id := idgen.GenerateID("")
	arn := idgen.AWSARN("bedrock", m.opts.Region, m.opts.AccountID, "automated-reasoning-policy/"+id)

	policy := &driver.AutomatedReasoningPolicy{
		ARN:              arn,
		ID:               id,
		Name:             cfg.Name,
		Version:          driver.AutomatedReasoningPolicyVersionDraft,
		DefinitionHash:   definitionHash(cfg.PolicyDefinition),
		Description:      cfg.Description,
		PolicyDefinition: copyBytes(cfg.PolicyDefinition),
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if cfg.KMSKeyID != "" {
		policy.KMSKeyARN = cfg.KMSKeyID
	}

	m.arPolicies.Set(arn, policy)
	m.setTags(arn, m.tagsFromMap(cfg.Tags))

	result := *policy

	return &result, nil
}

// GetAutomatedReasoningPolicy returns an automated reasoning policy by its ARN.
func (m *Mock) GetAutomatedReasoningPolicy(_ context.Context, policyARN string) (*driver.AutomatedReasoningPolicy, error) {
	policy, ok := m.arPolicies.Get(policyARN)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "automated reasoning policy %q not found", policyARN)
	}

	result := cloneARPolicy(policy)

	return &result, nil
}

// ListAutomatedReasoningPolicies lists all automated reasoning policies.
func (m *Mock) ListAutomatedReasoningPolicies(_ context.Context) ([]driver.AutomatedReasoningPolicy, error) {
	all := m.arPolicies.SortedValues()
	out := make([]driver.AutomatedReasoningPolicy, 0, len(all))

	for _, policy := range all {
		out = append(out, cloneARPolicy(policy))
	}

	return out, nil
}

// UpdateAutomatedReasoningPolicy updates an existing policy's definition, name,
// and description, refreshing its definition hash and update time.
func (m *Mock) UpdateAutomatedReasoningPolicy(
	_ context.Context, policyARN string, upd driver.AutomatedReasoningPolicyUpdate,
) (*driver.AutomatedReasoningPolicy, error) {
	stored, ok := m.arPolicies.Get(policyARN)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "automated reasoning policy %q not found", policyARN)
	}

	// Copy-on-write: mutate a copy, not the stored pointer, so concurrent
	// Get/List readers never race the write.
	policy := *stored

	if upd.Name != "" {
		policy.Name = upd.Name
	}

	if upd.Description != "" {
		policy.Description = upd.Description
	}

	if len(upd.PolicyDefinition) != 0 {
		policy.PolicyDefinition = copyBytes(upd.PolicyDefinition)
	}

	policy.DefinitionHash = definitionHash(policy.PolicyDefinition)
	policy.UpdatedAt = m.now()
	m.arPolicies.Set(policyARN, &policy)

	result := policy

	return &result, nil
}

// DeleteAutomatedReasoningPolicy deletes an automated reasoning policy by its ARN.
func (m *Mock) DeleteAutomatedReasoningPolicy(_ context.Context, policyARN string) error {
	if !m.arPolicies.Has(policyARN) {
		return errors.Newf(errors.NotFound, "automated reasoning policy %q not found", policyARN)
	}

	m.arPolicies.Delete(policyARN)

	return nil
}

// definitionHash returns a deterministic non-empty hash of a policy definition,
// used as the policy's concurrency token.
func definitionHash(def []byte) string {
	sum := sha256.Sum256(def)

	return hex.EncodeToString(sum[:])
}

// cloneInferenceProfile returns a value copy whose Models slice does not alias
// the stored profile, so callers can't mutate internal state via the result.
func cloneInferenceProfile(p *driver.InferenceProfile) driver.InferenceProfile {
	out := *p
	out.Models = append([]string(nil), p.Models...)

	return out
}

// clonePromptRouter returns a value copy whose Models slice does not alias the
// stored router.
func clonePromptRouter(p *driver.PromptRouter) driver.PromptRouter {
	out := *p
	out.Models = append([]string(nil), p.Models...)

	return out
}

// cloneARPolicy returns a value copy whose PolicyDefinition does not alias the
// stored policy.
func cloneARPolicy(p *driver.AutomatedReasoningPolicy) driver.AutomatedReasoningPolicy {
	out := *p
	out.PolicyDefinition = copyBytes(p.PolicyDefinition)

	return out
}
