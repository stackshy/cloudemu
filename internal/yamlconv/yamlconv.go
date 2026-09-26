// Package yamlconv converts a YAML document into the same tree encoding/json
// produces with Decoder.UseNumber: map[string]any, []any, string, bool,
// json.Number and nil. Callers that accept both JSON and YAML (CloudFormation
// templates, SSM documents, EKS add-on configuration) can then treat the two
// formats alike.
//
// Custom tags such as !Ref are passed to a TagFunc. Aliases, merge keys,
// duplicate keys and the binary, omap, pairs and set types are rejected.
package yamlconv

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Error is a conversion failure. Line and Column are 1-based. Column is 0
// when the YAML parser reports only a line.
type Error struct {
	Line   int
	Column int
	Msg    string
}

func (e *Error) Error() string {
	switch {
	case e.Line > 0 && e.Column > 0:
		return fmt.Sprintf("line %d, column %d: %s", e.Line, e.Column, e.Msg)
	case e.Line > 0:
		return fmt.Sprintf("line %d: %s", e.Line, e.Msg)
	default:
		return e.Msg
	}
}

// Messages carried by Error for the node-level rejections, so callers can
// tell them apart.
const (
	MsgAlias       = "aliases are not allowed"
	MsgMergeKey    = "merge keys are not allowed"
	MsgDuplicate   = "duplicate mapping key"
	MsgUnknownTag  = "unknown tag"
	MsgComplexKey  = "mapping keys must be scalars"
	MsgUnsupported = "unsupported type"
	MsgMultiDoc    = "only one document is allowed"
)

// TagFunc maps a node carrying a local tag (for example "!Ref") to its value.
// value is the node converted as if it had no tag. A tagged scalar converts
// to its plain string. ok is false for a tag the caller does not know.
type TagFunc func(tag string, value any) (out any, ok bool)

// Decode parses a single YAML document. An empty document yields nil.
func Decode(data []byte, tags TagFunc) (any, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))

	var doc yaml.Node
	if err := dec.Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, nil
		}

		return nil, parseError(data, err)
	}

	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err != nil {
			return nil, parseError(data, err)
		}

		return nil, &Error{Line: extra.Line, Column: extra.Column, Msg: MsgMultiDoc}
	}

	c := converter{tags: tags}

	return c.convert(&doc)
}

var lineRE = regexp.MustCompile(`line (\d+)`)

// parseError turns a yaml.v3 syntax error into an Error. yaml.v3 reports the
// line where the enclosing block starts, so problemLine looks for the line
// where the same error first shows up. The column is not known.
func parseError(data []byte, err error) error {
	e := splitLine(err)

	if line := problemLine(data, e.Msg); line > 0 {
		e.Line = line
	}

	return e
}

func splitLine(err error) *Error {
	msg := strings.TrimPrefix(err.Error(), "yaml: ")
	e := &Error{Msg: msg}

	if m := lineRE.FindStringSubmatch(msg); m != nil {
		e.Line, _ = strconv.Atoi(m[1])
		e.Msg = strings.TrimPrefix(strings.TrimPrefix(msg, m[0]), ": ")
	}

	return e
}

// problemLine returns the smallest n for which the first n lines fail with
// the same message, or 0 when no prefix does.
func problemLine(data []byte, msg string) int {
	lines := bytes.SplitAfter(data, []byte("\n"))

	var prefix []byte

	for i, l := range lines {
		prefix = append(prefix, l...)

		var n yaml.Node
		if err := yaml.Unmarshal(prefix, &n); err != nil && splitLine(err).Msg == msg {
			return i + 1
		}
	}

	return 0
}

type converter struct {
	tags TagFunc
}

func nodeErr(n *yaml.Node, msg string) error {
	return &Error{Line: n.Line, Column: n.Column, Msg: msg}
}

func (c converter) convert(n *yaml.Node) (any, error) {
	if isLocalTag(n.Tag) && n.Kind != yaml.AliasNode {
		return c.tagged(n)
	}

	return c.untagged(n)
}

// untagged converts a node by its kind, ignoring any local tag.
func (c converter) untagged(n *yaml.Node) (any, error) {
	switch n.Kind {
	case yaml.DocumentNode:
		if len(n.Content) == 0 {
			return nil, nil
		}

		return c.convert(n.Content[0])
	case yaml.AliasNode:
		return nil, nodeErr(n, MsgAlias)
	case yaml.MappingNode:
		return c.mapping(n)
	case yaml.SequenceNode:
		return c.sequence(n)
	case yaml.ScalarNode:
		return scalar(n)
	default:
		return nil, nodeErr(n, MsgUnsupported)
	}
}

// isLocalTag reports a tag outside the YAML core schema, such as "!Ref".
func isLocalTag(tag string) bool {
	return strings.HasPrefix(tag, "!") && !strings.HasPrefix(tag, "!!")
}

func (c converter) tagged(n *yaml.Node) (any, error) {
	if c.tags == nil {
		return nil, nodeErr(n, MsgUnknownTag+" "+n.Tag)
	}

	var (
		value any
		err   error
	)

	if n.Kind == yaml.ScalarNode {
		value = n.Value
	} else {
		value, err = c.untagged(n)
	}

	if err != nil {
		return nil, err
	}

	out, ok := c.tags(n.Tag, value)
	if !ok {
		return nil, nodeErr(n, MsgUnknownTag+" "+n.Tag)
	}

	return out, nil
}

func (c converter) mapping(n *yaml.Node) (any, error) {
	if n.Tag != "" && n.ShortTag() != "!!map" && !isLocalTag(n.Tag) {
		return nil, nodeErr(n, MsgUnsupported+" "+n.ShortTag())
	}

	// Content alternates key and value.
	pairs := len(n.Content) / 2
	out := make(map[string]any, pairs)
	seen := make(map[string]bool, pairs)

	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]

		key, err := mapKey(k)
		if err != nil {
			return nil, err
		}

		if seen[key] {
			return nil, nodeErr(k, MsgDuplicate+" "+strconv.Quote(key))
		}

		seen[key] = true

		val, err := c.convert(v)
		if err != nil {
			return nil, err
		}

		out[key] = val
	}

	return out, nil
}

func mapKey(k *yaml.Node) (string, error) {
	switch {
	case k.Kind == yaml.AliasNode:
		return "", nodeErr(k, MsgAlias)
	case k.Kind != yaml.ScalarNode:
		return "", nodeErr(k, MsgComplexKey)
	case k.ShortTag() == "!!merge":
		return "", nodeErr(k, MsgMergeKey)
	case isLocalTag(k.Tag):
		return "", nodeErr(k, MsgUnknownTag+" "+k.Tag)
	}

	return k.Value, nil
}

func (c converter) sequence(n *yaml.Node) (any, error) {
	if n.Tag != "" && n.ShortTag() != "!!seq" && !isLocalTag(n.Tag) {
		return nil, nodeErr(n, MsgUnsupported+" "+n.ShortTag())
	}

	out := make([]any, 0, len(n.Content))

	for _, e := range n.Content {
		v, err := c.convert(e)
		if err != nil {
			return nil, err
		}

		out = append(out, v)
	}

	return out, nil
}

// jsonNumberRE matches the number grammar encoding/json accepts.
var jsonNumberRE = regexp.MustCompile(`^-?(0|[1-9]\d*)(\.\d+)?([eE][+-]?\d+)?$`)

func scalar(n *yaml.Node) (any, error) {
	switch n.ShortTag() {
	case "!!null":
		return nil, nil
	case "!!bool":
		b, err := strconv.ParseBool(strings.ToLower(n.Value))
		if err != nil {
			return nil, nodeErr(n, "invalid bool "+strconv.Quote(n.Value))
		}

		return b, nil
	case "!!int", "!!float":
		return number(n), nil
	case "!!str", "!!timestamp":
		return n.Value, nil
	default:
		return nil, nodeErr(n, MsgUnsupported+" "+n.ShortTag())
	}
}

// number keeps a JSON-compatible literal as written. Other YAML forms such as
// 0x1F, 1_000 or +5 are normalized. Infinity and NaN stay strings because JSON
// cannot hold them.
func number(n *yaml.Node) any {
	if jsonNumberRE.MatchString(n.Value) {
		return json.Number(n.Value)
	}

	var v any
	if err := n.Decode(&v); err != nil {
		return n.Value
	}

	switch t := v.(type) {
	case int:
		return json.Number(strconv.Itoa(t))
	case int64:
		return json.Number(strconv.FormatInt(t, 10))
	case uint64:
		return json.Number(strconv.FormatUint(t, 10))
	case float64:
		s := strconv.FormatFloat(t, 'g', -1, 64)
		if jsonNumberRE.MatchString(s) {
			return json.Number(s)
		}
	}

	return n.Value
}
