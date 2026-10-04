package vtl

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Template values are nil, bool, int64, float64, string, *List, *Map or a host
// Object. Lists and maps are references, so a method such as add or put
// changes the value every variable holding it sees, as in Velocity.

// Object is a host value a template can read properties from and call methods
// on, such as API Gateway's $input or $util.
type Object interface {
	// Get returns the named property.
	Get(name string) (any, bool)
	// Call invokes the named method. ok is false when the object has no such
	// method.
	Call(name string, args []any) (result any, ok bool, err error)
}

// List is an ordered, mutable list value.
type List struct {
	Items []any
}

// NewList returns a list holding items.
func NewList(items ...any) *List { return &List{Items: items} }

// Index implements jsonpath.Array.
func (l *List) Index(i int) (any, bool) {
	if i < 0 || i >= len(l.Items) {
		return nil, false
	}

	return l.Items[i], true
}

// Map is an insertion-ordered, mutable map value (Java's LinkedHashMap).
type Map struct {
	keys []string
	vals map[string]any
}

// NewMap returns an empty map.
func NewMap() *Map { return &Map{vals: map[string]any{}} }

// MapOf builds a map from alternating key/value arguments.
func MapOf(kv ...any) *Map {
	m := NewMap()

	for i := 0; i+1 < len(kv); i += 2 {
		m.Put(fmt.Sprint(kv[i]), kv[i+1])
	}

	return m
}

// StringMap converts a Go string map to a Map with keys in sorted order.
func StringMap(in map[string]string) *Map {
	m := NewMap()

	for _, k := range sortedKeys(in) {
		m.Put(k, in[k])
	}

	return m
}

// Lookup implements jsonpath.Object.
func (m *Map) Lookup(key string) (any, bool) { return m.Get(key) }

// Get returns the value stored under key.
func (m *Map) Get(key string) (any, bool) {
	v, ok := m.vals[key]

	return v, ok
}

// Put stores v under key and returns the previous value.
func (m *Map) Put(key string, v any) any {
	prev, ok := m.vals[key]
	if !ok {
		m.keys = append(m.keys, key)
	}

	m.vals[key] = v

	return prev
}

// Remove deletes key and returns its previous value.
func (m *Map) Remove(key string) any {
	prev, ok := m.vals[key]
	if !ok {
		return nil
	}

	delete(m.vals, key)

	for i, k := range m.keys {
		if k == key {
			m.keys = append(m.keys[:i], m.keys[i+1:]...)

			break
		}
	}

	return prev
}

// Keys returns the keys in insertion order.
func (m *Map) Keys() []string { return append([]string(nil), m.keys...) }

// Len returns the number of entries.
func (m *Map) Len() int { return len(m.keys) }

// ParseJSON decodes a JSON document into template values, keeping object key
// order and decoding integral numbers as int64.
func ParseJSON(s string) (any, error) {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()

	v, err := decodeValue(dec)
	if err != nil {
		return nil, err
	}

	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errorf("unexpected data after JSON value")
	}

	return v, nil
}

func decodeValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}

	switch t := tok.(type) {
	case json.Delim:
		if t == '{' {
			return decodeObject(dec)
		}

		if t == '[' {
			return decodeArray(dec)
		}

		return nil, errorf("unexpected %q in JSON", t)
	case json.Number:
		return jsonNumber(t), nil
	default:
		return t, nil // string, bool or nil
	}
}

func decodeObject(dec *json.Decoder) (any, error) {
	m := NewMap()

	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}

		key, _ := tok.(string)

		v, err := decodeValue(dec)
		if err != nil {
			return nil, err
		}

		m.Put(key, v)
	}

	if _, err := dec.Token(); err != nil {
		return nil, err
	}

	return m, nil
}

func decodeArray(dec *json.Decoder) (any, error) {
	l := NewList()

	for dec.More() {
		v, err := decodeValue(dec)
		if err != nil {
			return nil, err
		}

		l.Items = append(l.Items, v)
	}

	if _, err := dec.Token(); err != nil {
		return nil, err
	}

	return l, nil
}

func jsonNumber(n json.Number) any {
	if i, err := strconv.ParseInt(string(n), 10, 64); err == nil {
		return i
	}

	f, _ := strconv.ParseFloat(string(n), 64)

	return f
}

// ToJSON encodes a template value as JSON. Host objects encode as null.
func ToJSON(v any) string {
	var b bytes.Buffer

	writeJSON(&b, v)

	return b.String()
}

func writeJSON(b *bytes.Buffer, v any) {
	switch t := v.(type) {
	case nil:
		b.WriteString("null")
	case string:
		writeJSONString(b, t)
	case bool, int64, float64:
		b.WriteString(Stringify(t))
	case *List:
		b.WriteByte('[')

		for i, it := range t.Items {
			if i > 0 {
				b.WriteByte(',')
			}

			writeJSON(b, it)
		}

		b.WriteByte(']')
	case *Map:
		b.WriteByte('{')

		for i, k := range t.keys {
			if i > 0 {
				b.WriteByte(',')
			}

			writeJSONString(b, k)
			b.WriteByte(':')
			writeJSON(b, t.vals[k])
		}

		b.WriteByte('}')
	default:
		b.WriteString("null")
	}
}

func writeJSONString(b *bytes.Buffer, s string) {
	enc := json.NewEncoder(b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)

	b.Truncate(b.Len() - 1) // drop the encoder's trailing newline
}

// Stringify renders a value the way Velocity prints it: nil as empty, lists
// as [a, b] and maps as {k=v, k2=v2}, as Java's toString does.
func Stringify(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		return formatFloat(t)
	default:
		return stringifyComposite(v)
	}
}

// stringifyComposite renders lists, maps and Stringers.
func stringifyComposite(v any) string {
	switch t := v.(type) {
	case *List:
		parts := make([]string, len(t.Items))
		for i, it := range t.Items {
			parts[i] = Stringify(it)
		}

		return "[" + strings.Join(parts, ", ") + "]"
	case *Map:
		parts := make([]string, len(t.keys))
		for i, k := range t.keys {
			parts[i] = k + "=" + Stringify(t.vals[k])
		}

		return "{" + strings.Join(parts, ", ") + "}"
	case fmt.Stringer:
		return t.String()
	default:
		return ""
	}
}

// formatFloat prints a double the way Java's Double.toString does for the
// common range: integral values keep a trailing ".0".
func formatFloat(f float64) string {
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return strconv.FormatFloat(f, 'g', -1, 64)
	}

	s := strconv.FormatFloat(f, 'f', -1, 64)
	if !strings.Contains(s, ".") {
		s += ".0"
	}

	return s
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	return keys
}
