package apigateway

import (
	"bytes"
	"encoding/json"
	"math"
	"regexp"
	"strings"
)

// maxSchemaDepth bounds $ref chains and nesting so a self-referencing model
// cannot recurse forever.
const maxSchemaDepth = 32

func validateSchema(doc any, schema map[string]any, models map[string]string, root map[string]any, depth int) bool {
	if depth > maxSchemaDepth {
		return false
	}

	if ref, ok := schema["$ref"].(string); ok {
		target, found := resolveRef(ref, models, root)

		return found && validateSchema(doc, target, models, root, depth+1)
	}

	if !typeAllowed(doc, schema["type"]) || !enumAllowed(doc, schema["enum"]) {
		return false
	}

	return combinatorsHold(doc, schema, models, root, depth) && validateKind(doc, schema, models, root, depth)
}

// validateKind applies the keywords specific to the document's JSON type.
func validateKind(doc any, schema map[string]any, models map[string]string, root map[string]any, depth int) bool {
	switch v := doc.(type) {
	case map[string]any:
		return objectValid(v, schema, models, root, depth)
	case []any:
		return arrayValid(v, schema, models, root, depth)
	case string:
		return stringValid(v, schema)
	case float64:
		return numberValid(v, schema)
	default:
		return true
	}
}

// resolveRef finds a $ref target: "#/definitions/X" in the same document, or a
// ".../models/X" URL naming another model of the API.
func resolveRef(ref string, models map[string]string, root map[string]any) (map[string]any, bool) {
	if def, ok := strings.CutPrefix(ref, "#/definitions/"); ok {
		defs, _ := root["definitions"].(map[string]any)
		target, found := defs[def].(map[string]any)

		return target, found
	}

	idx := strings.LastIndex(ref, "/models/")
	if idx < 0 {
		return nil, false
	}

	text, ok := models[ref[idx+len("/models/"):]]
	if !ok {
		return nil, false
	}

	var target map[string]any
	if err := json.Unmarshal([]byte(text), &target); err != nil {
		return nil, false
	}

	return target, true
}

func typeAllowed(doc, typ any) bool {
	switch t := typ.(type) {
	case string:
		return isJSONType(doc, t)
	case []any:
		for _, e := range t {
			if s, ok := e.(string); ok && isJSONType(doc, s) {
				return true
			}
		}

		return false
	default:
		return true
	}
}

func isJSONType(doc any, t string) bool {
	switch t {
	case "object":
		_, ok := doc.(map[string]any)

		return ok
	case "array":
		_, ok := doc.([]any)

		return ok
	case "string":
		_, ok := doc.(string)

		return ok
	case "boolean":
		_, ok := doc.(bool)

		return ok
	case "null":
		return doc == nil
	case "number":
		_, ok := doc.(float64)

		return ok
	case "integer":
		f, ok := doc.(float64)

		return ok && f == math.Trunc(f)
	default:
		return true
	}
}

func enumAllowed(doc, enum any) bool {
	list, ok := enum.([]any)
	if !ok {
		return true
	}

	want, _ := json.Marshal(doc)

	for _, e := range list {
		if got, _ := json.Marshal(e); bytes.Equal(got, want) {
			return true
		}
	}

	return false
}

func combinatorsHold(doc any, schema map[string]any, models map[string]string, root map[string]any, depth int) bool {
	if all, ok := schema["allOf"].([]any); ok {
		for _, s := range all {
			if sub, isMap := s.(map[string]any); isMap && !validateSchema(doc, sub, models, root, depth+1) {
				return false
			}
		}
	}

	if anyOf, ok := schema["anyOf"].([]any); ok && !countMatches(doc, anyOf, models, root, depth, func(n int) bool { return n > 0 }) {
		return false
	}

	if oneOf, ok := schema["oneOf"].([]any); ok && !countMatches(doc, oneOf, models, root, depth, func(n int) bool { return n == 1 }) {
		return false
	}

	return true
}

func countMatches(doc any, subs []any, models map[string]string, root map[string]any, depth int, accept func(int) bool) bool {
	n := 0

	for _, s := range subs {
		if sub, ok := s.(map[string]any); ok && validateSchema(doc, sub, models, root, depth+1) {
			n++
		}
	}

	return accept(n)
}

func objectValid(obj, schema map[string]any, models map[string]string, root map[string]any, depth int) bool {
	return requiredPresent(obj, schema) && propertiesValid(obj, schema, models, root, depth)
}

// requiredPresent checks every name in "required" is a member of obj.
func requiredPresent(obj, schema map[string]any) bool {
	req, _ := schema["required"].([]any)

	for _, r := range req {
		if name, isStr := r.(string); isStr {
			if _, present := obj[name]; !present {
				return false
			}
		}
	}

	return true
}

// propertiesValid checks each member against its declared property schema, or
// against additionalProperties when it is not declared.
func propertiesValid(obj, schema map[string]any, models map[string]string, root map[string]any, depth int) bool {
	props, _ := schema["properties"].(map[string]any)

	for name, val := range obj {
		sub, declared := props[name].(map[string]any)
		if !declared {
			if !additionalAllowed(val, schema["additionalProperties"], models, root, depth) {
				return false
			}

			continue
		}

		if !validateSchema(val, sub, models, root, depth+1) {
			return false
		}
	}

	return true
}

func additionalAllowed(val, rule any, models map[string]string, root map[string]any, depth int) bool {
	switch r := rule.(type) {
	case bool:
		return r
	case map[string]any:
		return validateSchema(val, r, models, root, depth+1)
	default:
		return true
	}
}

func arrayValid(arr []any, schema map[string]any, models map[string]string, root map[string]any, depth int) bool {
	if lo, ok := schema["minItems"].(float64); ok && float64(len(arr)) < lo {
		return false
	}

	if hi, ok := schema["maxItems"].(float64); ok && float64(len(arr)) > hi {
		return false
	}

	items, ok := schema["items"].(map[string]any)
	if !ok {
		return true
	}

	for _, el := range arr {
		if !validateSchema(el, items, models, root, depth+1) {
			return false
		}
	}

	return true
}

func stringValid(s string, schema map[string]any) bool {
	n := float64(len([]rune(s)))

	if lo, ok := schema["minLength"].(float64); ok && n < lo {
		return false
	}

	if hi, ok := schema["maxLength"].(float64); ok && n > hi {
		return false
	}

	if pat, ok := schema["pattern"].(string); ok {
		re, err := regexp.Compile(pat)
		if err == nil && !re.MatchString(s) {
			return false
		}
	}

	return true
}

func boolMember(schema map[string]any, name string) bool {
	b, _ := schema[name].(bool)

	return b
}

func numberValid(f float64, schema map[string]any) bool {
	if lo, ok := schema["minimum"].(float64); ok {
		if f < lo || (boolMember(schema, "exclusiveMinimum") && f == lo) {
			return false
		}
	}

	if hi, ok := schema["maximum"].(float64); ok {
		if f > hi || (boolMember(schema, "exclusiveMaximum") && f == hi) {
			return false
		}
	}

	return true
}
