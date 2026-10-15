package bedrockagent

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/bedrockagent/driver"
)

// Flow alias constraints from the service model.
const (
	flowAliasRoutes   = 1
	minFlowConcurrent = 1
	maxFlowConcurrent = 100
	flowAliasMarker   = "/alias/"
)

// CreateFlowVersion publishes the flow's DRAFT as the next numbered version.
// The version's ARN is the flow's ARN, as in the service model.
func (m *Mock) CreateFlowVersion(_ context.Context, flowID, description string) (*driver.Flow, error) {
	flowID = resourceID(flowID, "flow/")

	var v violations

	v.description(description)

	if err := v.err(); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	draft, ok := m.flows.Get(flowID)
	if !ok {
		return nil, flowNotFound(flowID)
	}

	snap := cloneFlow(draft)
	snap.Version = m.nextVersion("flow/" + flowID)
	snap.Status = driver.FlowPrepared
	snap.Description = orDefault(description, draft.Description)
	snap.CreatedAt = m.now()
	snap.UpdatedAt = snap.CreatedAt
	m.flowVersions.Set(versionKey(flowID, snap.Version), &snap)

	result := cloneFlow(&snap)

	return &result, nil
}

// GetFlowVersion returns a numbered version of a flow.
func (m *Mock) GetFlowVersion(_ context.Context, flowID, version string) (*driver.Flow, error) {
	flowID = resourceID(flowID, "flow/")

	var v violations

	v.numericVersion("flowVersion", version)

	if err := v.err(); err != nil {
		return nil, err
	}

	if !m.flows.Has(flowID) {
		return nil, flowNotFound(flowID)
	}

	stored, ok := m.flowVersions.Get(versionKey(flowID, version))
	if !ok {
		return nil, versionNotFound("flow", flowID, version)
	}

	result := cloneFlow(stored)

	return &result, nil
}

// ListFlowVersions lists one page of a flow's versions, DRAFT first.
func (m *Mock) ListFlowVersions(_ context.Context, flowID string, page driver.Page) ([]driver.Flow, string, error) {
	flowID = resourceID(flowID, "flow/")

	draft, ok := m.flows.Get(flowID)
	if !ok {
		return nil, "", flowNotFound(flowID)
	}

	out := append([]driver.Flow{cloneFlow(draft)}, childrenOf(m.flowVersions, flowID, cloneFlow)...)
	sortByVersion(out, func(f driver.Flow) string { return f.Version })

	return paginate(out, page)
}

// DeleteFlowVersion deletes a numbered version. A version an alias routes to is
// in use: deleting it needs skipResourceInUseCheck.
func (m *Mock) DeleteFlowVersion(_ context.Context, flowID, version string, skipResourceInUseCheck bool) error {
	flowID = resourceID(flowID, "flow/")

	var v violations

	v.numericVersion("flowVersion", version)

	if err := v.err(); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.flows.Has(flowID) {
		return flowNotFound(flowID)
	}

	key := versionKey(flowID, version)
	if !m.flowVersions.Has(key) {
		return versionNotFound("flow", flowID, version)
	}

	if !skipResourceInUseCheck {
		for _, a := range m.flowAliases.SortedValues() {
			if a.FlowID == flowID && routesToFlowVersion(a, version) {
				return errors.Newf(errors.FailedPrecondition,
					"Version %s of flow %s is in use by alias %s. Remove it from the alias or set skipResourceInUseCheck.",
					version, flowID, a.ID)
			}
		}
	}

	m.flowVersions.Delete(key)

	return nil
}

// CreateFlowAlias creates an alias routing to one version of a flow (DRAFT or
// an existing number). Names are unique per flow.
//
//nolint:gocritic // cfg matches the driver interface signature; copied once on entry.
func (m *Mock) CreateFlowAlias(_ context.Context, cfg driver.FlowAliasConfig) (*driver.FlowAlias, error) {
	cfg.FlowID = resourceID(cfg.FlowID, "flow/")

	if err := validateFlowAlias(cfg); err != nil {
		return nil, err
	}

	if err := validateTags(cfg.Tags); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	flow, err := m.checkFlowAliasTarget(cfg, "")
	if err != nil {
		return nil, err
	}

	id, err := newID(m.flowAliases)
	if err != nil {
		return nil, err
	}

	now := m.now()
	alias := &driver.FlowAlias{
		ID:                   id,
		ARN:                  flow.ARN + flowAliasMarker + id,
		FlowID:               cfg.FlowID,
		Name:                 cfg.Name,
		Description:          cfg.Description,
		RoutingConfiguration: append([]driver.FlowAliasRoute(nil), cfg.RoutingConfiguration...),
		Concurrency:          copyConcurrency(cfg.Concurrency),
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	m.flowAliases.Set(id, alias)
	m.putTags(alias.ARN, cfg.Tags)

	result := cloneFlowAlias(alias)

	return &result, nil
}

// GetFlowAlias returns an alias of a flow.
func (m *Mock) GetFlowAlias(_ context.Context, flowID, aliasID string) (*driver.FlowAlias, error) {
	alias, err := m.findFlowAlias(resourceID(flowID, "flow/"), aliasID)
	if err != nil {
		return nil, err
	}

	result := cloneFlowAlias(alias)

	return &result, nil
}

// ListFlowAliases lists one page of a flow's aliases.
func (m *Mock) ListFlowAliases(_ context.Context, flowID string, page driver.Page) ([]driver.FlowAlias, string, error) {
	flowID = resourceID(flowID, "flow/")

	if !m.flows.Has(flowID) {
		return nil, "", flowNotFound(flowID)
	}

	var out []driver.FlowAlias

	for _, a := range m.flowAliases.SortedValues() {
		if a.FlowID == flowID {
			out = append(out, cloneFlowAlias(a))
		}
	}

	return paginate(out, page)
}

// UpdateFlowAlias replaces an alias's name, description, routing and
// concurrency.
//
//nolint:gocritic // cfg matches the driver interface signature; copied once on entry.
func (m *Mock) UpdateFlowAlias(_ context.Context, aliasID string, cfg driver.FlowAliasConfig) (*driver.FlowAlias, error) {
	cfg.FlowID = resourceID(cfg.FlowID, "flow/")

	if err := validateFlowAlias(cfg); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	alias, err := m.findFlowAlias(cfg.FlowID, aliasID)
	if err != nil {
		return nil, err
	}

	if _, err := m.checkFlowAliasTarget(cfg, alias.ID); err != nil {
		return nil, err
	}

	updated := cloneFlowAlias(alias)
	updated.Name = cfg.Name
	updated.Description = cfg.Description
	updated.RoutingConfiguration = append([]driver.FlowAliasRoute(nil), cfg.RoutingConfiguration...)
	updated.Concurrency = copyConcurrency(cfg.Concurrency)
	updated.UpdatedAt = m.now()
	m.flowAliases.Set(alias.ID, &updated)

	result := cloneFlowAlias(&updated)

	return &result, nil
}

// DeleteFlowAlias deletes an alias and its tags and returns what it deleted.
func (m *Mock) DeleteFlowAlias(_ context.Context, flowID, aliasID string) (*driver.FlowAlias, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	alias, err := m.findFlowAlias(resourceID(flowID, "flow/"), aliasID)
	if err != nil {
		return nil, err
	}

	m.flowAliases.Delete(alias.ID)
	m.dropTags(alias.ARN)

	result := cloneFlowAlias(alias)

	return &result, nil
}

// deleteFlowChildren removes a deleted flow's versions, aliases and alias tags.
// Callers hold m.mu.
func (m *Mock) deleteFlowChildren(flowID string) {
	deleteByPrefix(m.flowVersions, flowID+"/")
	m.versionSeq.Delete("flow/" + flowID)

	for id, a := range m.flowAliases.All() {
		if a.FlowID == flowID {
			m.flowAliases.Delete(id)
			m.dropTags(a.ARN)
		}
	}
}

// findFlowAlias resolves an alias id or ARN scoped to its flow.
func (m *Mock) findFlowAlias(flowID, aliasID string) (*driver.FlowAlias, error) {
	if i := strings.LastIndex(aliasID, flowAliasMarker); i >= 0 {
		aliasID = aliasID[i+len(flowAliasMarker):]
	}

	if !m.flows.Has(flowID) {
		return nil, flowNotFound(flowID)
	}

	alias, ok := m.flowAliases.Get(aliasID)
	if !ok || alias.FlowID != flowID {
		return nil, errors.Newf(errors.NotFound, "flow alias %q not found", aliasID)
	}

	return alias, nil
}

// checkFlowAliasTarget resolves the flow and the routed version and enforces
// name uniqueness. Callers hold m.mu.
//
//nolint:gocritic // cfg matches the driver interface signature.
func (m *Mock) checkFlowAliasTarget(cfg driver.FlowAliasConfig, selfID string) (*driver.Flow, error) {
	flow, ok := m.flows.Get(cfg.FlowID)
	if !ok {
		return nil, flowNotFound(cfg.FlowID)
	}

	for _, r := range cfg.RoutingConfiguration {
		if r.FlowVersion != driver.DraftVersion && !m.flowVersions.Has(versionKey(cfg.FlowID, r.FlowVersion)) {
			return nil, versionNotFound("flow", cfg.FlowID, r.FlowVersion)
		}
	}

	for _, a := range m.flowAliases.All() {
		if a.FlowID == cfg.FlowID && a.ID != selfID && a.Name == cfg.Name {
			return nil, errors.Newf(errors.AlreadyExists, "An alias named %s already exists for flow %s.", cfg.Name, cfg.FlowID)
		}
	}

	return flow, nil
}

//nolint:gocritic // cfg matches the driver interface signature.
func validateFlowAlias(cfg driver.FlowAliasConfig) error {
	var v violations

	v.id("flowIdentifier", cfg.FlowID)
	v.name("name", cfg.Name)
	v.description(cfg.Description)
	v.clientToken(cfg.ClientToken)

	if len(cfg.RoutingConfiguration) != flowAliasRoutes {
		v.addf("Value at 'routingConfiguration' failed to satisfy constraint: "+
			"Member must have length less than or equal to %d, Member must have length greater than or equal to %d",
			flowAliasRoutes, flowAliasRoutes)
	}

	for _, r := range cfg.RoutingConfiguration {
		v.version("routingConfiguration.member.flowVersion", r.FlowVersion)
	}

	if c := cfg.Concurrency; c != nil {
		v.enum("concurrencyConfiguration.type", c.Type, driver.FlowConcurrencyAutomatic, driver.FlowConcurrencyManual)
		v.required("concurrencyConfiguration.type", c.Type == "")
		v.required("concurrencyConfiguration.maxConcurrency", c.Type == driver.FlowConcurrencyManual && c.MaxConcurrency == nil)

		if c.MaxConcurrency != nil && (*c.MaxConcurrency < minFlowConcurrent || *c.MaxConcurrency > maxFlowConcurrent) {
			v.addf("Value '%d' at 'concurrencyConfiguration.maxConcurrency' failed to satisfy constraint: "+
				"Member must have value between %d and %d", *c.MaxConcurrency, minFlowConcurrent, maxFlowConcurrent)
		}
	}

	return v.err()
}

func routesToFlowVersion(a *driver.FlowAlias, version string) bool {
	for _, r := range a.RoutingConfiguration {
		if r.FlowVersion == version {
			return true
		}
	}

	return false
}

func cloneFlowAlias(a *driver.FlowAlias) driver.FlowAlias {
	out := *a
	out.RoutingConfiguration = append([]driver.FlowAliasRoute(nil), a.RoutingConfiguration...)
	out.Concurrency = copyConcurrency(a.Concurrency)

	return out
}

func copyConcurrency(c *driver.FlowAliasConcurrency) *driver.FlowAliasConcurrency {
	if c == nil {
		return nil
	}

	out := *c

	if c.MaxConcurrency != nil {
		n := *c.MaxConcurrency
		out.MaxConcurrency = &n
	}

	return &out
}

func flowNotFound(id string) error {
	return errors.Newf(errors.NotFound, "flow %q not found", id)
}
