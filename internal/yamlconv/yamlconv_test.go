package yamlconv_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/internal/yamlconv"
)

func TestDecodeScalars(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want any
	}{
		{"int", "v: 42", json.Number("42")},
		{"negative", "v: -7", json.Number("-7")},
		{"float", "v: 1.50", json.Number("1.50")},
		{"exponent", "v: 1e3", json.Number("1e3")},
		{"hex", "v: 0x1F", json.Number("31")},
		{"underscore", "v: 1_000", json.Number("1000")},
		{"plus sign", "v: +5", json.Number("5")},
		{"infinity stays string", "v: .inf", ".inf"},
		{"true", "v: true", true},
		{"False", "v: False", false},
		{"yes stays string", "v: yes", "yes"},
		{"off stays string", "v: off", "off"},
		{"null", "v: null", nil},
		{"tilde", "v: ~", nil},
		{"empty", "v:", nil},
		{"timestamp stays literal", "v: 2010-09-09", "2010-09-09"},
		{"quoted number is string", `v: "42"`, "42"},
		{"explicit str tag", "v: !!str 42", "42"},
		{"plain string", "v: hello world", "hello world"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := yamlconv.Decode([]byte(tc.in), nil)
			require.NoError(t, err)
			assert.Equal(t, map[string]any{"v": tc.want}, got)
		})
	}
}

func TestDecodeMatchesJSONTree(t *testing.T) {
	t.Parallel()

	y := `
name: demo
count: 3
ratio: 0.5
enabled: true
tags: [a, b]
nested:
  list:
    - k: v
    - null
`
	j := `{"name":"demo","count":3,"ratio":0.5,"enabled":true,"tags":["a","b"],
		"nested":{"list":[{"k":"v"},null]}}`

	got, err := yamlconv.Decode([]byte(y), nil)
	require.NoError(t, err)

	dec := json.NewDecoder(strings.NewReader(j))
	dec.UseNumber()

	var want any
	require.NoError(t, dec.Decode(&want))
	assert.Equal(t, want, got)
}

func TestDecodeTags(t *testing.T) {
	t.Parallel()

	hook := func(tag string, v any) (any, bool) {
		if tag != "!Wrap" {
			return nil, false
		}

		return map[string]any{"wrapped": v}, true
	}

	got, err := yamlconv.Decode([]byte("a: !Wrap x\nb: !Wrap [1, !Wrap y]\nc: !Wrap {k: v}"), hook)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{
		"a": map[string]any{"wrapped": "x"},
		"b": map[string]any{"wrapped": []any{json.Number("1"), map[string]any{"wrapped": "y"}}},
		"c": map[string]any{"wrapped": map[string]any{"k": "v"}},
	}, got)

	// A tagged scalar keeps its text, it is not resolved as a number.
	got, err = yamlconv.Decode([]byte("a: !Wrap 10"), hook)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"a": map[string]any{"wrapped": "10"}}, got)
}

func TestDecodeErrors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		in       string
		msg      string
		line     int
		column   int
		withHook bool
	}{
		{"alias", "a: &x 1\nb: *x", yamlconv.MsgAlias, 2, 4, false},
		{"merge key", "a: &x {k: 1}\nb:\n  <<: {k: 2}", yamlconv.MsgMergeKey, 3, 3, false},
		{"duplicate key", "a: 1\na: 2", yamlconv.MsgDuplicate + ` "a"`, 2, 1, false},
		{"unknown tag without hook", "a: !Ref x", yamlconv.MsgUnknownTag + " !Ref", 1, 4, false},
		{"unknown tag with hook", "a: !Nope x", yamlconv.MsgUnknownTag + " !Nope", 1, 4, true},
		{"complex key", "? [a, b]\n: 1", yamlconv.MsgComplexKey, 1, 3, false},
		{"binary", "a: !!binary aGk=", yamlconv.MsgUnsupported + " !!binary", 1, 4, false},
		{"set", "a: !!set {x: null}", yamlconv.MsgUnsupported + " !!set", 1, 4, false},
		{"omap", "a: !!omap [x: 1]", yamlconv.MsgUnsupported + " !!omap", 1, 4, false},
		{"two documents", "a: 1\n---\nb: 2", yamlconv.MsgMultiDoc, 2, 1, false},
	}

	hook := func(string, any) (any, bool) { return nil, false }

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var h yamlconv.TagFunc
			if tc.withHook {
				h = hook
			}

			_, err := yamlconv.Decode([]byte(tc.in), h)

			var ye *yamlconv.Error
			require.True(t, errors.As(err, &ye), "want *yamlconv.Error, got %v", err)
			assert.Equal(t, tc.msg, ye.Msg)
			assert.Equal(t, tc.line, ye.Line, "line")
			assert.Equal(t, tc.column, ye.Column, "column")
		})
	}
}

func TestDecodeSyntaxErrorHasLine(t *testing.T) {
	t.Parallel()

	_, err := yamlconv.Decode([]byte("a: 1\nb: [1, 2\nc: 3\n"), nil)

	var ye *yamlconv.Error
	require.True(t, errors.As(err, &ye))
	assert.Positive(t, ye.Line)
	assert.NotContains(t, ye.Msg, "yaml:")
	assert.Contains(t, err.Error(), "line ")
}

// yaml.v3 reports the line where the block starts. Decode reports the line
// that breaks it.
func TestDecodeSyntaxErrorProblemLine(t *testing.T) {
	t.Parallel()

	_, err := yamlconv.Decode([]byte("a:\n  b: 1\n  c: 2\n bad: 3\n"), nil)

	var ye *yamlconv.Error
	require.True(t, errors.As(err, &ye))
	assert.Equal(t, 4, ye.Line)
	assert.Equal(t, 0, ye.Column)
}

func TestDecodeEmpty(t *testing.T) {
	t.Parallel()

	got, err := yamlconv.Decode(nil, nil)
	require.NoError(t, err)
	assert.Nil(t, got)

	got, err = yamlconv.Decode([]byte("# only a comment\n"), nil)
	require.NoError(t, err)
	assert.Nil(t, got)
}
