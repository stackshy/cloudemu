package bedrockagent

import (
	"net/http"
	"strings"

	badriver "github.com/stackshy/cloudemu/v2/services/bedrockagent/driver"
)

// Segment counts of /flows/{id}/versions|aliases[/{child}].
const (
	flowChildCollectionSegs = 2
	flowChildItemSegs       = 3
)

type createFlowVersionRequest struct {
	Description string `json:"description"`
	ClientToken string `json:"clientToken"`
}

type flowVersionSummaryJSON struct {
	ID        string `json:"id"`
	Arn       string `json:"arn"`
	Status    string `json:"status"`
	Version   string `json:"version"`
	CreatedAt string `json:"createdAt"`
}

type listFlowVersionsResponse struct {
	FlowVersionSummaries []flowVersionSummaryJSON `json:"flowVersionSummaries"`
	NextToken            string                   `json:"nextToken,omitempty"`
}

type deleteFlowVersionResponse struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

type flowAliasRouteJSON struct {
	FlowVersion string `json:"flowVersion,omitempty"`
}

type flowConcurrencyJSON struct {
	Type           string `json:"type"`
	MaxConcurrency *int32 `json:"maxConcurrency,omitempty"`
}

type flowAliasRequest struct {
	Name                     string               `json:"name"`
	Description              string               `json:"description"`
	ClientToken              string               `json:"clientToken"`
	RoutingConfiguration     []flowAliasRouteJSON `json:"routingConfiguration"`
	ConcurrencyConfiguration *flowConcurrencyJSON `json:"concurrencyConfiguration"`
	Tags                     map[string]string    `json:"tags"`
}

type flowAliasJSON struct {
	ID                       string               `json:"id"`
	Arn                      string               `json:"arn"`
	FlowID                   string               `json:"flowId"`
	Name                     string               `json:"name"`
	Description              string               `json:"description,omitempty"`
	RoutingConfiguration     []flowAliasRouteJSON `json:"routingConfiguration"`
	ConcurrencyConfiguration *flowConcurrencyJSON `json:"concurrencyConfiguration,omitempty"`
	CreatedAt                string               `json:"createdAt"`
	UpdatedAt                string               `json:"updatedAt"`
}

type listFlowAliasesResponse struct {
	FlowAliasSummaries []flowAliasJSON `json:"flowAliasSummaries"`
	NextToken          string          `json:"nextToken,omitempty"`
}

type deleteFlowAliasResponse struct {
	FlowID string `json:"flowId"`
	ID     string `json:"id"`
}

// serveFlowChildren routes /flows/{id}/versions[/{v}/] and
// /flows/{id}/aliases[/{alias}].
func serveFlowChildren(w http.ResponseWriter, r *http.Request, c badriver.FlowVersions, segs []string) {
	flowID := segs[0]

	switch {
	case len(segs) == flowChildCollectionSegs && segs[1] == segVersions:
		serveFlowVersionCollection(w, r, c, flowID)
	case len(segs) == flowChildItemSegs && segs[1] == segVersions:
		serveFlowVersion(w, r, c, flowID, segs[2])
	case len(segs) == flowChildCollectionSegs:
		serveFlowAliasCollection(w, r, c, flowID)
	case len(segs) == flowChildItemSegs:
		serveFlowAlias(w, r, c, flowID, segs[2])
	default:
		notFound(w, r.URL.Path)
	}
}

func serveFlowVersionCollection(w http.ResponseWriter, r *http.Request, c badriver.FlowVersions, flowID string) {
	switch r.Method {
	case http.MethodPost:
		var in createFlowVersionRequest
		if !decodeBody(w, r, &in) {
			return
		}

		flow, err := c.CreateFlowVersion(r.Context(), flowID, in.Description)
		if err != nil {
			writeErr(w, err)

			return
		}

		writeJSONStatus(w, http.StatusCreated, toFlowJSON(flow))
	case http.MethodGet:
		page, ok := queryPage(w, r)
		if !ok {
			return
		}

		versions, next, err := c.ListFlowVersions(r.Context(), flowID, page)
		if err != nil {
			writeErr(w, err)

			return
		}

		out := make([]flowVersionSummaryJSON, 0, len(versions))

		for i := range versions {
			v := &versions[i]
			out = append(out, flowVersionSummaryJSON{ID: v.ID, Arn: v.ARN, Status: v.Status, Version: v.Version, CreatedAt: v.CreatedAt})
		}

		writeJSON(w, listFlowVersionsResponse{FlowVersionSummaries: out, NextToken: next})
	default:
		methodNotAllowed(w)
	}
}

func serveFlowVersion(w http.ResponseWriter, r *http.Request, c badriver.FlowVersions, flowID, version string) {
	switch r.Method {
	case http.MethodGet:
		flow, err := c.GetFlowVersion(r.Context(), flowID, version)
		if err != nil {
			writeErr(w, err)

			return
		}

		writeJSON(w, toFlowJSON(flow))
	case http.MethodDelete:
		if err := c.DeleteFlowVersion(r.Context(), flowID, version, queryBool(r, "skipResourceInUseCheck")); err != nil {
			writeErr(w, err)

			return
		}

		writeJSON(w, deleteFlowVersionResponse{ID: resourceIDOf(flowID, "flow/"), Version: version})
	default:
		methodNotAllowed(w)
	}
}

func serveFlowAliasCollection(w http.ResponseWriter, r *http.Request, c badriver.FlowVersions, flowID string) {
	switch r.Method {
	case http.MethodPost:
		var in flowAliasRequest
		if !decodeJSON(w, r, &in) {
			return
		}

		writeFlowAlias(w, http.StatusCreated)(c.CreateFlowAlias(r.Context(), in.config(flowID)))
	case http.MethodGet:
		page, ok := queryPage(w, r)
		if !ok {
			return
		}

		aliases, next, err := c.ListFlowAliases(r.Context(), flowID, page)
		if err != nil {
			writeErr(w, err)

			return
		}

		out := make([]flowAliasJSON, 0, len(aliases))

		for i := range aliases {
			out = append(out, toFlowAliasJSON(&aliases[i]))
		}

		writeJSON(w, listFlowAliasesResponse{FlowAliasSummaries: out, NextToken: next})
	default:
		methodNotAllowed(w)
	}
}

func serveFlowAlias(w http.ResponseWriter, r *http.Request, c badriver.FlowVersions, flowID, aliasID string) {
	switch r.Method {
	case http.MethodGet:
		writeFlowAlias(w, http.StatusOK)(c.GetFlowAlias(r.Context(), flowID, aliasID))
	case http.MethodPut:
		var in flowAliasRequest
		if !decodeJSON(w, r, &in) {
			return
		}

		writeFlowAlias(w, http.StatusOK)(c.UpdateFlowAlias(r.Context(), aliasID, in.config(flowID)))
	case http.MethodDelete:
		alias, err := c.DeleteFlowAlias(r.Context(), flowID, aliasID)
		if err != nil {
			writeErr(w, err)

			return
		}

		writeJSON(w, deleteFlowAliasResponse{FlowID: alias.FlowID, ID: alias.ID})
	default:
		methodNotAllowed(w)
	}
}

func writeFlowAlias(w http.ResponseWriter, status int) func(*badriver.FlowAlias, error) {
	return func(a *badriver.FlowAlias, err error) {
		if err != nil {
			writeErr(w, err)

			return
		}

		writeJSONStatus(w, status, toFlowAliasJSON(a))
	}
}

func (in *flowAliasRequest) config(flowID string) badriver.FlowAliasConfig {
	cfg := badriver.FlowAliasConfig{
		FlowID: flowID, Name: in.Name, Description: in.Description, ClientToken: in.ClientToken, Tags: in.Tags,
	}

	for _, rt := range in.RoutingConfiguration {
		cfg.RoutingConfiguration = append(cfg.RoutingConfiguration, badriver.FlowAliasRoute{FlowVersion: rt.FlowVersion})
	}

	if c := in.ConcurrencyConfiguration; c != nil {
		cfg.Concurrency = &badriver.FlowAliasConcurrency{Type: c.Type, MaxConcurrency: c.MaxConcurrency}
	}

	return cfg
}

func toFlowAliasJSON(a *badriver.FlowAlias) flowAliasJSON {
	out := flowAliasJSON{
		ID: a.ID, Arn: a.ARN, FlowID: a.FlowID, Name: a.Name, Description: a.Description,
		RoutingConfiguration: make([]flowAliasRouteJSON, 0, len(a.RoutingConfiguration)),
		CreatedAt:            a.CreatedAt, UpdatedAt: a.UpdatedAt,
	}

	for _, rt := range a.RoutingConfiguration {
		out.RoutingConfiguration = append(out.RoutingConfiguration, flowAliasRouteJSON{FlowVersion: rt.FlowVersion})
	}

	if c := a.Concurrency; c != nil {
		out.ConcurrencyConfiguration = &flowConcurrencyJSON{Type: c.Type, MaxConcurrency: c.MaxConcurrency}
	}

	return out
}

// resourceIDOf returns the id of an id-or-ARN identifier (what follows kind in
// an ARN).
func resourceIDOf(identifier, kind string) string {
	if i := strings.LastIndex(identifier, ":"+kind); i >= 0 {
		return identifier[i+len(kind)+1:]
	}

	return identifier
}
