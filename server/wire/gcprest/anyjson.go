package gcprest

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"
)

// TypedAny renders v as a google.protobuf.Any in proto3 JSON: v's own JSON
// object with the "@type" discriminator added. It is the shape a done
// google.longrunning.Operation carries in `response` and `metadata`; a wrong or
// missing type URL makes a GAPIC or Terraform LRO wait fail, so callers pass the
// exact proto type (type.googleapis.com/google.cloud.<svc>.v1.<Message>). v must
// marshal to a JSON object (an empty struct yields {"@type": …}).
func TypedAny(v any, typeURL string) (json.RawMessage, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}

	var fields map[string]json.RawMessage
	if uErr := json.Unmarshal(raw, &fields); uErr != nil {
		return nil, uErr
	}

	if fields == nil {
		fields = map[string]json.RawMessage{}
	}

	typ, err := json.Marshal(typeURL)
	if err != nil {
		return nil, err
	}

	fields["@type"] = typ

	return json.Marshal(fields)
}

// FormatTime renders t as a proto3-JSON google.protobuf.Timestamp (RFC 3339,
// UTC, nanosecond precision); a zero time renders as "" so an omitempty field
// drops it.
func FormatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}

	return t.UTC().Format(time.RFC3339Nano)
}

// DecodeOptionalJSON is DecodeJSON for a request whose body the API allows to
// be empty: an empty body leaves v at its zero value instead of failing. A
// present but malformed body is still 400 INVALID_ARGUMENT.
func DecodeOptionalJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)

	if err := json.NewDecoder(r.Body).Decode(v); err != nil && !errors.Is(err, io.EOF) {
		WriteError(w, http.StatusBadRequest, "invalid", "malformed JSON body: "+err.Error())
		return false
	}

	return true
}
