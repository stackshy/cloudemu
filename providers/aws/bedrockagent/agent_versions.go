package bedrockagent

import (
	"context"
	"slices"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/bedrockagent/driver"
)

// Alias constants from the service model and the Bedrock console.
const (
	maxAgentAliasRoutes      = 1
	aliasAcceptInvocations   = "ACCEPT_INVOCATIONS"
	aliasRejectInvocations   = "REJECT_INVOCATIONS"
	agentAliasIDPattern      = `(\bTSTALIASID\b|[0-9a-zA-Z]+)`
	agentVersionPrepared     = driver.AgentPrepared
	testAliasDescription     = "Test Alias for Agent"
	errTestAliasImmutableMsg = "The test alias TSTALIASID of an agent can't be modified or deleted."
)

// CreateAgentAlias creates an alias of an agent in the PREPARED state. Without
// a routingConfiguration it cuts a new numbered version of the agent's DRAFT
// (with its action groups, knowledge bases and collaborators) and routes to
// it, as real Bedrock does; with one, the named version must exist.
//
//nolint:gocritic // cfg matches the driver interface signature; copied once on entry.
func (m *Mock) CreateAgentAlias(_ context.Context, cfg driver.AgentAliasConfig) (*driver.AgentAlias, error) {
	if err := validateAgentAlias(cfg); err != nil {
		return nil, err
	}

	if err := validateTags(cfg.Tags); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.agents.Has(cfg.AgentID) {
		return nil, agentNotFound(cfg.AgentID)
	}

	if err := m.checkAliasName(cfg.AgentID, "", cfg.Name); err != nil {
		return nil, err
	}

	routing, err := m.resolveAliasRouting(cfg.AgentID, cfg.RoutingConfiguration)
	if err != nil {
		return nil, err
	}

	id, err := newID(m.aliases)
	if err != nil {
		return nil, err
	}

	now := m.now()
	alias := &driver.AgentAlias{
		ID:                   id,
		ARN:                  idgen.AWSARN("bedrock", m.opts.Region, m.opts.AccountID, "agent-alias/"+cfg.AgentID+"/"+id),
		AgentID:              cfg.AgentID,
		Name:                 cfg.Name,
		Description:          cfg.Description,
		Status:               driver.AgentAliasPrepared,
		ClientToken:          cfg.ClientToken,
		RoutingConfiguration: routing,
		HistoryEvents:        []driver.AgentAliasHistoryEvent{{RoutingConfiguration: copyRoutes(routing), StartDate: now}},
		InvocationState:      aliasAcceptInvocations,
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	m.aliases.Set(id, alias)
	m.putTags(alias.ARN, cfg.Tags)

	result := cloneAlias(alias)

	return &result, nil
}

// GetAgentAlias returns an alias of an agent, including the test alias.
func (m *Mock) GetAgentAlias(_ context.Context, agentID, aliasID string) (*driver.AgentAlias, error) {
	if err := validateAliasRef(agentID, aliasID); err != nil {
		return nil, err
	}

	agent, ok := m.agents.Get(agentID)
	if !ok {
		return nil, agentNotFound(agentID)
	}

	if aliasID == driver.AgentTestAliasID {
		alias := m.testAlias(agent)

		return &alias, nil
	}

	alias, ok := m.aliases.Get(aliasID)
	if !ok || alias.AgentID != agentID {
		return nil, aliasNotFound(aliasID)
	}

	result := cloneAlias(alias)

	return &result, nil
}

// ListAgentAliases lists one page of an agent's aliases; the test alias comes
// first, then the others by id.
func (m *Mock) ListAgentAliases(_ context.Context, agentID string, page driver.Page) ([]driver.AgentAlias, string, error) {
	agent, ok := m.agents.Get(agentID)
	if !ok {
		return nil, "", agentNotFound(agentID)
	}

	out := []driver.AgentAlias{m.testAlias(agent)}

	for _, a := range m.aliases.SortedValues() {
		if a.AgentID == agentID {
			out = append(out, cloneAlias(a))
		}
	}

	return paginate(out, page)
}

// UpdateAgentAlias renames an alias, changes its invocation state and
// re-routes it. Without a routingConfiguration it cuts a new version of DRAFT,
// as real Bedrock does. A routing change closes the previous history event.
//
//nolint:gocritic // cfg matches the driver interface signature; copied once on entry.
func (m *Mock) UpdateAgentAlias(_ context.Context, aliasID string, cfg driver.AgentAliasConfig) (*driver.AgentAlias, error) {
	if err := validateAliasUpdate(aliasID, cfg); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	alias, ok := m.aliases.Get(aliasID)
	if !m.agents.Has(cfg.AgentID) || !ok || alias.AgentID != cfg.AgentID {
		return nil, aliasNotFound(aliasID)
	}

	if err := m.checkAliasName(cfg.AgentID, aliasID, cfg.Name); err != nil {
		return nil, err
	}

	routing, err := m.resolveAliasRouting(cfg.AgentID, cfg.RoutingConfiguration)
	if err != nil {
		return nil, err
	}

	now := m.now()
	updated := cloneAlias(alias)
	updated.Name = cfg.Name
	updated.Description = cfg.Description
	updated.InvocationState = orDefault(cfg.InvocationState, alias.InvocationState)
	updated.UpdatedAt = now

	reroute(&updated, routing, now)

	m.aliases.Set(aliasID, &updated)

	result := cloneAlias(&updated)

	return &result, nil
}

// DeleteAgentAlias deletes an alias and its tags.
func (m *Mock) DeleteAgentAlias(_ context.Context, agentID, aliasID string) (string, error) {
	if err := validateAliasRef(agentID, aliasID); err != nil {
		return "", err
	}

	if aliasID == driver.AgentTestAliasID {
		return "", errors.New(errors.InvalidArgument, errTestAliasImmutableMsg)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.agents.Has(agentID) {
		return "", agentNotFound(agentID)
	}

	alias, ok := m.aliases.Get(aliasID)
	if !ok || alias.AgentID != agentID {
		return "", aliasNotFound(aliasID)
	}

	m.aliases.Delete(aliasID)
	m.dropTags(alias.ARN)

	return statusDeleting, nil
}

// GetAgentVersion returns a numbered version of an agent.
func (m *Mock) GetAgentVersion(_ context.Context, agentID, version string) (*driver.Agent, error) {
	var v violations

	v.numericVersion("agentVersion", version)

	if err := v.err(); err != nil {
		return nil, err
	}

	if !m.agents.Has(agentID) {
		return nil, agentNotFound(agentID)
	}

	stored, ok := m.agentVersions.Get(versionKey(agentID, version))
	if !ok {
		return nil, versionNotFound("agent", agentID, version)
	}

	result := cloneAgent(stored)

	return &result, nil
}

// ListAgentVersions lists one page of an agent's versions, DRAFT first.
func (m *Mock) ListAgentVersions(_ context.Context, agentID string, page driver.Page) ([]driver.Agent, string, error) {
	draft, ok := m.agents.Get(agentID)
	if !ok {
		return nil, "", agentNotFound(agentID)
	}

	out := []driver.Agent{cloneAgent(draft)}

	for key, a := range m.agentVersions.All() {
		if strings.HasPrefix(key, agentID+"/") {
			out = append(out, cloneAgent(a))
		}
	}

	sortByVersion(out, func(a driver.Agent) string { return a.Version })

	return paginate(out, page)
}

// DeleteAgentVersion deletes a numbered version and its action groups,
// knowledge-base associations and collaborators. A version an alias routes to
// is in use: deleting it needs skipResourceInUseCheck.
func (m *Mock) DeleteAgentVersion(_ context.Context, agentID, version string, skipResourceInUseCheck bool) (string, error) {
	var v violations

	v.numericVersion("agentVersion", version)

	if err := v.err(); err != nil {
		return "", err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.agents.Has(agentID) {
		return "", agentNotFound(agentID)
	}

	key := versionKey(agentID, version)
	if !m.agentVersions.Has(key) {
		return "", versionNotFound("agent", agentID, version)
	}

	if !skipResourceInUseCheck {
		if alias := m.aliasRoutingTo(agentID, version); alias != "" {
			return "", errors.Newf(errors.FailedPrecondition,
				"Version %s of agent %s is in use by alias %s. Remove it from the alias or set skipResourceInUseCheck.",
				version, agentID, alias)
		}
	}

	m.agentVersions.Delete(key)
	m.deleteAgentChildren(agentID, version)

	return statusDeleting, nil
}

// resolveAliasRouting returns the routing an alias write ends up with: the
// caller's (whose version must exist), or a freshly cut version. Callers hold
// m.mu.
func (m *Mock) resolveAliasRouting(agentID string, routes []driver.AgentAliasRoute) ([]driver.AgentAliasRoute, error) {
	if len(routes) == 0 {
		return []driver.AgentAliasRoute{{AgentVersion: m.cutAgentVersion(agentID)}}, nil
	}

	for _, r := range routes {
		if r.AgentVersion == "" || r.AgentVersion == driver.DraftVersion {
			continue
		}

		if !m.agentVersions.Has(versionKey(agentID, r.AgentVersion)) {
			return nil, versionNotFound("agent", agentID, r.AgentVersion)
		}
	}

	return copyRoutes(routes), nil
}

// cutAgentVersion snapshots an agent's DRAFT and every DRAFT child as the next
// numbered version. Callers hold m.mu, so the snapshot is consistent.
func (m *Mock) cutAgentVersion(agentID string) string {
	draft, _ := m.agents.Get(agentID)
	version := m.nextVersion(agentSeqKey(agentID))
	now := m.now()

	snap := cloneAgent(draft)
	snap.Version = version
	snap.Status = agentVersionPrepared
	snap.CreatedAt = now
	snap.UpdatedAt = now
	m.agentVersions.Set(versionKey(agentID, version), &snap)

	draftPrefix := versionKey(agentID, driver.DraftVersion) + "/"

	copyChildren(m.actionGroups, draftPrefix, func(g *driver.ActionGroup) (string, *driver.ActionGroup) {
		c := cloneActionGroup(g)
		c.AgentVersion = version

		return versionKey(agentID, version, c.ID), &c
	})
	copyChildren(m.agentKBs, draftPrefix, func(kb *driver.AgentKnowledgeBase) (string, *driver.AgentKnowledgeBase) {
		c := *kb
		c.AgentVersion = version

		return versionKey(agentID, version, c.KnowledgeBaseID), &c
	})
	copyChildren(m.collaborators, draftPrefix, func(col *driver.AgentCollaborator) (string, *driver.AgentCollaborator) {
		c := *col
		c.AgentVersion = version

		return versionKey(agentID, version, c.ID), &c
	})

	return version
}

// copyChildren stores a converted copy of every value whose key starts with
// prefix.
func copyChildren[V any](store *memstore.Store[V], prefix string, conv func(V) (string, V)) {
	for key, v := range store.All() {
		if strings.HasPrefix(key, prefix) {
			k, c := conv(v)
			store.Set(k, c)
		}
	}
}

// deleteAgentChildren removes the action groups, knowledge-base associations
// and collaborators of one agent version, or of every version when version is
// empty. Callers hold m.mu.
func (m *Mock) deleteAgentChildren(agentID, version string) {
	prefix := agentID + "/"
	if version != "" {
		prefix = versionKey(agentID, version) + "/"
	}

	deleteByPrefix(m.actionGroups, prefix)
	deleteByPrefix(m.agentKBs, prefix)
	deleteByPrefix(m.collaborators, prefix)
}

// deleteDraftChild removes one DRAFT child of an agent (an action group,
// knowledge-base association or collaborator) and marks the DRAFT changed.
// Versions are immutable, so version must be DRAFT.
func deleteDraftChild[V any](m *Mock, store *memstore.Store[V], agentID, version, childField, childID string,
	notFound func(string) error,
) error {
	var v violations

	v.id("agentId", agentID)
	v.draft(version)
	v.id(childField, childID)

	if err := v.err(); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.agents.Has(agentID) {
		return agentNotFound(agentID)
	}

	key := versionKey(agentID, version, childID)
	if !store.Has(key) {
		return notFound(childID)
	}

	store.Delete(key)
	m.markDraftChanged(agentID)

	return nil
}

// deleteByPrefix removes every entry whose key starts with prefix.
func deleteByPrefix[V any](store *memstore.Store[V], prefix string) {
	for key := range store.All() {
		if strings.HasPrefix(key, prefix) {
			store.Delete(key)
		}
	}
}

// aliasRoutingTo returns the id of an alias of agentID that routes to version,
// or "".
func (m *Mock) aliasRoutingTo(agentID, version string) string {
	for _, a := range m.aliases.SortedValues() {
		if a.AgentID != agentID {
			continue
		}

		for _, r := range a.RoutingConfiguration {
			if r.AgentVersion == version {
				return a.ID
			}
		}
	}

	return ""
}

// checkAliasName rejects a name another alias of the agent already uses.
func (m *Mock) checkAliasName(agentID, selfID, name string) error {
	for _, a := range m.aliases.All() {
		if a.AgentID == agentID && a.ID != selfID && a.Name == name {
			return errors.Newf(errors.AlreadyExists, "An alias named %s already exists for agent %s.", name, agentID)
		}
	}

	if name == driver.AgentTestAliasName {
		return errors.Newf(errors.AlreadyExists, "An alias named %s already exists for agent %s.", name, agentID)
	}

	return nil
}

// testAlias is the TSTALIASID alias every agent has, routed to DRAFT.
func (m *Mock) testAlias(agent *driver.Agent) driver.AgentAlias {
	route := []driver.AgentAliasRoute{{AgentVersion: driver.DraftVersion}}

	return driver.AgentAlias{
		ID:   driver.AgentTestAliasID,
		ARN:  idgen.AWSARN("bedrock", m.opts.Region, m.opts.AccountID, "agent-alias/"+agent.ID+"/"+driver.AgentTestAliasID),
		Name: driver.AgentTestAliasName, AgentID: agent.ID, Description: testAliasDescription,
		Status:               driver.AgentAliasPrepared,
		RoutingConfiguration: route,
		HistoryEvents:        []driver.AgentAliasHistoryEvent{{RoutingConfiguration: copyRoutes(route), StartDate: agent.CreatedAt}},
		InvocationState:      aliasAcceptInvocations,
		CreatedAt:            agent.CreatedAt,
		UpdatedAt:            agent.CreatedAt,
	}
}

// validateAgentAlias checks the members CreateAgentAlias and UpdateAgentAlias
// share.
//
//nolint:gocritic // cfg matches the driver interface signature.
func validateAgentAlias(cfg driver.AgentAliasConfig) error {
	var v violations

	v.id("agentId", cfg.AgentID)
	v.name("agentAliasName", cfg.Name)
	v.description(cfg.Description)
	v.clientToken(cfg.ClientToken)

	if len(cfg.RoutingConfiguration) > maxAgentAliasRoutes {
		v.addf("Value at 'routingConfiguration' failed to satisfy constraint: "+
			"Member must have length less than or equal to %d", maxAgentAliasRoutes)
	}

	for _, r := range cfg.RoutingConfiguration {
		v.pattern("routingConfiguration.member.agentVersion", r.AgentVersion, versionRE, versionPattern)
	}

	return v.err()
}

// reroute points an alias at routing, closing the open history event and
// recording a new one when the routing changed.
func reroute(alias *driver.AgentAlias, routing []driver.AgentAliasRoute, now string) {
	if slices.Equal(routing, alias.RoutingConfiguration) {
		return
	}

	if len(alias.HistoryEvents) > 0 && alias.HistoryEvents[0].EndDate == "" {
		alias.HistoryEvents[0].EndDate = now
	}

	event := driver.AgentAliasHistoryEvent{RoutingConfiguration: copyRoutes(routing), StartDate: now}
	alias.HistoryEvents = append([]driver.AgentAliasHistoryEvent{event}, alias.HistoryEvents...)
	alias.RoutingConfiguration = routing
}

// validateAliasUpdate checks UpdateAgentAlias's members and refuses the test
// alias.
//
//nolint:gocritic // cfg matches the driver interface signature.
func validateAliasUpdate(aliasID string, cfg driver.AgentAliasConfig) error {
	if err := validateAgentAlias(cfg); err != nil {
		return err
	}

	var v violations

	v.enum("aliasInvocationState", cfg.InvocationState, aliasAcceptInvocations, aliasRejectInvocations)
	v.pattern("agentAliasId", aliasID, regexpAgentAliasID, agentAliasIDPattern)

	if err := v.err(); err != nil {
		return err
	}

	if aliasID == driver.AgentTestAliasID {
		return errors.New(errors.InvalidArgument, errTestAliasImmutableMsg)
	}

	return nil
}

// validateAliasRef checks the agentId and agentAliasId path members.
func validateAliasRef(agentID, aliasID string) error {
	var v violations

	v.id("agentId", agentID)
	v.required("agentAliasId", aliasID == "")
	v.pattern("agentAliasId", aliasID, regexpAgentAliasID, agentAliasIDPattern)

	return v.err()
}

// cloneAlias returns a value copy whose slices do not alias the stored alias.
func cloneAlias(a *driver.AgentAlias) driver.AgentAlias {
	out := *a
	out.RoutingConfiguration = copyRoutes(a.RoutingConfiguration)
	out.HistoryEvents = make([]driver.AgentAliasHistoryEvent, len(a.HistoryEvents))

	for i, e := range a.HistoryEvents {
		e.RoutingConfiguration = copyRoutes(e.RoutingConfiguration)
		out.HistoryEvents[i] = e
	}

	return out
}

func copyRoutes(r []driver.AgentAliasRoute) []driver.AgentAliasRoute {
	if r == nil {
		return nil
	}

	return append([]driver.AgentAliasRoute(nil), r...)
}

func agentSeqKey(agentID string) string { return "agent/" + agentID }

func aliasNotFound(aliasID string) error {
	return errors.Newf(errors.NotFound, "agent alias %q not found", aliasID)
}

func versionNotFound(kind, id, version string) error {
	return errors.Newf(errors.NotFound, "version %s of %s %q not found", version, kind, id)
}
