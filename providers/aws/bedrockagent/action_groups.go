package bedrockagent

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/bedrockagent/driver"
)

// actionGroupSignatures is the ActionGroupSignature enum from the service
// model.
//
//nolint:gochecknoglobals // fixed enum table from the service model.
var actionGroupSignatures = []string{
	"AMAZON.UserInput", "AMAZON.CodeInterpreter", "ANTHROPIC.Computer", "ANTHROPIC.Bash", "ANTHROPIC.TextEditor",
}

// CreateAgentActionGroup adds an action group to an agent's DRAFT, ENABLED
// unless the caller says otherwise. Names are unique per agent version.
//
//nolint:gocritic // cfg matches the driver interface signature; copied once on entry.
func (m *Mock) CreateAgentActionGroup(_ context.Context, cfg driver.ActionGroupConfig) (*driver.ActionGroup, error) {
	if err := validateActionGroup(cfg); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.agents.Has(cfg.AgentID) {
		return nil, agentNotFound(cfg.AgentID)
	}

	if err := m.checkActionGroupName(cfg.AgentID, "", cfg.Name); err != nil {
		return nil, err
	}

	prefix := versionKey(cfg.AgentID, driver.DraftVersion)

	id, err := uniqueID(func(id string) bool { return m.actionGroups.Has(versionKey(prefix, id)) })
	if err != nil {
		return nil, err
	}

	now := m.now()
	group := &driver.ActionGroup{
		ID:                               id,
		AgentID:                          cfg.AgentID,
		AgentVersion:                     driver.DraftVersion,
		Name:                             cfg.Name,
		Description:                      cfg.Description,
		ClientToken:                      cfg.ClientToken,
		ParentActionGroupSignature:       cfg.ParentActionGroupSignature,
		ParentActionGroupSignatureParams: maps.Clone(cfg.ParentActionGroupSignatureParams),
		State:                            orDefault(cfg.State, driver.ActionGroupEnabled),
		Executor:                         copyRaw(cfg.Executor),
		APISchema:                        copyRaw(cfg.APISchema),
		FunctionSchema:                   copyRaw(cfg.FunctionSchema),
		CreatedAt:                        now,
		UpdatedAt:                        now,
	}
	m.actionGroups.Set(versionKey(prefix, id), group)
	m.markDraftChanged(cfg.AgentID)

	result := cloneActionGroup(group)

	return &result, nil
}

// GetAgentActionGroup returns an action group of any agent version.
func (m *Mock) GetAgentActionGroup(_ context.Context, agentID, version, actionGroupID string) (*driver.ActionGroup, error) {
	if err := m.checkAgentChild(agentID, version, "actionGroupId", actionGroupID); err != nil {
		return nil, err
	}

	group, ok := m.actionGroups.Get(versionKey(agentID, version, actionGroupID))
	if !ok {
		return nil, actionGroupNotFound(actionGroupID)
	}

	result := cloneActionGroup(group)

	return &result, nil
}

// UpdateAgentActionGroup replaces an action group of the DRAFT. Members the
// caller omits are cleared, except state, which keeps its value.
//
//nolint:gocritic // cfg matches the driver interface signature; copied once on entry.
func (m *Mock) UpdateAgentActionGroup(_ context.Context, actionGroupID string, cfg driver.ActionGroupConfig) (*driver.ActionGroup, error) {
	if err := validateActionGroup(cfg); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.agents.Has(cfg.AgentID) {
		return nil, agentNotFound(cfg.AgentID)
	}

	key := versionKey(cfg.AgentID, driver.DraftVersion, actionGroupID)

	group, ok := m.actionGroups.Get(key)
	if !ok {
		return nil, actionGroupNotFound(actionGroupID)
	}

	if err := m.checkActionGroupName(cfg.AgentID, actionGroupID, cfg.Name); err != nil {
		return nil, err
	}

	updated := cloneActionGroup(group)
	updated.Name = cfg.Name
	updated.Description = cfg.Description
	updated.ParentActionGroupSignature = cfg.ParentActionGroupSignature
	updated.ParentActionGroupSignatureParams = maps.Clone(cfg.ParentActionGroupSignatureParams)
	updated.State = orDefault(cfg.State, group.State)
	updated.Executor = copyRaw(cfg.Executor)
	updated.APISchema = copyRaw(cfg.APISchema)
	updated.FunctionSchema = copyRaw(cfg.FunctionSchema)
	updated.UpdatedAt = m.now()
	m.actionGroups.Set(key, &updated)
	m.markDraftChanged(cfg.AgentID)

	result := cloneActionGroup(&updated)

	return &result, nil
}

// DeleteAgentActionGroup removes an action group from the DRAFT.
func (m *Mock) DeleteAgentActionGroup(_ context.Context, agentID, version, actionGroupID string) error {
	return deleteDraftChild(m, m.actionGroups, agentID, version, "actionGroupId", actionGroupID, actionGroupNotFound)
}

// ListAgentActionGroups lists one page of an agent version's action groups.
func (m *Mock) ListAgentActionGroups(_ context.Context, agentID, version string, page driver.Page) ([]driver.ActionGroup, string, error) {
	if err := m.checkAgentVersion(agentID, version); err != nil {
		return nil, "", err
	}

	return paginate(childrenOf(m.actionGroups, versionKey(agentID, version), cloneActionGroup), page)
}

// checkAgentVersion validates a read's version member and resolves the agent
// and, for a numbered version, the version.
func (m *Mock) checkAgentVersion(agentID, version string) error {
	var v violations

	v.id("agentId", agentID)
	v.version("agentVersion", version)

	if err := v.err(); err != nil {
		return err
	}

	if !m.agents.Has(agentID) {
		return agentNotFound(agentID)
	}

	if version != driver.DraftVersion && !m.agentVersions.Has(versionKey(agentID, version)) {
		return versionNotFound("agent", agentID, version)
	}

	return nil
}

// checkAgentChild validates a child id member, then resolves the agent version
// as checkAgentVersion does.
func (m *Mock) checkAgentChild(agentID, version, field, childID string) error {
	var v violations

	v.id(field, childID)

	if err := v.err(); err != nil {
		return err
	}

	return m.checkAgentVersion(agentID, version)
}

// checkActionGroupName rejects a name another DRAFT action group of the agent
// already uses. Callers hold m.mu.
func (m *Mock) checkActionGroupName(agentID, selfID, name string) error {
	prefix := versionKey(agentID, driver.DraftVersion) + "/"

	for key, g := range m.actionGroups.All() {
		if strings.HasPrefix(key, prefix) && g.ID != selfID && g.Name == name {
			return errors.Newf(errors.AlreadyExists,
				"An action group named %s already exists for agent %s.", name, agentID)
		}
	}

	return nil
}

// childrenOf returns copies of the values stored under "<parent>/", sorted by
// key.
func childrenOf[V, T any](store interface{ All() map[string]V }, parent string, clone func(V) T) []T {
	all := store.All()
	keys := make([]string, 0, len(all))

	for k := range all {
		if strings.HasPrefix(k, parent+"/") {
			keys = append(keys, k)
		}
	}

	slices.Sort(keys)

	out := make([]T, 0, len(keys))
	for _, k := range keys {
		out = append(out, clone(all[k]))
	}

	return out
}

// validateActionGroup checks the members Create- and UpdateAgentActionGroup
// share.
//
//nolint:gocritic // cfg matches the driver interface signature.
func validateActionGroup(cfg driver.ActionGroupConfig) error {
	var v violations

	v.id("agentId", cfg.AgentID)
	v.draft(cfg.AgentVersion)
	v.name("actionGroupName", cfg.Name)
	v.description(cfg.Description)
	v.clientToken(cfg.ClientToken)
	v.enum("parentActionGroupSignature", cfg.ParentActionGroupSignature, actionGroupSignatures...)
	v.enum("actionGroupState", cfg.State, driver.ActionGroupEnabled, driver.ActionGroupDisabled)
	v.actionGroupSchemas(cfg.APISchema, cfg.FunctionSchema)

	return v.err()
}

// actionGroupSchemas checks that an action group defines its actions one way
// (apiSchema or functionSchema, not both) and that an inline apiSchema
// payload is an OpenAPI document in JSON or YAML.
func (v *violations) actionGroupSchemas(apiSchema, functionSchema json.RawMessage) {
	hasAPI, hasFunctions := present(apiSchema), present(functionSchema)
	if hasAPI && hasFunctions {
		v.addf("An action group takes either apiSchema or functionSchema, not both")
	}

	if !hasAPI {
		return
	}

	var schema struct {
		Payload *string `json:"payload"`
	}

	if json.Unmarshal(apiSchema, &schema) != nil || schema.Payload == nil {
		return
	}

	var doc map[string]any
	if json.Unmarshal([]byte(*schema.Payload), &doc) != nil && yaml.Unmarshal([]byte(*schema.Payload), &doc) != nil || len(doc) == 0 {
		v.addf("apiSchema.payload must be an OpenAPI document in JSON or YAML")
	}
}

// present reports whether a JSON member was sent with a value.
func present(raw json.RawMessage) bool {
	return len(raw) > 0 && string(raw) != "null"
}

// cloneActionGroup returns a value copy that shares no map or RawMessage with
// the stored action group.
func cloneActionGroup(g *driver.ActionGroup) driver.ActionGroup {
	out := *g
	out.ParentActionGroupSignatureParams = maps.Clone(g.ParentActionGroupSignatureParams)
	out.Executor = copyRaw(g.Executor)
	out.APISchema = copyRaw(g.APISchema)
	out.FunctionSchema = copyRaw(g.FunctionSchema)

	return out
}

func actionGroupNotFound(id string) error {
	return errors.Newf(errors.NotFound, "action group %q not found", id)
}
