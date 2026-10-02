// Package gcpenum rewrites numeric proto enums in GCP REST request bodies to
// their value names.
//
// The cloud.google.com/go gapic REST clients marshal requests with
// protojson{UseEnumNumbers: true} and add $alt=json;enum-encoding=int, so every
// enum field arrives as a JSON number. Terraform, gcloud and the discovery
// google.golang.org/api clients send the value name. Handlers model enums as
// strings, so a body pre-pass that turns known numbers into names lets both
// kinds of client share one decode path. String bodies are returned
// byte-identical.
//
// Tables are generated from the proto descriptors by internal/gcpenumgen and
// keyed by JSON path, so a number is rewritten only where the proto declares an
// enum, never by field name alone.
package gcpenum

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
)

// Enum is one proto enum: its full name, used in error text, and the number to
// value name map, the same shape as a generated pb X_name map.
type Enum struct {
	Type  string
	Names map[int32]string
}

// Fields maps a dot-separated path of lowerCamel proto json_names to the enum
// at that path. Arrays are transparent, so "stateMessages.severity" matches
// every element. A "*" segment matches any map key, as in
// "cleanupPolicies.*.action".
type Fields map[string]Enum

// wildcard is the path segment that matches any map key.
const wildcard = "*"

// UnknownValueError reports a JSON number at an enum path that does not name a
// value of that enum: a number missing from the table, a non-integral number or
// one outside int32.
type UnknownValueError struct {
	Path  string
	Type  string
	Value string
}

func (e *UnknownValueError) Error() string {
	return fmt.Sprintf("invalid enum value %s for field %q (%s)", e.Value, e.Path, e.Type)
}

// Normalize rewrites every JSON number at an enum path in raw to its value
// name. A number that is integral and fits int32 maps through Names, so 1, 1e0
// and -0 resolve as 1, 1 and 0. Any other number at an enum path is an
// *UnknownValueError. Strings, nulls and other values are left for the
// service's own validation.
//
// When nothing is rewritten raw itself is returned. A body that is not a JSON
// object is also returned unchanged, so the caller's decode reports the error.
// raw is never written to.
func Normalize(raw []byte, f Fields) ([]byte, error) {
	return normalize(raw, f, true)
}

// NormalizeStored is the lenient variant for blobs already in the store. It
// rewrites known numbers and leaves unknown, non-integral or out-of-range
// numbers and malformed JSON as they are, so one bad stored value never fails
// a read. raw is never written to.
func NormalizeStored(raw []byte, f Fields) []byte {
	out, err := normalize(raw, f, false)
	if err != nil {
		return raw
	}

	return out
}

// Sub returns the entries of f under prefix with "prefix." stripped. It is the
// table for a stored sub-blob such as one field of a resource. It returns nil
// when no entry lies under prefix.
func Sub(f Fields, prefix string) Fields {
	var out Fields

	lead := prefix + "."

	for path, e := range f {
		rest, ok := strings.CutPrefix(path, lead)
		if !ok {
			continue
		}

		if out == nil {
			out = Fields{}
		}

		out[rest] = e
	}

	return out
}

// Name resolves one enum token, a JSON string or number, to a value name of e.
// A string must be one of e's names; a number must be integral, fit int32 and
// be in e.Names. Anything else reports false.
func Name(raw json.RawMessage, e Enum) (string, bool) {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		for _, name := range e.Names {
			if name == s {
				return s, true
			}
		}

		return "", false
	}

	var n json.Number
	if err := json.Unmarshal(raw, &n); err != nil {
		return "", false
	}

	num, ok := enumNumber(n)
	if !ok {
		return "", false
	}

	name, ok := e.Names[num]

	return name, ok
}

// ReadBody reads at most gcprest.MaxBodyBytes of the request body and runs
// Normalize over it. On a read error or an unknown enum value it writes a 400
// INVALID_ARGUMENT and returns ok=false.
func ReadBody(w http.ResponseWriter, r *http.Request, f Fields) (body []byte, ok bool) {
	r.Body = http.MaxBytesReader(w, r.Body, gcprest.MaxBodyBytes)

	raw, err := io.ReadAll(r.Body)
	if err != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", err.Error())
		return nil, false
	}

	out, err := Normalize(raw, f)
	if err != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", err.Error())
		return nil, false
	}

	return out, true
}

// DecodeJSON is a drop-in for gcprest.DecodeJSON that normalizes enums first.
// It decodes with a json.Decoder, as gcprest.DecodeJSON does, so trailing bytes
// are tolerated and an empty body is the same EOF 400.
func DecodeJSON(w http.ResponseWriter, r *http.Request, v any, f Fields) bool {
	body, ok := ReadBody(w, r, f)
	if !ok {
		return false
	}

	if err := json.NewDecoder(bytes.NewReader(body)).Decode(v); err != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", err.Error())
		return false
	}

	return true
}

// walker carries one rewrite pass: the table, the set of every path prefix that
// leads to an enum, whether unknown numbers are errors, and whether anything
// was rewritten.
type walker struct {
	fields   Fields
	prefixes map[string]bool
	strict   bool
	changed  bool
}

func normalize(raw []byte, f Fields, strict bool) ([]byte, error) {
	if len(f) == 0 || !bytes.ContainsAny(raw, "0123456789") {
		return raw, nil
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()

	var root any
	if err := dec.Decode(&root); err != nil {
		return raw, nil
	}

	if _, isObject := root.(map[string]any); !isObject {
		return raw, nil
	}

	wk := &walker{fields: f, prefixes: prefixesOf(f), strict: strict}

	out, err := wk.walk(root, "")
	if err != nil {
		return nil, err
	}

	if !wk.changed {
		return raw, nil
	}

	return encode(out, raw), nil
}

// prefixesOf returns every proper and full path prefix of the table keys, so
// the walk can skip subtrees that hold no enum.
func prefixesOf(f Fields) map[string]bool {
	out := make(map[string]bool, len(f))

	for path := range f {
		for i := range len(path) {
			if path[i] == '.' {
				out[path[:i]] = true
			}
		}

		out[path] = true
	}

	return out
}

// encode re-encodes a rewritten body without HTML escaping. A value decoded
// from JSON always re-encodes; raw is kept if it somehow does not.
func encode(v any, raw []byte) []byte {
	var buf bytes.Buffer

	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)

	if err := enc.Encode(v); err != nil {
		return raw
	}

	return bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
}

func join(path, seg string) string {
	if path == "" {
		return seg
	}

	return path + "." + seg
}

// walk returns v with the enums under path rewritten. Arrays are transparent:
// each element is walked at the array's own path.
func (wk *walker) walk(v any, path string) (any, error) {
	switch t := v.(type) {
	case map[string]any:
		return wk.walkObject(t, path)
	case []any:
		return wk.walkArray(t, path)
	case json.Number:
		if e, ok := wk.fields[path]; ok {
			return wk.rewriteScalar(t, path, e)
		}
	}

	return v, nil
}

func (wk *walker) walkObject(m map[string]any, path string) (any, error) {
	for k, child := range m {
		next := wk.childPath(path, k)
		if next == "" {
			continue
		}

		out, err := wk.walk(child, next)
		if err != nil {
			return nil, err
		}

		m[k] = out
	}

	return m, nil
}

// childPath resolves key k under path to a table path: the literal field name
// when the table has it, else the "*" map-key segment, else "" to skip.
func (wk *walker) childPath(path, k string) string {
	if p := join(path, k); wk.prefixes[p] {
		return p
	}

	if p := join(path, wildcard); wk.prefixes[p] {
		return p
	}

	return ""
}

func (wk *walker) walkArray(a []any, path string) (any, error) {
	for i, elem := range a {
		out, err := wk.walk(elem, path)
		if err != nil {
			return nil, err
		}

		a[i] = out
	}

	return a, nil
}

// rewriteScalar maps one number to its value name. In lenient mode a number it
// cannot map is kept as it is.
func (wk *walker) rewriteScalar(n json.Number, path string, e Enum) (any, error) {
	if num, ok := enumNumber(n); ok {
		if name, known := e.Names[num]; known {
			wk.changed = true
			return name, nil
		}
	}

	if !wk.strict {
		return n, nil
	}

	return nil, &UnknownValueError{Path: path, Type: e.Type, Value: n.String()}
}

// enumNumber parses n as an enum number: integral and within int32.
func enumNumber(n json.Number) (int32, bool) {
	if i, err := strconv.ParseInt(n.String(), 10, 32); err == nil {
		return int32(i), true
	}

	fl, err := strconv.ParseFloat(n.String(), 64)
	if err != nil || fl != math.Trunc(fl) || fl < math.MinInt32 || fl > math.MaxInt32 {
		return 0, false
	}

	return int32(fl), true
}
