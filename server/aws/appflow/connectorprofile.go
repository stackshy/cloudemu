package appflow

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/appflow/driver"
)

func (h *Handler) createConnectorProfile(w http.ResponseWriter, r *http.Request) {
	raw, ok := decodeBodyMap(w, r)
	if !ok {
		return
	}

	var body connectorProfileBody
	if !unmarshalBody(w, raw, &body) {
		return
	}

	out, err := h.af.CreateConnectorProfile(r.Context(), &driver.CreateConnectorProfileInput{
		ConnectorProfileName: body.ConnectorProfileName,
		ConnectorType:        body.ConnectorType,
		ConnectorLabel:       body.ConnectorLabel,
		ConnectionMode:       body.ConnectionMode,
		Extra:                extraFrom(raw, connectorProfileModeledKeys),
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"connectorProfileArn": out.ConnectorProfileArn})
}

func (h *Handler) updateConnectorProfile(w http.ResponseWriter, r *http.Request) {
	raw, ok := decodeBodyMap(w, r)
	if !ok {
		return
	}

	var body connectorProfileBody
	if !unmarshalBody(w, raw, &body) {
		return
	}

	out, err := h.af.UpdateConnectorProfile(r.Context(), &driver.UpdateConnectorProfileInput{
		ConnectorProfileName: body.ConnectorProfileName,
		Extra:                extraFrom(raw, connectorProfileModeledKeys),
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"connectorProfileArn": out.ConnectorProfileArn})
}

func (h *Handler) deleteConnectorProfile(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ConnectorProfileName string `json:"connectorProfileName"`
		ForceDelete          bool   `json:"forceDelete"`
	}

	if !decodeJSON(w, r, &body) {
		return
	}

	if err := h.af.DeleteConnectorProfile(r.Context(), body.ConnectorProfileName, body.ForceDelete); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{})
}

func (h *Handler) describeConnectorProfiles(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ConnectorProfileNames []string `json:"connectorProfileNames"`
		ConnectorType         string   `json:"connectorType"`
		MaxResults            int32    `json:"maxResults"`
		NextToken             string   `json:"nextToken"`
	}

	if !decodeJSON(w, r, &body) {
		return
	}

	profiles, next, err := h.af.DescribeConnectorProfiles(r.Context(), body.ConnectorProfileNames,
		body.ConnectorType, driver.Page{NextToken: body.NextToken, MaxResults: body.MaxResults})
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]map[string]any, 0, len(profiles))
	for i := range profiles {
		out = append(out, connectorProfileToWire(&profiles[i]))
	}

	writeJSON(w, withNext(map[string]any{"connectorProfileDetails": out}, next))
}
