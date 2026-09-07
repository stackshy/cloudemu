package location

import (
	"net/http"
	"time"
)

// timeFormat is the restJson1 date-time format Location uses for CreateTime and
// UpdateTime; the SDK decodes it with smithytime.ParseDateTime.
const timeFormat = time.RFC3339Nano

// rfc3339 renders a timestamp in the Location date-time format, or nil for the
// zero time so an unset timestamp reads back as null.
func rfc3339(t time.Time) any {
	if t.IsZero() {
		return nil
	}

	return t.UTC().Format(timeFormat)
}

// putNonEmpty sets key to val only when val is non-empty.
func putNonEmpty(m map[string]any, key, val string) {
	if val != "" {
		m[key] = val
	}
}

// putTags sets the Tags key only when the resource carries tags, so an untagged
// resource reads back with Tags null (matching the real service).
func putTags(m map[string]any, tags map[string]string) {
	if len(tags) > 0 {
		m["Tags"] = tags
	}
}

// writeList writes a list response: an Entries array plus an optional
// NextToken. The Entries key is always present (an empty page reads as []).
func writeList(w http.ResponseWriter, entries []map[string]any, next string) {
	body := map[string]any{"Entries": entries}
	if next != "" {
		body["NextToken"] = next
	}

	writeJSON(w, body)
}
