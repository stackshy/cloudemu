package kafka

import (
	"encoding/json"
	"net/http"
)

// listNodes handles GET /v1/clusters/{arn}/nodes, rendering the NodeInfoList.
func (h *Handler) listNodes(w http.ResponseWriter, r *http.Request, arn string) {
	list, next, err := h.k.ListNodes(r.Context(), arn, pageFromQuery(r))
	if err != nil {
		writeErr(w, err)

		return
	}

	infos := make([]map[string]json.RawMessage, 0, len(list))
	for i := range list {
		infos = append(infos, nodeToWire(list[i]))
	}

	writeJSON(w, withNext(map[string]any{"nodeInfoList": infos}, next))
}
