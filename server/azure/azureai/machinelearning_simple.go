package azureai

import (
	"net/http"

	mldriver "github.com/stackshy/cloudemu/azureai/driver"
	"github.com/stackshy/cloudemu/server/wire/azurearm"
)

func datastoreJSON(d *mldriver.Datastore) map[string]any {
	return map[string]any{
		"id": d.ID, "name": d.Name, "type": mlProvider + "/workspaces/datastores",
		"properties": map[string]any{
			"datastoreType": d.StoreType, "accountName": d.AccountName, "containerName": d.Container,
		},
	}
}

//nolint:dupl // uniform workspace-child CRUD; shape recurs across collections.
func (h *MachineLearningHandler) serveDatastores(w http.ResponseWriter, r *http.Request, p *mlPath, ws string) {
	if len(p.rest) == mlLenCollection {
		if r.Method != http.MethodGet {
			writeMLMethodNotAllowed(w)

			return
		}

		ds, err := h.svc.ListDatastores(r.Context(), p.resourceGroup, ws)
		writeMLList(w, datastoreJSON, ds, err)

		return
	}

	name := p.rest[3]

	switch r.Method {
	case http.MethodPut:
		var body struct {
			Properties struct {
				DatastoreType string `json:"datastoreType"`
				AccountName   string `json:"accountName"`
				ContainerName string `json:"containerName"`
			} `json:"properties"`
		}

		if !azurearm.DecodeJSON(w, r, &body) {
			return
		}

		d, err := h.svc.CreateDatastore(r.Context(), mldriver.DatastoreConfig{
			Workspace: ws, ResourceGroup: p.resourceGroup, Name: name,
			StoreType: body.Properties.DatastoreType, AccountName: body.Properties.AccountName,
			Container: body.Properties.ContainerName,
		})
		writeML(w, datastoreJSON, d, err)
	case http.MethodGet:
		d, err := h.svc.GetDatastore(r.Context(), p.resourceGroup, ws, name)
		writeML(w, datastoreJSON, d, err)
	case http.MethodDelete:
		writeMLDelete(w, h.svc.DeleteDatastore(r.Context(), p.resourceGroup, ws, name))
	default:
		writeMLMethodNotAllowed(w)
	}
}

func connectionJSON(c *mldriver.Connection) map[string]any {
	return map[string]any{
		"id": c.ID, "name": c.Name, "type": mlProvider + "/workspaces/connections",
		"properties": map[string]any{"category": c.Category, "target": c.Target, "authType": c.AuthType},
	}
}

//nolint:dupl // uniform workspace-child CRUD; shape recurs across collections.
func (h *MachineLearningHandler) serveConnections(w http.ResponseWriter, r *http.Request, p *mlPath, ws string) {
	if len(p.rest) == mlLenCollection {
		if r.Method != http.MethodGet {
			writeMLMethodNotAllowed(w)

			return
		}

		cs, err := h.svc.ListConnections(r.Context(), p.resourceGroup, ws)
		writeMLList(w, connectionJSON, cs, err)

		return
	}

	name := p.rest[3]

	switch r.Method {
	case http.MethodPut:
		var body struct {
			Properties struct {
				Category string `json:"category"`
				Target   string `json:"target"`
				AuthType string `json:"authType"`
			} `json:"properties"`
		}

		if !azurearm.DecodeJSON(w, r, &body) {
			return
		}

		c, err := h.svc.CreateConnection(r.Context(), mldriver.ConnectionConfig{
			Workspace: ws, ResourceGroup: p.resourceGroup, Name: name,
			Category: body.Properties.Category, Target: body.Properties.Target, AuthType: body.Properties.AuthType,
		})
		writeML(w, connectionJSON, c, err)
	case http.MethodGet:
		c, err := h.svc.GetConnection(r.Context(), p.resourceGroup, ws, name)
		writeML(w, connectionJSON, c, err)
	case http.MethodDelete:
		writeMLDelete(w, h.svc.DeleteConnection(r.Context(), p.resourceGroup, ws, name))
	default:
		writeMLMethodNotAllowed(w)
	}
}

func mlScheduleJSON(s *mldriver.MLSchedule) map[string]any {
	return map[string]any{
		"id": s.ID, "name": s.Name, "type": mlProvider + "/workspaces/schedules",
		"properties": map[string]any{
			"displayName": s.DisplayName, "isEnabled": s.IsEnabled,
			"trigger": map[string]any{"expression": s.Cron},
		},
	}
}

func (h *MachineLearningHandler) serveSchedules(w http.ResponseWriter, r *http.Request, p *mlPath, ws string) {
	if len(p.rest) == mlLenCollection {
		if r.Method != http.MethodGet {
			writeMLMethodNotAllowed(w)

			return
		}

		ss, err := h.svc.ListMLSchedules(r.Context(), p.resourceGroup, ws)
		writeMLList(w, mlScheduleJSON, ss, err)

		return
	}

	name := p.rest[3]

	switch r.Method {
	case http.MethodPut:
		var body struct {
			Properties struct {
				DisplayName string `json:"displayName"`
				Trigger     struct {
					Expression string `json:"expression"`
				} `json:"trigger"`
			} `json:"properties"`
		}

		if !azurearm.DecodeJSON(w, r, &body) {
			return
		}

		s, err := h.svc.CreateMLSchedule(r.Context(), mldriver.MLScheduleConfig{
			Workspace: ws, ResourceGroup: p.resourceGroup, Name: name,
			Cron: body.Properties.Trigger.Expression, DisplayName: body.Properties.DisplayName,
		})
		writeML(w, mlScheduleJSON, s, err)
	case http.MethodGet:
		s, err := h.svc.GetMLSchedule(r.Context(), p.resourceGroup, ws, name)
		writeML(w, mlScheduleJSON, s, err)
	case http.MethodDelete:
		writeMLDelete(w, h.svc.DeleteMLSchedule(r.Context(), p.resourceGroup, ws, name))
	default:
		writeMLMethodNotAllowed(w)
	}
}

// writeMLList writes a "value"-wrapped list or maps the error.
func writeMLList[T any](w http.ResponseWriter, toJSON func(*T) map[string]any, items []T, err error) {
	if err != nil {
		azurearm.WriteCErr(w, err)

		return
	}

	out := make([]map[string]any, 0, len(items))
	for i := range items {
		out = append(out, toJSON(&items[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, map[string]any{"value": out})
}
