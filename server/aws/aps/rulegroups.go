package aps

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/aps/driver"
)

// serveRuleGroupsCollection routes /workspaces/{id}/rulegroupsnamespaces.
func (h *Handler) serveRuleGroupsCollection(w http.ResponseWriter, r *http.Request, workspaceID string) {
	switch r.Method {
	case http.MethodGet:
		h.listRuleGroupsNamespaces(w, r, workspaceID)
	case http.MethodPost:
		h.createRuleGroupsNamespace(w, r, workspaceID)
	default:
		methodNotAllowed(w)
	}
}

// serveRuleGroupsItem routes /workspaces/{id}/rulegroupsnamespaces/{name}.
func (h *Handler) serveRuleGroupsItem(w http.ResponseWriter, r *http.Request, workspaceID, name string) {
	switch r.Method {
	case http.MethodGet:
		h.describeRuleGroupsNamespace(w, r, workspaceID, name)
	case http.MethodPut:
		h.putRuleGroupsNamespace(w, r, workspaceID, name)
	case http.MethodDelete:
		h.deleteRuleGroupsNamespace(w, r, workspaceID, name)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) createRuleGroupsNamespace(w http.ResponseWriter, r *http.Request, workspaceID string) {
	raw, ok := decodeBodyMap(w, r)
	if !ok {
		return
	}

	ns, err := h.aps.CreateRuleGroupsNamespace(r.Context(), &driver.RuleGroupsNamespaceInput{
		WorkspaceID: workspaceID,
		Name:        stringField(raw, "name"),
		Data:        stringField(raw, "data"),
		Tags:        tagsFromBody(raw),
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	body := map[string]any{
		"arn":    ns.Arn,
		"name":   ns.Name,
		"status": statusBlock(ns.Status),
	}
	if ns.Tags != nil {
		body["tags"] = ns.Tags
	}

	writeJSON(w, body)
}

func (h *Handler) putRuleGroupsNamespace(w http.ResponseWriter, r *http.Request, workspaceID, name string) {
	raw, ok := decodeBodyMap(w, r)
	if !ok {
		return
	}

	ns, err := h.aps.PutRuleGroupsNamespace(r.Context(), &driver.RuleGroupsNamespaceInput{
		WorkspaceID: workspaceID,
		Name:        name,
		Data:        stringField(raw, "data"),
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	body := map[string]any{
		"arn":    ns.Arn,
		"name":   ns.Name,
		"status": statusBlock(ns.Status),
	}
	if ns.Tags != nil {
		body["tags"] = ns.Tags
	}

	writeJSON(w, body)
}

func (h *Handler) describeRuleGroupsNamespace(w http.ResponseWriter, r *http.Request, workspaceID, name string) {
	ns, err := h.aps.DescribeRuleGroupsNamespace(r.Context(), workspaceID, name)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"ruleGroupsNamespace": ruleGroupsNamespaceToWire(ns)})
}

func (h *Handler) deleteRuleGroupsNamespace(w http.ResponseWriter, r *http.Request, workspaceID, name string) {
	if err := h.aps.DeleteRuleGroupsNamespace(r.Context(), workspaceID, name); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{})
}

func (h *Handler) listRuleGroupsNamespaces(w http.ResponseWriter, r *http.Request, workspaceID string) {
	q := r.URL.Query()

	namespaces, next, err := h.aps.ListRuleGroupsNamespaces(r.Context(), workspaceID, q.Get("name"), driver.Page{
		NextToken:  q.Get("nextToken"),
		MaxResults: atoiDefault(q.Get("maxResults")),
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	summaries := make([]map[string]any, 0, len(namespaces))
	for i := range namespaces {
		summaries = append(summaries, ruleGroupsNamespaceSummaryToWire(&namespaces[i]))
	}

	body := map[string]any{"ruleGroupsNamespaces": summaries}
	if next != "" {
		body["nextToken"] = next
	}

	writeJSON(w, body)
}
