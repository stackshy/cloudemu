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

// WriteJSONErrorQueryCompat writes a JSON error response like WriteJSONError,
// plus the X-Amzn-Query-Error header that AWS services migrated from the
// legacy Query protocol to AwsJson (the "awsQueryCompatible" Smithy trait)
// emit on every error response. Real aws-sdk-go-v2 exception types read this
// header to override their ErrorCode() back to the original Query-protocol
// code (e.g. "AWS.SimpleQueueService.NonExistentQueue" instead of the JSON
// shape name "QueueDoesNotExist"); tools that still match on the legacy code
// — including terraform-provider-aws's SQS delete/create waiters — rely on
// it, so omitting it leaves those SDK code paths unable to recognize the
// error at all. queryCode must be "<legacy code>;Sender" or
// "<legacy code>;Receiver", matching the header's documented format.
func WriteJSONErrorQueryCompat(w http.ResponseWriter, status int, errType, queryCode, msg string) {
	w.Header().Set("Content-Type", "application/x-amz-json-1.0")
	w.Header().Set("x-amzn-query-error", queryCode)
	w.WriteHeader(status)

	json.NewEncoder(w).Encode(map[string]string{ //nolint:errcheck // best-effort response
		"__type":  errType,
		"Message": msg,
	})
}

// WriteJSONErrorFields writes a JSON error response carrying additional
// top-level members alongside __type and Message. DynamoDB uses this for
// ConditionalCheckFailedException, which returns the conflicting item in an
// Item member when ReturnValuesOnConditionCheckFailure=ALL_OLD.
func WriteJSONErrorFields(w http.ResponseWriter, status int, errType, msg string, extra map[string]any) {
	w.Header().Set("Content-Type", "application/x-amz-json-1.0")
	w.WriteHeader(status)

	body := map[string]any{
		"__type":  errType,
		"Message": msg,
	}
	for k, v := range extra {
		body[k] = v
	}

	json.NewEncoder(w).Encode(body) //nolint:errcheck // best-effort response
}
