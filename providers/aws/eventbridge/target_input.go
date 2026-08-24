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
// the envelope by its JSONPath) into InputTemplate, then substitutes the
// predefined reserved variables (<aws.events.*>) EventBridge supports without an
// InputPathsMap declaration. When the template is a JSON string literal, the
// delivered body is its unquoted value — matching how EventBridge delivers a
// quoted transformer template.
func applyInputTransformer(raw string, envelope []byte, reserved map[string]json.RawMessage) (string, bool) {
	var it inputTransformer
	if err := json.Unmarshal([]byte(raw), &it); err != nil || it.InputTemplate == "" {
		return "", false
	}

	result := it.InputTemplate

	for name, path := range it.InputPathsMap {
		val, ok := extractPath(envelope, path)
		if !ok {
			val = json.RawMessage("null")
		}

		result = strings.ReplaceAll(result, "<"+name+">", templateText(val))
	}

	result = substituteReserved(result, reserved)

	trimmed := strings.TrimSpace(it.InputTemplate)
	if strings.HasPrefix(trimmed, "\"") && strings.HasSuffix(trimmed, "\"") {
		var unquoted string
		if err := json.Unmarshal([]byte(result), &unquoted); err == nil {
			return unquoted, true
		}
	}

	return result, true
}

// reservedVarOrder lists the predefined transformer variables longest-name
// first, so a shorter reserved name (e.g. aws.events.event) never clobbers a
// longer one (aws.events.event.json) during literal replacement.
var reservedVarOrder = []string{ //nolint:gochecknoglobals // fixed substitution order
	"aws.events.event.ingestion-time",
	"aws.events.event.json",
	"aws.events.rule-name",
	"aws.events.rule-arn",
	"aws.events.event",
}

// substituteReserved replaces every <aws.events.*> reserved variable in the
// template with its value. Reserved variables need not be declared in
// InputPathsMap and cannot be overwritten by one, so they are substituted after
// the user variables.
func substituteReserved(template string, reserved map[string]json.RawMessage) string {
	for _, name := range reservedVarOrder {
		val, ok := reserved[name]
		if !ok {
			continue
		}

		// Reserved variables are substituted as their raw JSON: a string keeps its
		// quotes and an object/array its braces, so a JSON-value placeholder such as
		// "ruleName": <aws.events.rule-name> stays valid JSON — matching how
		// EventBridge auto-quotes reserved string variables.
		template = strings.ReplaceAll(template, "<"+name+">", string(val))
	}

	return template
}

// templateText renders an extracted JSON value for substitution into a
// transformer template: a JSON string yields its raw (unquoted) content, so a
// quoted placeholder such as "<st>" stays valid JSON; any other value yields
// its compact JSON form.
func templateText(val json.RawMessage) string {
	var s string
	if err := json.Unmarshal(val, &s); err == nil {
		return s
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
