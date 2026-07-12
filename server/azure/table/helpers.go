package table

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	cerrors "github.com/stackshy/cloudemu/errors"
	driver "github.com/stackshy/cloudemu/tablestorage/driver"
)

// tableNameFromDelete extracts "name" from a "Tables('name')" path.
func tableNameFromDelete(path string) string {
	open := strings.IndexByte(path, '(')
	closeIdx := strings.LastIndexByte(path, ')')

	if open < 0 || closeIdx < 0 || closeIdx <= open {
		return ""
	}

	inner := path[open+1 : closeIdx]

	return strings.Trim(inner, "'")
}

// splitEntityPath splits "table(predicate)" into ("table", "predicate", true).
// A bare "table" with no parentheses returns ok=false.
func splitEntityPath(path string) (table, predicate string, ok bool) {
	open := strings.IndexByte(path, '(')
	if open < 0 {
		return "", "", false
	}

	closeIdx := strings.LastIndexByte(path, ')')
	if closeIdx < 0 || closeIdx <= open {
		return "", "", false
	}

	return path[:open], path[open+1 : closeIdx], true
}

// parseKeyPredicate parses "PartitionKey='p',RowKey='r'" into ("p", "r").
// The two clauses may appear in either order.
func parseKeyPredicate(predicate string) (partitionKey, rowKey string, ok bool) {
	for _, clause := range splitTopLevel(predicate) {
		key, val, found := strings.Cut(clause, "=")
		if !found {
			continue
		}

		key = strings.TrimSpace(key)
		val = unquote(strings.TrimSpace(val))

		switch key {
		case "PartitionKey":
			partitionKey = val
		case "RowKey":
			rowKey = val
		}
	}

	if partitionKey == "" && rowKey == "" {
		return "", "", false
	}

	return partitionKey, rowKey, true
}

// splitTopLevel splits on commas that are not inside single-quoted strings, so
// a key value containing a comma survives.
func splitTopLevel(s string) []string {
	var (
		parts   []string
		start   int
		inQuote bool
	)

	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\'':
			inQuote = !inQuote
		case ',':
			if !inQuote {
				parts = append(parts, s[start:i])
				start = i + 1
			}
		}
	}

	parts = append(parts, s[start:])

	return parts
}

// unquote strips surrounding single quotes and unescapes doubled quotes, which
// is how OData escapes a literal apostrophe.
func unquote(s string) string {
	if len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\'' {
		s = s[1 : len(s)-1]
	}

	return strings.ReplaceAll(s, "''", "'")
}

// entityToJSON copies an entity's properties into a fresh JSON map. The
// PartitionKey/RowKey and user properties round-trip verbatim.
func entityToJSON(e driver.Entity) map[string]any {
	out := make(map[string]any, len(e)+1)
	for k, v := range e {
		out[k] = v
	}

	return out
}

func entityETag() string {
	return fmt.Sprintf("W/\"datetime'%s'\"", time.Now().UTC().Format(time.RFC3339Nano))
}

func scheme(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}

	return "http"
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) error {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		return err
	}

	return json.Unmarshal(data, v)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", contentTypeJSON+";odata=minimalmetadata;streaming=true;charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// tableError is the Azure Table Storage OData JSON error envelope.
type tableError struct {
	ODataError struct {
		Code    string `json:"code"`
		Message struct {
			Lang  string `json:"lang"`
			Value string `json:"value"`
		} `json:"message"`
	} `json:"odata.error"`
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	var e tableError
	e.ODataError.Code = code
	e.ODataError.Message.Lang = "en-US"
	e.ODataError.Message.Value = msg

	w.Header().Set("Content-Type", contentTypeJSON)
	w.Header().Set("X-Ms-Error-Code", code)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(e)
}

// writeErr maps CloudEmu canonical errors to Azure Table HTTP errors.
func writeErr(w http.ResponseWriter, err error) {
	switch {
	case cerrors.IsNotFound(err):
		writeError(w, http.StatusNotFound, "ResourceNotFound", err.Error())
	case cerrors.IsAlreadyExists(err):
		writeError(w, http.StatusConflict, "EntityAlreadyExists", err.Error())
	case cerrors.IsInvalidArgument(err):
		writeError(w, http.StatusBadRequest, "InvalidInput", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "InternalError", err.Error())
	}
}
