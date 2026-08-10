package opensearch

import (
	"encoding/json"
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/opensearch/driver"
)

// serveMigrations routes /opensearch/app-migrations and /app-migrations/{id}.
func (h *Handler) serveMigrations(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 {
		switch r.Method {
		case http.MethodPost:
			h.startMigration(w, r)
		case http.MethodGet:
			h.listMigrations(w, r)
		default:
			methodNotAllowed(w)
		}

		return
	}

	if len(rest) == 1 && r.Method == http.MethodGet {
		h.getMigration(w, r, rest[0])

		return
	}

	notFoundPath(w, r.URL.Path)
}

func (h *Handler) startMigration(w http.ResponseWriter, r *http.Request) {
	var body map[string]json.RawMessage
	if !decodeJSON(w, r, &body) {
		return
	}

	id, err := h.os.StartMigration(r.Context(), body)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"MigrationId": id})
}

func (h *Handler) getMigration(w http.ResponseWriter, r *http.Request, id string) {
	out, err := h.os.GetMigration(r.Context(), id)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, out)
}

func (h *Handler) listMigrations(w http.ResponseWriter, r *http.Request) {
	list, next, err := h.os.ListMigrations(r.Context(), pageFromQuery(r))
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, withNext(map[string]any{"Migrations": list}, next))
}

func (h *Handler) listInsights(w http.ResponseWriter, r *http.Request) {
	page := pageFromBody(w, r)
	if page == nil {
		return
	}

	list, next, err := h.os.ListInsights(r.Context(), *page)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, withNext(map[string]any{"Insights": list}, next))
}

func (h *Handler) describeInsightDetails(w http.ResponseWriter, r *http.Request) {
	var req struct {
		InsightID string `json:"InsightId"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	out, err := h.os.DescribeInsightDetails(r.Context(), req.InsightID)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, out)
}

func (h *Handler) insightFeedback(w http.ResponseWriter, r *http.Request) {
	var req struct {
		InsightID string `json:"InsightId"`
		Feedback  string `json:"Feedback"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	if err := h.os.InsightFeedback(r.Context(), req.InsightID, req.Feedback); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{})
}

// serveDataSource routes /opensearch/domain/{name}/dataSource[/{dsName}].
func (h *Handler) serveDataSource(w http.ResponseWriter, r *http.Request, domainName string, rest []string) {
	if len(rest) == 0 {
		switch r.Method {
		case http.MethodPost:
			h.addDataSource(w, r, domainName)
		case http.MethodGet:
			h.listDataSources(w, r, domainName)
		default:
			methodNotAllowed(w)
		}

		return
	}

	if len(rest) == 1 {
		h.serveDataSourceByName(w, r, domainName, rest[0])

		return
	}

	notFoundPath(w, r.URL.Path)
}

func (h *Handler) serveDataSourceByName(w http.ResponseWriter, r *http.Request, domainName, name string) {
	switch r.Method {
	case http.MethodGet:
		h.getDataSource(w, r, domainName, name)
	case http.MethodPut:
		h.updateDataSource(w, r, domainName, name)
	case http.MethodDelete:
		h.deleteDataSource(w, r, domainName, name)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) addDataSource(w http.ResponseWriter, r *http.Request, domainName string) {
	var req dataSourceWire
	if !decodeJSON(w, r, &req) {
		return
	}

	msg, err := h.os.AddDataSource(r.Context(), domainName, req.toDriver())
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"Message": msg})
}

func (h *Handler) getDataSource(w http.ResponseWriter, r *http.Request, domainName, name string) {
	ds, err := h.os.GetDataSource(r.Context(), domainName, name)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, dataSourceToWire(ds))
}

func (h *Handler) updateDataSource(w http.ResponseWriter, r *http.Request, domainName, name string) {
	var req dataSourceWire
	if !decodeJSON(w, r, &req) {
		return
	}

	req.Name = name

	msg, err := h.os.UpdateDataSource(r.Context(), domainName, req.toDriver())
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"Message": msg})
}

func (h *Handler) deleteDataSource(w http.ResponseWriter, r *http.Request, domainName, name string) {
	msg, err := h.os.DeleteDataSource(r.Context(), domainName, name)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"Message": msg})
}

func (h *Handler) listDataSources(w http.ResponseWriter, r *http.Request, domainName string) {
	list, err := h.os.ListDataSources(r.Context(), domainName)
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]map[string]any, 0, len(list))
	for i := range list {
		out = append(out, dataSourceToWire(&list[i]))
	}

	writeJSON(w, map[string]any{"DataSources": out})
}

// serveIndex routes /opensearch/domain/{name}/index[/{indexName}].
func (h *Handler) serveIndex(w http.ResponseWriter, r *http.Request, domainName string, rest []string) {
	if len(rest) == 0 {
		if r.Method == http.MethodPost {
			h.createIndex(w, r, domainName)

			return
		}

		methodNotAllowed(w)

		return
	}

	if len(rest) == 1 {
		h.serveIndexByName(w, r, domainName, rest[0])

		return
	}

	notFoundPath(w, r.URL.Path)
}

func (h *Handler) serveIndexByName(w http.ResponseWriter, r *http.Request, domainName, indexName string) {
	switch r.Method {
	case http.MethodGet:
		out, err := h.os.GetIndex(r.Context(), domainName, indexName)
		h.indexResult(w, out, err)
	case http.MethodPut:
		var settings map[string]json.RawMessage
		if !decodeJSON(w, r, &settings) {
			return
		}

		out, err := h.os.UpdateIndex(r.Context(), domainName, indexName, settings)
		h.indexResult(w, out, err)
	case http.MethodDelete:
		out, err := h.os.DeleteIndex(r.Context(), domainName, indexName)
		h.indexResult(w, out, err)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) createIndex(w http.ResponseWriter, r *http.Request, domainName string) {
	var req struct {
		IndexName string                     `json:"IndexName"`
		Settings  map[string]json.RawMessage `json:"Settings"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	out, err := h.os.CreateIndex(r.Context(), domainName, req.IndexName, req.Settings)
	h.indexResult(w, out, err)
}

func (*Handler) indexResult(w http.ResponseWriter, out map[string]json.RawMessage, err error) {
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, out)
}

// serveDomainMaintenance handles POST (start) and GET (status) on
// /opensearch/domain/{name}/domainMaintenance.
func (h *Handler) serveDomainMaintenance(w http.ResponseWriter, r *http.Request, domainName string) {
	switch r.Method {
	case http.MethodPost:
		var req struct {
			Action string `json:"Action"`
			NodeID string `json:"NodeId"`
		}

		if !decodeJSON(w, r, &req) {
			return
		}

		id, err := h.os.StartDomainMaintenance(r.Context(), domainName, req.Action, req.NodeID)
		if err != nil {
			writeErr(w, err)

			return
		}

		writeJSON(w, map[string]any{"MaintenanceId": id})
	case http.MethodGet:
		out, err := h.os.GetDomainMaintenanceStatus(r.Context(), domainName, r.URL.Query().Get("maintenanceId"))
		if err != nil {
			writeErr(w, err)

			return
		}

		writeJSON(w, out)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) listDomainMaintenances(w http.ResponseWriter, r *http.Request, domainName string) {
	q := r.URL.Query()

	list, next, err := h.os.ListDomainMaintenances(r.Context(), domainName, q.Get("action"), q.Get("status"), pageFromQuery(r))
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, withNext(map[string]any{"DomainMaintenances": list}, next))
}

func (h *Handler) listScheduledActions(w http.ResponseWriter, r *http.Request, domainName string) {
	list, next, err := h.os.ListScheduledActions(r.Context(), domainName, pageFromQuery(r))
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, withNext(map[string]any{"ScheduledActions": list}, next))
}

// serveScheduledAction handles PUT /opensearch/domain/{name}/scheduledAction/update.
func (h *Handler) serveScheduledAction(w http.ResponseWriter, r *http.Request, domainName string, rest []string) {
	if len(rest) != 1 || rest[0] != segUpdate || r.Method != http.MethodPut {
		notFoundPath(w, r.URL.Path)

		return
	}

	var req struct {
		ActionID         string `json:"ActionID"`
		ActionType       string `json:"ActionType"`
		ScheduleAt       string `json:"ScheduleAt"`
		DesiredStartTime int64  `json:"DesiredStartTime"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	out, err := h.os.UpdateScheduledAction(r.Context(), domainName, req.ActionID, req.ActionType, req.ScheduleAt, req.DesiredStartTime)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"ScheduledAction": out})
}

// dataSourceWire is the wire shape of a per-domain data source.
type dataSourceWire struct {
	Name           string                     `json:"Name"`
	DataSourceType map[string]json.RawMessage `json:"DataSourceType"`
	Description    string                     `json:"Description"`
	Status         string                     `json:"Status"`
}

func (d dataSourceWire) toDriver() driver.DataSource {
	return driver.DataSource{
		Name:           d.Name,
		DataSourceType: d.DataSourceType,
		Description:    d.Description,
		Status:         d.Status,
	}
}

func dataSourceToWire(d *driver.DataSource) map[string]any {
	return map[string]any{
		"Name":           d.Name,
		"DataSourceType": d.DataSourceType,
		"Description":    d.Description,
		"Status":         d.Status,
	}
}
