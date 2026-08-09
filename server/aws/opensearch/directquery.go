package opensearch

import (
	"encoding/json"
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/opensearch/driver"
)

// serveDirectQuery routes /opensearch/directQueryDataSource[/{name}].
func (h *Handler) serveDirectQuery(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 {
		switch r.Method {
		case http.MethodPost:
			h.addDirectQuery(w, r)
		case http.MethodGet:
			h.listDirectQuery(w, r)
		default:
			methodNotAllowed(w)
		}

		return
	}

	if len(rest) == 1 {
		h.serveDirectQueryByName(w, r, rest[0])

		return
	}

	notFoundPath(w, r.URL.Path)
}

func (h *Handler) serveDirectQueryByName(w http.ResponseWriter, r *http.Request, name string) {
	switch r.Method {
	case http.MethodGet:
		h.getDirectQuery(w, r, name)
	case http.MethodPut:
		h.updateDirectQuery(w, r, name)
	case http.MethodDelete:
		h.deleteDirectQuery(w, r, name)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) addDirectQuery(w http.ResponseWriter, r *http.Request) {
	var req dqWire
	if !decodeJSON(w, r, &req) {
		return
	}

	arn, err := h.os.AddDirectQueryDataSource(r.Context(), req.toDriver())
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"DataSourceArn": arn})
}

func (h *Handler) getDirectQuery(w http.ResponseWriter, r *http.Request, name string) {
	ds, err := h.os.GetDirectQueryDataSource(r.Context(), name)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, dqToWire(ds))
}

func (h *Handler) updateDirectQuery(w http.ResponseWriter, r *http.Request, name string) {
	var req dqWire
	if !decodeJSON(w, r, &req) {
		return
	}

	req.DataSourceName = name

	arn, err := h.os.UpdateDirectQueryDataSource(r.Context(), req.toDriver())
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"DataSourceArn": arn})
}

func (h *Handler) deleteDirectQuery(w http.ResponseWriter, r *http.Request, name string) {
	if err := h.os.DeleteDirectQueryDataSource(r.Context(), name); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{})
}

func (h *Handler) listDirectQuery(w http.ResponseWriter, r *http.Request) {
	list, err := h.os.ListDirectQueryDataSources(r.Context())
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]map[string]any, 0, len(list))
	for i := range list {
		out = append(out, dqToWire(&list[i]))
	}

	writeJSON(w, map[string]any{"DirectQueryDataSources": out})
}

// dqWire is the wire shape of a direct-query data source.
type dqWire struct {
	DataSourceName string                     `json:"DataSourceName"`
	DataSourceType map[string]json.RawMessage `json:"DataSourceType"`
	Description    string                     `json:"Description"`
	OpenSearchArns []string                   `json:"OpenSearchArns"`
	TagList        []tag                      `json:"TagList"`
}

//nolint:gocritic // hugeParam: small wire struct decoded once per request.
func (d dqWire) toDriver() driver.DirectQueryDataSource {
	return driver.DirectQueryDataSource{
		DataSourceName: d.DataSourceName,
		DataSourceType: d.DataSourceType,
		Description:    d.Description,
		OpenSearchArns: d.OpenSearchArns,
		TagList:        tagsToMap(d.TagList),
	}
}

func dqToWire(d *driver.DirectQueryDataSource) map[string]any {
	return map[string]any{
		"DataSourceName": d.DataSourceName,
		"DataSourceType": d.DataSourceType,
		"Description":    d.Description,
		"DataSourceArn":  d.ARN,
		"OpenSearchArns": d.OpenSearchArns,
		"TagList":        mapToTags(d.TagList),
	}
}
