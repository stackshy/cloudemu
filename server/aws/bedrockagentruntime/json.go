package bedrockagentruntime

import (
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"slices"
)

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "ValidationException", "invalid JSON: "+err.Error())

		return false
	}

	return true
}

// decodeJSONAllowEmpty decodes a JSON body but tolerates an empty body, for
// operations whose body members are all optional.
func decodeJSONAllowEmpty(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	if err := json.NewDecoder(r.Body).Decode(v); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "ValidationException", "invalid JSON: "+err.Error())

		return false
	}

	return true
}

func writeJSON(w http.ResponseWriter, v any) {
	writeJSONStatus(w, http.StatusOK, v)
}

// writeJSONStatus writes v with the success status the service model gives
// the operation.
func writeJSONStatus(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// sortedKeys returns m's keys in order.
func sortedKeys[V any](m map[string]V) []string {
	return slices.Sorted(maps.Keys(m))
}
