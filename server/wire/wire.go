// Package wire provides shared HTTP wire-format helpers for service handlers:
// XML and JSON encoding, JSON decoding, and HTTP-date formatting.
package wire

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"time"
)

// ToHTTPDate converts an ISO8601 timestamp to HTTP-date format (RFC1123).
// The AWS SDK expects Last-Modified as "Mon, 02 Jan 2006 15:04:05 GMT".
func ToHTTPDate(iso string) string {
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return iso
	}

	return t.UTC().Format(http.TimeFormat)
}

// WriteXML writes an XML response with the given status code.
func WriteXML(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	fmt.Fprint(w, xml.Header)
	xml.NewEncoder(w).Encode(v) //nolint:errcheck // best-effort response
}

// DecodeJSON reads a JSON request body into v. Returns false and writes an
// error response if decoding fails.
func DecodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		WriteJSONError(w, http.StatusBadRequest, "SerializationException", "invalid JSON: "+err.Error())
		return false
	}

	return true
}

// WriteJSON writes a JSON response with a 200 status code.
func WriteJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/x-amz-json-1.0")
	json.NewEncoder(w).Encode(v) //nolint:errcheck // best-effort response
}

// WriteJSONError writes a JSON error response with the given status.
func WriteJSONError(w http.ResponseWriter, status int, errType, msg string) {
	w.Header().Set("Content-Type", "application/x-amz-json-1.0")
	w.WriteHeader(status)

	json.NewEncoder(w).Encode(map[string]string{ //nolint:errcheck // best-effort response
		"__type":  errType,
		"Message": msg,
	})
}
