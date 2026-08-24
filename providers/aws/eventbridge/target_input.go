package eventbridge

import (
	"encoding/json"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/eventbus/driver"
)

// targetBody computes the payload EventBridge delivers to a single target given
// the rendered event envelope. Precedence mirrors real EventBridge, where a
// target carries at most one of these: InputTransformer > Input > InputPath >
// the raw envelope.
func targetBody(t *driver.Target, envelope []byte, reserved map[string]json.RawMessage) string {
	if t.InputTransformer != "" {
		if body, ok := applyInputTransformer(t.InputTransformer, envelope, reserved); ok {
			return body
		}
	}

	if t.Input != "" {
		return t.Input
	}

	if t.InputPath != "" {
		if sub, ok := extractPath(envelope, t.InputPath); ok {
			return jsonToString(sub)
		}
	}

	return string(envelope)
}

type inputTransformer struct {
	InputPathsMap map[string]string `json:"InputPathsMap"`
	InputTemplate string            `json:"InputTemplate"`
}

// applyInputTransformer substitutes each InputPathsMap variable (extracted from
// the envelope by its JSONPath) and the predefined reserved variables
// (<aws.events.*>, which need no InputPathsMap declaration) into InputTemplate.
// Substitution is quote-context aware, matching EventBridge: a string value is
// auto-quoted when the placeholder is a standalone JSON field value, but inserted
// unquoted when the placeholder already sits inside a quoted string literal
// (AWS's "Including reserved variables in a string" behavior). When the whole
// template is a JSON string literal, the delivered body is its unquoted value.
func applyInputTransformer(raw string, envelope []byte, reserved map[string]json.RawMessage) (string, bool) {
	var it inputTransformer
	if err := json.Unmarshal([]byte(raw), &it); err != nil || it.InputTemplate == "" {
		return "", false
	}

	result := substituteVars(it.InputTemplate, it.InputPathsMap, envelope, reserved)

	trimmed := strings.TrimSpace(it.InputTemplate)
	if strings.HasPrefix(trimmed, "\"") && strings.HasSuffix(trimmed, "\"") {
		var unquoted string
		if err := json.Unmarshal([]byte(result), &unquoted); err == nil {
			return unquoted, true
		}
	}

	return result, true
}

// substituteVars replaces every <name> placeholder in the template in a single
// left-to-right pass that tracks whether the placeholder sits inside a quoted
// JSON string literal. Reserved <aws.events.*> variables take precedence over
// InputPathsMap variables (reserved names cannot be overwritten). A string value
// is emitted unquoted inside a string context and quoted in a standalone JSON
// field context; object/array/number values are emitted as raw JSON, and quotes
// are stripped from them inside a string context (matching EventBridge, which
// removes internal quotes to keep a string valid).
func substituteVars(template string, paths map[string]string, envelope []byte, reserved map[string]json.RawMessage) string {
	var b strings.Builder

	inString := false

	i := 0
	for i < len(template) {
		c := template[i]

		if inString && c == '\\' && i+1 < len(template) {
			b.WriteByte(c)
			b.WriteByte(template[i+1])

			i += 2

			continue
		}

		if c == '"' {
			inString = !inString

			b.WriteByte(c)

			i++

			continue
		}

		if repl, n, ok := matchVar(template[i:], inString, paths, envelope, reserved); ok {
			b.WriteString(repl)

			i += n

			continue
		}

		b.WriteByte(c)

		i++
	}

	return b.String()
}

// matchVar tests whether s begins with a <name> placeholder for a known
// transformer variable. On a match it returns the rendered substitution (given
// the surrounding quote context), the number of bytes the placeholder occupies,
// and true; otherwise the leading byte is not the start of a substitution.
func matchVar(
	s string,
	inString bool,
	paths map[string]string,
	envelope []byte,
	reserved map[string]json.RawMessage,
) (rendered string, consumed int, matched bool) {
	if s == "" || s[0] != '<' {
		return "", 0, false
	}

	end := strings.IndexByte(s, '>')
	if end <= 0 {
		return "", 0, false
	}

	val, ok := resolveVar(s[1:end], paths, envelope, reserved)
	if !ok {
		return "", 0, false
	}

	return renderVar(val, inString), end + 1, true
}

// resolveVar returns the JSON value for a transformer variable name: a reserved
// <aws.events.*> variable if defined, otherwise the InputPathsMap variable
// resolved against the envelope. A declared InputPathsMap variable whose JSONPath
// is missing resolves to null; an unknown name is not a variable at all.
func resolveVar(name string, paths map[string]string, envelope []byte, reserved map[string]json.RawMessage) (json.RawMessage, bool) {
	if val, ok := reserved[name]; ok {
		return val, true
	}

	path, ok := paths[name]
	if !ok {
		return nil, false
	}

	val, ok := extractPath(envelope, path)
	if !ok {
		return json.RawMessage("null"), true
	}

	return val, true
}

// renderVar formats a variable's JSON value for substitution given whether the
// placeholder sits inside a quoted string literal. A JSON string is unquoted
// inside a string context and kept quoted (raw JSON) standalone; any other value
// is emitted as raw JSON, with its quotes stripped inside a string context.
func renderVar(val json.RawMessage, inString bool) string {
	var s string
	if err := json.Unmarshal(val, &s); err == nil {
		if inString {
			return s
		}

		return string(val)
	}

	if inString {
		return strings.ReplaceAll(string(val), "\"", "")
	}

	return string(val)
}

// extractPath resolves a dotted JSONPath (e.g. "$.detail.state") against the
// envelope, returning the selected value as raw JSON. Only the "$"-rooted
// dotted form EventBridge transformers use is supported.
func extractPath(envelope []byte, path string) (json.RawMessage, bool) {
	if path == "" {
		return nil, false
	}

	var cur any
	if err := json.Unmarshal(envelope, &cur); err != nil {
		return nil, false
	}

	segments := strings.Split(strings.TrimPrefix(path, "$"), ".")
	for _, seg := range segments {
		if seg == "" {
			continue
		}

		obj, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}

		cur, ok = obj[seg]
		if !ok {
			return nil, false
		}
	}

	out, err := json.Marshal(cur)
	if err != nil {
		return nil, false
	}

	return out, true
}

// jsonToString renders selected JSON for delivery: a JSON string is delivered as
// its unquoted content; any other value as its compact JSON form.
func jsonToString(val json.RawMessage) string {
	var s string
	if err := json.Unmarshal(val, &s); err == nil {
		return s
	}

	return string(val)
}
