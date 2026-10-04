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

// Len implements jsonpath.Array.
func (l *List) Len() int { return len(l.Items) }

// Map is an insertion-ordered, mutable map value (Java's LinkedHashMap).
type Map struct {
	keys []string
	vals map[string]any
}

// NewMap returns an empty map.
func NewMap() *Map { return &Map{vals: map[string]any{}} }

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

// Keys returns the keys in insertion order. It implements jsonpath.Object.
func (m *Map) Keys() []string { return append([]string(nil), m.keys...) }

// Len returns the number of entries.
func (m *Map) Len() int { return len(m.keys) }

// ParseJSON decodes a JSON document into template values, keeping object key
// order and decoding integral numbers as int64. Nesting deeper than
// MaxValueDepth is rejected.
func ParseJSON(s string) (any, error) {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()

	v, err := decodeValue(dec, 0)
	if err != nil {
		return nil, err
	}

	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errorf("unexpected data after JSON value")
	}

	return v, nil
}

func decodeValue(dec *json.Decoder, depth int) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}

	switch t := tok.(type) {
	case json.Delim:
		if depth >= MaxValueDepth {
			return nil, ErrDepthLimit
		}

		if t == '{' {
			return decodeObject(dec, depth+1)
		}

		if t == '[' {
			return decodeArray(dec, depth+1)
		}

		return nil, errorf("unexpected %q in JSON", t)
	case json.Number:
		return jsonNumber(t), nil
	default:
		return t, nil // string, bool or nil
	}
}

func decodeObject(dec *json.Decoder, depth int) (any, error) {
	m := NewMap()

	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}

		key, _ := tok.(string)

		v, err := decodeValue(dec, depth)
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

func decodeArray(dec *json.Decoder, depth int) (any, error) {
	l := NewList()

	for dec.More() {
		v, err := decodeValue(dec, depth)
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

// ToJSON encodes a template value as JSON. Host objects encode as null. A
// value that contains itself, nests deeper than MaxValueDepth or encodes to
// more than MaxOutputBytes is an error.
func ToJSON(v any) (string, error) {
	f := &formatter{w: boundedWriter{limit: MaxOutputBytes}}
	if err := f.json(v, 0); err != nil {
		return "", err
	}

	return f.w.String(), nil
}

// Stringify renders a value the way Velocity prints it: nil as empty, lists
// as [a, b] and maps as {k=v, k2=v2}, as Java's toString does. A list or map
// that contains itself prints as "(this Collection)" or "(this Map)". Output
// past MaxOutputBytes or nesting past MaxValueDepth is cut off; use format
// where that must be an error.
func Stringify(v any) string {
	s, _ := format(v, MaxOutputBytes)

	return s
}

// format renders v like Stringify, failing once the result would pass limit
// bytes or nest past MaxValueDepth.
func format(v any, limit int) (string, error) {
	if s, ok := v.(string); ok {
		if len(s) > limit {
			return "", ErrOutputLimit
		}

		return s, nil
	}

	f := &formatter{w: boundedWriter{limit: limit}}
	err := f.str(v, 0)

	return f.w.String(), err
}

// formatter writes values into a bounded buffer, tracking the lists and maps
// it is inside so a self-reference is detected.
type formatter struct {
	w      boundedWriter
	inside map[any]bool
}

func (f *formatter) enter(v any, depth int) (cyclic bool, err error) {
	if depth >= MaxValueDepth {
		return false, ErrDepthLimit
	}

	if f.inside[v] {
		return true, nil
	}

	if f.inside == nil {
		f.inside = map[any]bool{}
	}

	f.inside[v] = true

	return false, nil
}

func (f *formatter) leave(v any) { delete(f.inside, v) }

func (f *formatter) str(v any, depth int) error {
	switch t := v.(type) {
	case *List:
		return f.strList(t, depth)
	case *Map:
		return f.strMap(t, depth)
	default:
		return f.w.WriteString(scalarString(v))
	}
}

func (f *formatter) strList(l *List, depth int) error {
	cyclic, err := f.enter(l, depth)
	if err != nil {
		return err
	}

	if cyclic {
		return f.w.WriteString("(this Collection)")
	}

	defer f.leave(l)

	if err := f.w.WriteByte('['); err != nil {
		return err
	}

	for i, it := range l.Items {
		if i > 0 {
			if err := f.w.WriteString(", "); err != nil {
				return err
			}
		}

		if err := f.str(it, depth+1); err != nil {
			return err
		}
	}

	return f.w.WriteByte(']')
}

func (f *formatter) strMap(m *Map, depth int) error {
	cyclic, err := f.enter(m, depth)
	if err != nil {
		return err
	}

	if cyclic {
		return f.w.WriteString("(this Map)")
	}

	defer f.leave(m)

	if err := f.w.WriteByte('{'); err != nil {
		return err
	}

	for i, k := range m.keys {
		sep := k + "="
		if i > 0 {
			sep = ", " + sep
		}

		if err := f.w.WriteString(sep); err != nil {
			return err
		}

		if err := f.str(m.vals[k], depth+1); err != nil {
			return err
		}
	}

	return f.w.WriteByte('}')
}

func (f *formatter) json(v any, depth int) error {
	switch t := v.(type) {
	case nil:
		return f.w.WriteString("null")
	case string:
		return f.w.WriteString(jsonString(t))
	case bool, int64, float64:
		return f.w.WriteString(scalarString(t))
	case *List:
		return f.jsonList(t, depth)
	case *Map:
		return f.jsonMap(t, depth)
	default:
		return f.w.WriteString("null")
	}
}

func (f *formatter) jsonList(l *List, depth int) error {
	if err := f.enterJSON(l, depth); err != nil {
		return err
	}

	defer f.leave(l)

	if err := f.w.WriteByte('['); err != nil {
		return err
	}

	for i, it := range l.Items {
		if i > 0 {
			if err := f.w.WriteByte(','); err != nil {
				return err
			}
		}

		if err := f.json(it, depth+1); err != nil {
			return err
		}
	}

	return f.w.WriteByte(']')
}

func (f *formatter) jsonMap(m *Map, depth int) error {
	if err := f.enterJSON(m, depth); err != nil {
		return err
	}

	defer f.leave(m)

	if err := f.w.WriteByte('{'); err != nil {
		return err
	}

	for i, k := range m.keys {
		key := jsonString(k) + ":"
		if i > 0 {
			key = "," + key
		}

		if err := f.w.WriteString(key); err != nil {
			return err
		}

		if err := f.json(m.vals[k], depth+1); err != nil {
			return err
		}
	}

	return f.w.WriteByte('}')
}

func (f *formatter) enterJSON(v any, depth int) error {
	cyclic, err := f.enter(v, depth)
	if err != nil {
		return err
	}

	if cyclic {
		return ErrCyclicValue
	}

	return nil
}

func jsonString(s string) string {
	var b bytes.Buffer

	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)

	return strings.TrimSuffix(b.String(), "\n")
}

// scalarString prints a non-collection value.
func scalarString(v any) string {
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
