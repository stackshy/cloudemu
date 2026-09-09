package appsync

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/appsync/driver"
)

// serveDataSources routes /v1/apis/{apiId}/datasources and its item paths.
func (h *Handler) serveDataSources(w http.ResponseWriter, r *http.Request, apiID string, rest []string) {
	if len(rest) == 0 {
		switch r.Method {
		case http.MethodPost:
			h.createDataSource(w, r, apiID)
		case http.MethodGet:
			h.listDataSources(w, r, apiID)
		default:
			methodNotAllowed(w)
		}

		return
	}

	if len(rest) != 1 {
		notFoundPath(w, r.URL.Path)

		return
	}

	name := rest[0]

	switch r.Method {
	case http.MethodGet:
		h.getDataSource(w, r, apiID, name)
	case http.MethodPost:
		h.updateDataSource(w, r, apiID, name)
	case http.MethodDelete:
		h.deleteDataSource(w, r, apiID, name)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) createDataSource(w http.ResponseWriter, r *http.Request, apiID string) {
	raw, ok := decodeBodyMap(w, r)
	if !ok {
		return
	}

	var body dataSourceBody
	if !unmarshalBody(w, raw, &body) {
		return
	}

	out, err := h.as.CreateDataSource(r.Context(), &driver.CreateDataSourceInput{
		APIID:          apiID,
		Name:           body.Name,
		Type:           body.Type,
		Description:    body.Description,
		ServiceRoleArn: body.ServiceRoleArn,
		Extra:          extraFrom(raw, dataSourceModeledKeys),
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"dataSource": dataSourceToWire(out)})
}

func (h *Handler) getDataSource(w http.ResponseWriter, r *http.Request, apiID, name string) {
	out, err := h.as.GetDataSource(r.Context(), apiID, name)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"dataSource": dataSourceToWire(out)})
}

func (h *Handler) updateDataSource(w http.ResponseWriter, r *http.Request, apiID, name string) {
	raw, ok := decodeBodyMap(w, r)
	if !ok {
		return
	}

	var body dataSourceBody
	if !unmarshalBody(w, raw, &body) {
		return
	}

	out, err := h.as.UpdateDataSource(r.Context(), &driver.UpdateDataSourceInput{
		APIID:          apiID,
		Name:           name,
		Type:           body.Type,
		Description:    body.Description,
		ServiceRoleArn: body.ServiceRoleArn,
		Extra:          extraFrom(raw, dataSourceModeledKeys),
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"dataSource": dataSourceToWire(out)})
}

func (h *Handler) deleteDataSource(w http.ResponseWriter, r *http.Request, apiID, name string) {
	if err := h.as.DeleteDataSource(r.Context(), apiID, name); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{})
}

func (h *Handler) listDataSources(w http.ResponseWriter, r *http.Request, apiID string) {
	sources, next, err := h.as.ListDataSources(r.Context(), apiID, pageFromQuery(r))
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]map[string]any, 0, len(sources))
	for i := range sources {
		out = append(out, dataSourceToWire(&sources[i]))
	}

	writeJSON(w, withNext(map[string]any{"dataSources": out}, next))
}
