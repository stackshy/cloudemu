package tablestorage

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	driver "github.com/stackshy/cloudemu/v2/services/tablestorage/driver"
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

// parseKeyPredicate parses "PartitionKey='p',RowKey='r'" into ("p", "r"). The
// two clauses may appear in either order, but a single-entity key predicate
// must name BOTH keys: a malformed clause, an unknown key, or a missing key
// makes it invalid (ok=false → 400 InvalidInput) rather than a partial key that
// then reports the entity as not-found (404).
func parseKeyPredicate(predicate string) (partitionKey, rowKey string, ok bool) {
	var havePK, haveRK bool

	for _, clause := range splitTopLevel(predicate) {
		key, val, found := strings.Cut(clause, "=")
		if !found {
			return "", "", false
		}

		key = strings.TrimSpace(key)
		val = unquote(strings.TrimSpace(val))

		switch key {
		case "PartitionKey":
			partitionKey, havePK = val, true
		case "RowKey":
			rowKey, haveRK = val, true
		default:
			return "", "", false
		}
	}

	if !havePK || !haveRK {
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
// PartitionKey/RowKey and user properties round-trip verbatim, except an
// Edm.Int64 (stored as a native int64) is rendered as a JSON string, which is
// how Table Storage encodes 64-bit integers on the wire.
func entityToJSON(e driver.Entity) map[string]any {
	out := make(map[string]any, len(e)+1)
	for k, v := range e {
		if n, ok := v.(int64); ok {
			out[k] = strconv.FormatInt(n, 10)
			continue
		}

		out[k] = v
	}

	return out
}

// atoiDefault parses s as an int, returning def when s is empty or invalid.
func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}

	if n, err := strconv.Atoi(s); err == nil {
		return n
	}

	return def
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
	status, code := mapErr(err)
	writeError(w, status, code, err.Error())
}

// mapErr maps a CloudEmu canonical error to its Azure Table HTTP status + code.
func mapErr(err error) (status int, code string) {
	switch {
	case errors.Is(err, driver.ErrTableNotFound):
		return http.StatusNotFound, "TableNotFound"
	case cerrors.IsNotFound(err):
		return http.StatusNotFound, "EntityNotFound"
	case cerrors.IsAlreadyExists(err):
		return http.StatusConflict, "EntityAlreadyExists"
	case cerrors.IsFailedPrecondition(err):
		return http.StatusPreconditionFailed, "UpdateConditionNotSatisfied"
	case cerrors.IsInvalidArgument(err):
		return http.StatusBadRequest, "InvalidInput"
	default:
		return http.StatusInternalServerError, "InternalError"
	}
}
