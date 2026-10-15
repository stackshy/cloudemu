package bedrockagent

import (
	"encoding/json"
	"net/http"

	badriver "github.com/stackshy/cloudemu/v2/services/bedrockagent/driver"
)

// Segment counts of the /agents/{id}/agentaliases|agentversions subtrees.
const (
	agentChildCollectionSegs = 2 // {agent}/agentaliases
	agentChildItemSegs       = 3 // {agent}/agentaliases/{alias}
	versionChildCollSegs     = 4 // {agent}/agentversions/{v}/actiongroups
	versionChildItemSegs     = 5 // {agent}/agentversions/{v}/actiongroups/{id}
)

type agentAliasRouteJSON struct {
	AgentVersion          string `json:"agentVersion,omitempty"`
	ProvisionedThroughput string `json:"provisionedThroughput,omitempty"`
}

type agentAliasHistoryEventJSON struct {
	RoutingConfiguration []agentAliasRouteJSON `json:"routingConfiguration"`
	StartDate            string                `json:"startDate,omitempty"`
	EndDate              string                `json:"endDate,omitempty"`
}

type agentAliasRequest struct {
	AgentAliasName       string                `json:"agentAliasName"`
	Description          string                `json:"description"`
	ClientToken          string                `json:"clientToken"`
	RoutingConfiguration []agentAliasRouteJSON `json:"routingConfiguration"`
	AliasInvocationState string                `json:"aliasInvocationState"`
	Tags                 map[string]string     `json:"tags"`
}

type agentAliasJSON struct {
	AgentAliasID            string                       `json:"agentAliasId"`
	AgentAliasARN           string                       `json:"agentAliasArn"`
	AgentAliasName          string                       `json:"agentAliasName"`
	AgentID                 string                       `json:"agentId"`
	AgentAliasStatus        string                       `json:"agentAliasStatus"`
	Description             string                       `json:"description,omitempty"`
	ClientToken             string                       `json:"clientToken,omitempty"`
	RoutingConfiguration    []agentAliasRouteJSON        `json:"routingConfiguration"`
	AgentAliasHistoryEvents []agentAliasHistoryEventJSON `json:"agentAliasHistoryEvents"`
	AliasInvocationState    string                       `json:"aliasInvocationState,omitempty"`
	CreatedAt               string                       `json:"createdAt"`
	UpdatedAt               string                       `json:"updatedAt"`
}

type agentAliasSummaryJSON struct {
	AgentAliasID         string                `json:"agentAliasId"`
	AgentAliasName       string                `json:"agentAliasName"`
	AgentAliasStatus     string                `json:"agentAliasStatus"`
	Description          string                `json:"description,omitempty"`
	RoutingConfiguration []agentAliasRouteJSON `json:"routingConfiguration"`
	AliasInvocationState string                `json:"aliasInvocationState,omitempty"`
	CreatedAt            string                `json:"createdAt"`
	UpdatedAt            string                `json:"updatedAt"`
}

type agentAliasEnvelope struct {
	AgentAlias agentAliasJSON `json:"agentAlias"`
}

type listAgentAliasesResponse struct {
	AgentAliasSummaries []agentAliasSummaryJSON `json:"agentAliasSummaries"`
	NextToken           string                  `json:"nextToken,omitempty"`
}

type deleteAgentAliasResponse struct {
	AgentID          string `json:"agentId"`
	AgentAliasID     string `json:"agentAliasId"`
	AgentAliasStatus string `json:"agentAliasStatus"`
}

type agentVersionJSON struct {
	AgentID                     string          `json:"agentId"`
	AgentName                   string          `json:"agentName"`
	AgentARN                    string          `json:"agentArn"`
	Version                     string          `json:"version"`
	Instruction                 string          `json:"instruction,omitempty"`
	AgentStatus                 string          `json:"agentStatus"`
	FoundationModel             string          `json:"foundationModel,omitempty"`
	Description                 string          `json:"description,omitempty"`
	IdleSessionTTLInSeconds     int32           `json:"idleSessionTTLInSeconds"`
	AgentResourceRoleArn        string          `json:"agentResourceRoleArn"`
	CustomerEncryptionKeyArn    string          `json:"customerEncryptionKeyArn,omitempty"`
	AgentCollaboration          string          `json:"agentCollaboration,omitempty"`
	PromptOverrideConfiguration json.RawMessage `json:"promptOverrideConfiguration"`
	GuardrailConfiguration      json.RawMessage `json:"guardrailConfiguration,omitempty"`
	MemoryConfiguration         json.RawMessage `json:"memoryConfiguration,omitempty"`
	CreatedAt                   string          `json:"createdAt"`
	UpdatedAt                   string          `json:"updatedAt"`
}

type agentVersionSummaryJSON struct {
	AgentName              string          `json:"agentName"`
	AgentStatus            string          `json:"agentStatus"`
	AgentVersion           string          `json:"agentVersion"`
	Description            string          `json:"description,omitempty"`
	GuardrailConfiguration json.RawMessage `json:"guardrailConfiguration,omitempty"`
	CreatedAt              string          `json:"createdAt"`
	UpdatedAt              string          `json:"updatedAt"`
}

type agentVersionEnvelope struct {
	AgentVersion agentVersionJSON `json:"agentVersion"`
}

type listAgentVersionsResponse struct {
	AgentVersionSummaries []agentVersionSummaryJSON `json:"agentVersionSummaries"`
	NextToken             string                    `json:"nextToken,omitempty"`
}

type deleteAgentVersionResponse struct {
	AgentID      string `json:"agentId"`
	AgentVersion string `json:"agentVersion"`
	AgentStatus  string `json:"agentStatus"`
}

// serveAgentAliases dispatches /agents/{id}/agentaliases/[{alias}/].
func (h *Handler) serveAgentAliases(w http.ResponseWriter, r *http.Request, segs []string) {
	agentID := segs[0]

	switch len(segs) {
	case agentChildCollectionSegs:
		switch r.Method {
		case http.MethodPut:
			h.createAgentAlias(w, r, agentID)
		case http.MethodPost:
			withCapability(h, w, "ListAgentAliases", func(c badriver.AgentVersions) { listAgentAliases(w, r, c, agentID) })
		default:
			methodNotAllowed(w)
		}
	case agentChildItemSegs:
		withCapability(h, w, "AgentAlias", func(c badriver.AgentVersions) { serveAgentAlias(w, r, c, agentID, segs[2]) })
	default:
		notFound(w, r.URL.Path)
	}
}

// serveAgentVersions dispatches /agents/{id}/agentversions/... including the
// action-group, knowledge-base and collaborator children of a version.
func (h *Handler) serveAgentVersions(w http.ResponseWriter, r *http.Request, segs []string) {
	agentID := segs[0]

	switch {
	case len(segs) == agentChildCollectionSegs && r.Method == http.MethodPost:
		withCapability(h, w, "ListAgentVersions", func(c badriver.AgentVersions) { listAgentVersions(w, r, c, agentID) })
	case len(segs) == agentChildItemSegs:
		withCapability(h, w, "AgentVersion", func(c badriver.AgentVersions) { serveAgentVersion(w, r, c, agentID, segs[2]) })
	case len(segs) == versionChildCollSegs || len(segs) == versionChildItemSegs:
		h.serveVersionChild(w, r, segs)
	case len(segs) == agentChildCollectionSegs:
		methodNotAllowed(w)
	default:
		notFound(w, r.URL.Path)
	}
}

// serveVersionChild routes the action groups, knowledge bases and
// collaborators of an agent version.
func (h *Handler) serveVersionChild(w http.ResponseWriter, r *http.Request, segs []string) {
	ref := childRef{agentID: segs[0], version: segs[2]}
	if len(segs) == versionChildItemSegs {
		ref.childID = segs[4]
	}

	switch segs[3] {
	case segActionGroups:
		withCapability(h, w, "AgentActionGroup", func(c badriver.ActionGroups) { serveActionGroups(w, r, c, ref) })
	case segAgentKBs:
		withCapability(h, w, "AgentKnowledgeBase", func(c badriver.AgentKnowledgeBases) { serveAgentKBs(w, r, c, ref) })
	case segAgentCollaborators:
		withCapability(h, w, "AgentCollaborator", func(c badriver.AgentCollaborators) { serveCollaborators(w, r, c, ref) })
	default:
		notFound(w, r.URL.Path)
	}
}

// childRef addresses an agent-version child; childID is empty on the
// collection.
type childRef struct {
	agentID, version, childID string
}

// childOps are the operations of an agent-version child collection: create
// (PUT) and list (POST) on the collection, get (GET), update (PUT) and delete
// (DELETE) on an item.
type childOps struct {
	create, list, get, update, remove func()
}

// routeChild picks the operation for the request, or answers 405.
func routeChild(w http.ResponseWriter, r *http.Request, ref childRef, ops childOps) {
	type route struct {
		item   bool
		method string
	}

	op := map[route]func(){
		{false, http.MethodPut}:   ops.create,
		{false, http.MethodPost}:  ops.list,
		{true, http.MethodGet}:    ops.get,
		{true, http.MethodPut}:    ops.update,
		{true, http.MethodDelete}: ops.remove,
	}[route{ref.childID != "", r.Method}]

	if op == nil {
		methodNotAllowed(w)

		return
	}

	op()
}

// writeDeleted answers a 204 delete, or its error.
func writeDeleted(w http.ResponseWriter, err error) {
	if err != nil {
		writeErr(w, err)

		return
	}

	writeNoContent(w)
}

// withCapability runs fn with the driver's capability T, or answers that the
// backend does not support op.
func withCapability[T any](h *Handler, w http.ResponseWriter, op string, fn func(T)) {
	if c, ok := capabilityOf[T](h.agent, w, op); ok {
		fn(c)
	}
}

func (h *Handler) createAgentAlias(w http.ResponseWriter, r *http.Request, agentID string) {
	var in agentAliasRequest
	if !decodeJSON(w, r, &in) {
		return
	}

	alias, err := h.agent.CreateAgentAlias(r.Context(), in.config(agentID))
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSONStatus(w, http.StatusAccepted, agentAliasEnvelope{AgentAlias: toAgentAliasJSON(alias)})
}

func listAgentAliases(w http.ResponseWriter, r *http.Request, c badriver.AgentVersions, agentID string) {
	page, ok := decodeListBody(w, r)
	if !ok {
		return
	}

	aliases, next, err := c.ListAgentAliases(r.Context(), agentID, page)
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]agentAliasSummaryJSON, 0, len(aliases))

	for i := range aliases {
		a := toAgentAliasJSON(&aliases[i])
		out = append(out, agentAliasSummaryJSON{
			AgentAliasID: a.AgentAliasID, AgentAliasName: a.AgentAliasName, AgentAliasStatus: a.AgentAliasStatus,
			Description: a.Description, RoutingConfiguration: a.RoutingConfiguration,
			AliasInvocationState: a.AliasInvocationState, CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt,
		})
	}

	writeJSON(w, listAgentAliasesResponse{AgentAliasSummaries: out, NextToken: next})
}

func serveAgentAlias(w http.ResponseWriter, r *http.Request, c badriver.AgentVersions, agentID, aliasID string) {
	switch r.Method {
	case http.MethodGet:
		alias, err := c.GetAgentAlias(r.Context(), agentID, aliasID)
		if err != nil {
			writeErr(w, err)

			return
		}

		writeJSON(w, agentAliasEnvelope{AgentAlias: toAgentAliasJSON(alias)})
	case http.MethodPut:
		var in agentAliasRequest
		if !decodeJSON(w, r, &in) {
			return
		}

		alias, err := c.UpdateAgentAlias(r.Context(), aliasID, in.config(agentID))
		if err != nil {
			writeErr(w, err)

			return
		}

		writeJSONStatus(w, http.StatusAccepted, agentAliasEnvelope{AgentAlias: toAgentAliasJSON(alias)})
	case http.MethodDelete:
		status, err := c.DeleteAgentAlias(r.Context(), agentID, aliasID)
		if err != nil {
			writeErr(w, err)

			return
		}

		writeJSONStatus(w, http.StatusAccepted,
			deleteAgentAliasResponse{AgentID: agentID, AgentAliasID: aliasID, AgentAliasStatus: status})
	default:
		methodNotAllowed(w)
	}
}

func listAgentVersions(w http.ResponseWriter, r *http.Request, c badriver.AgentVersions, agentID string) {
	page, ok := decodeListBody(w, r)
	if !ok {
		return
	}

	versions, next, err := c.ListAgentVersions(r.Context(), agentID, page)
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]agentVersionSummaryJSON, 0, len(versions))

	for i := range versions {
		v := &versions[i]
		out = append(out, agentVersionSummaryJSON{
			AgentName: v.Name, AgentStatus: v.Status, AgentVersion: v.Version, Description: v.Description,
			GuardrailConfiguration: v.GuardrailConfiguration, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt,
		})
	}

	writeJSON(w, listAgentVersionsResponse{AgentVersionSummaries: out, NextToken: next})
}

func serveAgentVersion(w http.ResponseWriter, r *http.Request, c badriver.AgentVersions, agentID, version string) {
	switch r.Method {
	case http.MethodGet:
		v, err := c.GetAgentVersion(r.Context(), agentID, version)
		if err != nil {
			writeErr(w, err)

			return
		}

		writeJSON(w, agentVersionEnvelope{AgentVersion: toAgentVersionJSON(v)})
	case http.MethodDelete:
		status, err := c.DeleteAgentVersion(r.Context(), agentID, version, queryBool(r, "skipResourceInUseCheck"))
		if err != nil {
			writeErr(w, err)

			return
		}

		writeJSONStatus(w, http.StatusAccepted,
			deleteAgentVersionResponse{AgentID: agentID, AgentVersion: version, AgentStatus: status})
	default:
		methodNotAllowed(w)
	}
}

// config maps an alias body onto the driver's AgentAliasConfig.
func (in *agentAliasRequest) config(agentID string) badriver.AgentAliasConfig {
	routes := make([]badriver.AgentAliasRoute, 0, len(in.RoutingConfiguration))

	for _, rt := range in.RoutingConfiguration {
		routes = append(routes, badriver.AgentAliasRoute{
			AgentVersion: rt.AgentVersion, ProvisionedThroughput: rt.ProvisionedThroughput,
		})
	}

	return badriver.AgentAliasConfig{
		AgentID:              agentID,
		Name:                 in.AgentAliasName,
		Description:          in.Description,
		ClientToken:          in.ClientToken,
		RoutingConfiguration: routes,
		InvocationState:      in.AliasInvocationState,
		Tags:                 in.Tags,
	}
}

func toAgentAliasJSON(a *badriver.AgentAlias) agentAliasJSON {
	history := make([]agentAliasHistoryEventJSON, 0, len(a.HistoryEvents))
	for _, e := range a.HistoryEvents {
		history = append(history, agentAliasHistoryEventJSON{
			RoutingConfiguration: toRoutesJSON(e.RoutingConfiguration), StartDate: e.StartDate, EndDate: e.EndDate,
		})
	}

	return agentAliasJSON{
		AgentAliasID:            a.ID,
		AgentAliasARN:           a.ARN,
		AgentAliasName:          a.Name,
		AgentID:                 a.AgentID,
		AgentAliasStatus:        a.Status,
		Description:             a.Description,
		ClientToken:             a.ClientToken,
		RoutingConfiguration:    toRoutesJSON(a.RoutingConfiguration),
		AgentAliasHistoryEvents: history,
		AliasInvocationState:    a.InvocationState,
		CreatedAt:               a.CreatedAt,
		UpdatedAt:               a.UpdatedAt,
	}
}

func toRoutesJSON(routes []badriver.AgentAliasRoute) []agentAliasRouteJSON {
	out := make([]agentAliasRouteJSON, 0, len(routes))
	for _, rt := range routes {
		out = append(out, agentAliasRouteJSON{AgentVersion: rt.AgentVersion, ProvisionedThroughput: rt.ProvisionedThroughput})
	}

	return out
}

func toAgentVersionJSON(v *badriver.Agent) agentVersionJSON {
	a := toAgentJSON(v)

	return agentVersionJSON{
		AgentID: a.AgentID, AgentName: a.AgentName, AgentARN: a.AgentARN, Version: v.Version,
		Instruction: a.Instruction, AgentStatus: a.AgentStatus, FoundationModel: a.FoundationModel,
		Description: a.Description, IdleSessionTTLInSeconds: a.IdleSessionTTLInSeconds,
		AgentResourceRoleArn: a.AgentResourceRoleArn, CustomerEncryptionKeyArn: a.CustomerEncryptionKeyArn,
		AgentCollaboration: a.AgentCollaboration, PromptOverrideConfiguration: a.PromptOverrideConfiguration,
		GuardrailConfiguration: a.GuardrailConfiguration, MemoryConfiguration: a.MemoryConfiguration,
		CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt,
	}
}
