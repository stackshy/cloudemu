package server

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"time"
)

// toHTTPDate converts an ISO8601 timestamp to HTTP-date format (RFC1123).
// The AWS SDK expects Last-Modified as "Mon, 02 Jan 2006 15:04:05 GMT".
func toHTTPDate(iso string) string {
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return iso
	}

	return t.UTC().Format(http.TimeFormat)
}

// writeXML writes an XML response with the given status code.
func writeXML(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	fmt.Fprint(w, xml.Header)
	xml.NewEncoder(w).Encode(v) //nolint:errcheck // best-effort response
}

// decodeJSON reads a JSON request body into v. Returns false and writes
// an error response if decoding fails.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeJSONError(w, http.StatusBadRequest, "SerializationException", "invalid JSON: "+err.Error())
		return false
	}

	return true
}

// writeJSON writes a JSON response with a 200 status code.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/x-amz-json-1.0")
	json.NewEncoder(w).Encode(v) //nolint:errcheck // best-effort response
}

// writeJSONError writes a JSON error response with the given status.
func writeJSONError(w http.ResponseWriter, status int, errType, msg string) {
	w.Header().Set("Content-Type", "application/x-amz-json-1.0")
	w.WriteHeader(status)

	json.NewEncoder(w).Encode(map[string]string{ //nolint:errcheck // best-effort response
		"__type":  errType,
		"Message": msg,
	})
}
