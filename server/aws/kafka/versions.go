package kafka

import (
	"encoding/json"
	"net/http"
)

// listKafkaVersions handles GET /v1/kafka-versions.
func (h *Handler) listKafkaVersions(w http.ResponseWriter, r *http.Request) {
	list, next, err := h.k.ListKafkaVersions(r.Context(), pageFromQuery(r))
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, withNext(map[string]any{"kafkaVersions": rawList(list)}, next))
}

// getCompatibleKafkaVersions handles GET /v1/compatible-kafka-versions.
func (h *Handler) getCompatibleKafkaVersions(w http.ResponseWriter, r *http.Request) {
	list, err := h.k.GetCompatibleKafkaVersions(r.Context(), r.URL.Query().Get("clusterArn"))
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"compatibleKafkaVersions": rawList(list)})
}

// rawList wraps a slice of raw JSON objects so it marshals as a JSON array.
func rawList(items []json.RawMessage) []json.RawMessage {
	if items == nil {
		return []json.RawMessage{}
	}

	return items
}
