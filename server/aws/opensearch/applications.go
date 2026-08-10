package opensearch

import (
	"encoding/json"
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/opensearch/driver"
)

// serveApplication routes /opensearch/application and its sub-paths.
func (h *Handler) serveApplication(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 {
		if r.Method == http.MethodPost {
			h.createApplication(w, r)

			return
		}

		methodNotAllowed(w)

		return
	}

	id := rest[0]

	if len(rest) == 1 {
		h.serveApplicationByID(w, r, id)

		return
	}

	switch rest[1] {
	case "attachDataSource":
		h.attachDataSource(w, r, id)
	case "detachDataSource":
		h.detachDataSource(w, r, id)
	case "describeDataSourceAttachment":
		h.describeDataSourceAttachment(w, r, id)
	case "listDataSourceAttachments":
		h.listDataSourceAttachments(w, r, id)
	case "capability":
		h.serveCapability(w, r, id, rest[2:])
	default:
		notFoundPath(w, r.URL.Path)
	}
}

func (h *Handler) serveApplicationByID(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		h.getApplication(w, r, id)
	case http.MethodPut:
		h.updateApplication(w, r, id)
	case http.MethodDelete:
		h.deleteApplication(w, r, id)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) createApplication(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name                     string                       `json:"name"`
		DataSources              []map[string]json.RawMessage `json:"dataSources"`
		IamIdentityCenterOptions map[string]json.RawMessage   `json:"iamIdentityCenterOptions"`
		AppConfigs               []map[string]json.RawMessage `json:"appConfigs"`
		TagList                  []tag                        `json:"tagList"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	out, err := h.os.CreateApplication(r.Context(), driver.CreateApplicationInput{
		Name:                     req.Name,
		DataSources:              req.DataSources,
		IamIdentityCenterOptions: req.IamIdentityCenterOptions,
		AppConfigs:               req.AppConfigs,
		TagList:                  tagsToMap(req.TagList),
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, applicationToWire(out))
}

func (h *Handler) getApplication(w http.ResponseWriter, r *http.Request, id string) {
	out, err := h.os.GetApplication(r.Context(), id)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, applicationToWire(out))
}

func (h *Handler) updateApplication(w http.ResponseWriter, r *http.Request, id string) {
	var req struct {
		DataSources []map[string]json.RawMessage `json:"dataSources"`
		AppConfigs  []map[string]json.RawMessage `json:"appConfigs"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	out, err := h.os.UpdateApplication(r.Context(), id, req.DataSources, req.AppConfigs)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, applicationToWire(out))
}

func (h *Handler) deleteApplication(w http.ResponseWriter, r *http.Request, id string) {
	if err := h.os.DeleteApplication(r.Context(), id); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{})
}

func (h *Handler) listApplications(w http.ResponseWriter, r *http.Request) {
	list, next, err := h.os.ListApplications(r.Context(), pageFromQuery(r))
	if err != nil {
		writeErr(w, err)

		return
	}

	summaries := make([]map[string]any, 0, len(list))
	for i := range list {
		summaries = append(summaries, map[string]any{
			"id":            list[i].ID,
			"arn":           list[i].ARN,
			"name":          list[i].Name,
			"endpoint":      list[i].Endpoint,
			"status":        list[i].Status,
			"createdAt":     list[i].CreatedAt.Unix(),
			"lastUpdatedAt": list[i].LastUpdatedAt.Unix(),
		})
	}

	writeJSON(w, withNext(map[string]any{"ApplicationSummaries": summaries}, next))
}

// serveCapability routes /application/{id}/capability/*.
func (h *Handler) serveCapability(w http.ResponseWriter, r *http.Request, id string, rest []string) {
	const wantSegs = 2
	if len(rest) != wantSegs {
		notFoundPath(w, r.URL.Path)

		return
	}

	switch {
	case rest[0] == "register" && r.Method == http.MethodPost:
		h.registerCapability(w, r, id, rest[1])
	case rest[0] == "deregister" && r.Method == http.MethodDelete:
		h.deregisterCapability(w, r, id, rest[1])
	case r.Method == http.MethodGet:
		h.getCapability(w, r, id, rest[1])
	default:
		notFoundPath(w, r.URL.Path)
	}
}

func (h *Handler) registerCapability(w http.ResponseWriter, r *http.Request, id, capability string) {
	var payload map[string]json.RawMessage
	if !decodeJSON(w, r, &payload) {
		return
	}

	out, err := h.os.RegisterCapability(r.Context(), id, capability, payload)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, out)
}

func (h *Handler) deregisterCapability(w http.ResponseWriter, r *http.Request, id, capability string) {
	if err := h.os.DeregisterCapability(r.Context(), id, capability); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{})
}

func (h *Handler) getCapability(w http.ResponseWriter, r *http.Request, id, capability string) {
	out, err := h.os.GetCapability(r.Context(), id, capability)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, out)
}

func (h *Handler) attachDataSource(w http.ResponseWriter, r *http.Request, id string) {
	var ds map[string]json.RawMessage
	if !decodeJSON(w, r, &ds) {
		return
	}

	out, err := h.os.AttachDataSource(r.Context(), id, ds)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, out)
}

func (h *Handler) detachDataSource(w http.ResponseWriter, r *http.Request, id string) {
	var req struct {
		DataSourceArn string `json:"dataSourceArn"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	out, err := h.os.DetachDataSource(r.Context(), id, req.DataSourceArn)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, out)
}

func (h *Handler) describeDataSourceAttachment(w http.ResponseWriter, r *http.Request, id string) {
	var req struct {
		DataSourceAttachmentID string `json:"dataSourceAttachmentId"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	out, err := h.os.DescribeDataSourceAttachment(r.Context(), id, req.DataSourceAttachmentID)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, out)
}

func (h *Handler) listDataSourceAttachments(w http.ResponseWriter, r *http.Request, id string) {
	list, next, err := h.os.ListDataSourceAttachments(r.Context(), id, pageFromQuery(r))
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, withNext(map[string]any{"dataSourceAttachments": list}, next))
}

// serveDefaultAppSetting handles GET/PUT /opensearch/defaultApplicationSetting.
func (h *Handler) serveDefaultAppSetting(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		out, err := h.os.GetDefaultApplicationSetting(r.Context())
		if err != nil {
			writeErr(w, err)

			return
		}

		writeJSON(w, out)
	case http.MethodPut:
		var setting map[string]json.RawMessage
		if !decodeJSON(w, r, &setting) {
			return
		}

		out, err := h.os.PutDefaultApplicationSetting(r.Context(), setting)
		if err != nil {
			writeErr(w, err)

			return
		}

		writeJSON(w, out)
	default:
		methodNotAllowed(w)
	}
}
