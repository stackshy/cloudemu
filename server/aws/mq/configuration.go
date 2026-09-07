package mq

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/mq/driver"
)

func (h *Handler) createConfiguration(w http.ResponseWriter, r *http.Request) {
	raw, ok := decodeBodyMap(w, r)
	if !ok {
		return
	}

	out, err := h.mq.CreateConfiguration(r.Context(), &driver.CreateConfigurationInput{
		Name:                   rawString(raw, "name"),
		EngineType:             rawString(raw, "engineType"),
		EngineVersion:          rawString(raw, "engineVersion"),
		AuthenticationStrategy: rawString(raw, "authenticationStrategy"),
		Tags:                   tagsFromBody(raw),
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, configurationToWire(out))
}

func (h *Handler) describeConfiguration(w http.ResponseWriter, r *http.Request, configID string) {
	out, err := h.mq.DescribeConfiguration(r.Context(), configID)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, configurationToWire(out))
}

func (h *Handler) updateConfiguration(w http.ResponseWriter, r *http.Request, configID string) {
	raw, ok := decodeBodyMap(w, r)
	if !ok {
		return
	}

	out, err := h.mq.UpdateConfiguration(r.Context(), &driver.UpdateConfigurationInput{
		ConfigurationID: configID,
		Data:            rawString(raw, "data"),
		Description:     rawString(raw, "description"),
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	body := configurationToWire(out)
	body["warnings"] = []map[string]any{}

	writeJSON(w, body)
}

//nolint:dupl // parallel list-handler shape; distinct driver call and response key.
func (h *Handler) listConfigurations(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	configs, next, err := h.mq.ListConfigurations(r.Context(), driver.Page{
		NextToken:  q.Get("nextToken"),
		MaxResults: atoiDefault(q.Get("maxResults")),
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	items := make([]map[string]any, 0, len(configs))
	for _, c := range configs {
		items = append(items, configurationToWire(c))
	}

	body := map[string]any{"configurations": items}
	if next != "" {
		body["nextToken"] = next
	}

	writeJSON(w, body)
}

func (h *Handler) describeConfigurationRevision(w http.ResponseWriter, r *http.Request, configID, revision string) {
	rev, err := h.mq.DescribeConfigurationRevision(r.Context(), configID, atoiDefault(revision))
	if err != nil {
		writeErr(w, err)

		return
	}

	body := map[string]any{
		"configurationId": configID,
		"created":         iso8601OrNil(rev.Created),
		"data":            rev.Data,
	}
	if rev.Description != "" {
		body["description"] = rev.Description
	}

	writeJSON(w, body)
}
