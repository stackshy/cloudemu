package appsync

import (
	"encoding/json"
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/appsync/driver"
)

func (h *Handler) createGraphqlAPI(w http.ResponseWriter, r *http.Request) {
	raw, ok := decodeBodyMap(w, r)
	if !ok {
		return
	}

	var body graphqlAPIBody
	if !unmarshalBody(w, raw, &body) {
		return
	}

	out, err := h.as.CreateGraphqlAPI(r.Context(), &driver.CreateGraphqlAPIInput{
		Name:               body.Name,
		AuthenticationType: body.AuthenticationType,
		Visibility:         body.Visibility,
		APIType:            body.APIType,
		XrayEnabled:        body.XrayEnabled,
		Tags:               body.Tags,
		Extra:              extraFrom(raw, graphqlAPIModeledKeys),
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"graphqlApi": apiToWire(out)})
}

func (h *Handler) getGraphqlAPI(w http.ResponseWriter, r *http.Request, apiID string) {
	out, err := h.as.GetGraphqlAPI(r.Context(), apiID)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"graphqlApi": apiToWire(out)})
}

func (h *Handler) updateGraphqlAPI(w http.ResponseWriter, r *http.Request, apiID string) {
	raw, ok := decodeBodyMap(w, r)
	if !ok {
		return
	}

	var body graphqlAPIBody
	if !unmarshalBody(w, raw, &body) {
		return
	}

	out, err := h.as.UpdateGraphqlAPI(r.Context(), &driver.UpdateGraphqlAPIInput{
		APIID:              apiID,
		Name:               body.Name,
		AuthenticationType: body.AuthenticationType,
		XrayEnabled:        body.XrayEnabled,
		Extra:              extraFrom(raw, graphqlAPIModeledKeys),
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"graphqlApi": apiToWire(out)})
}

func (h *Handler) deleteGraphqlAPI(w http.ResponseWriter, r *http.Request, apiID string) {
	if err := h.as.DeleteGraphqlAPI(r.Context(), apiID); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{})
}

func (h *Handler) listGraphqlAPIs(w http.ResponseWriter, r *http.Request) {
	apis, next, err := h.as.ListGraphqlAPIs(r.Context(), pageFromQuery(r))
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]map[string]any, 0, len(apis))
	for i := range apis {
		out = append(out, apiToWire(&apis[i]))
	}

	writeJSON(w, withNext(map[string]any{"graphqlApis": out}, next))
}

// unmarshalBody re-marshals the raw request map into a typed struct, so both
// the modeled fields and the Extra passthrough derive from one decode.
func unmarshalBody(w http.ResponseWriter, raw map[string]json.RawMessage, v any) bool {
	b, err := json.Marshal(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, driver.ExBadRequest, "invalid JSON: "+err.Error())

		return false
	}

	if err := json.Unmarshal(b, v); err != nil {
		writeError(w, http.StatusBadRequest, driver.ExBadRequest, "invalid JSON: "+err.Error())

		return false
	}

	return true
}
