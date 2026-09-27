package ssm

import (
	"encoding/json"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
)

// Document parameter types a Command document can declare.
const (
	paramStringList = "StringList"
	paramInteger    = "Integer"
	paramBoolean    = "Boolean"
	paramStringMap  = "StringMap"
	paramMapList    = "MapList"
)

// excInvalidParameters is what SendCommand returns for parameters that do not
// fit the document.
const excInvalidParameters = "InvalidParameters"

// paramSpec is one declared parameter with the constraints SendCommand
// checks. A negative bound is unset.
type paramSpec struct {
	typ            string
	defaults       []string
	hasDefault     bool
	allowedValues  []string
	allowedPattern string
	minItems       int
	maxItems       int
	minChars       int
	maxChars       int
}

// commandStep is one plugin a Command document runs. Name is the step name
// (schema 2.x) or the plugin name (schema 1.2).
type commandStep struct {
	Name   string `json:"name"`
	Action string `json:"action"`
	// Legacy is set for a schema 1.2 runtimeConfig plugin, whose output sits
	// under a "0.<plugin>" folder.
	Legacy bool `json:"legacy,omitempty"`
}

// parameterSpecs reads the declared parameters and their constraints.
func parameterSpecs(v any) map[string]paramSpec {
	params, ok := v.(map[string]any)
	if !ok {
		return nil
	}

	out := make(map[string]paramSpec, len(params))

	for name, raw := range params {
		spec, _ := raw.(map[string]any)
		p := paramSpec{
			typ:            scalarString(spec["type"]),
			allowedPattern: scalarString(spec["allowedPattern"]),
			minItems:       intField(spec, "minItems"),
			maxItems:       intField(spec, "maxItems"),
			minChars:       intField(spec, "minChars"),
			maxChars:       intField(spec, "maxChars"),
		}

		if def, ok := spec["default"]; ok {
			p.hasDefault = true
			p.defaults = paramValues(def)
		}

		if allowed, ok := spec["allowedValues"].([]any); ok {
			for _, a := range allowed {
				p.allowedValues = append(p.allowedValues, scalarString(a))
			}
		}

		out[name] = p
	}

	return out
}

// intField reads a numeric constraint, or -1 when it is absent.
func intField(spec map[string]any, key string) int {
	n, err := strconv.Atoi(scalarString(spec[key]))
	if err != nil {
		return -1
	}

	return n
}

// paramValues renders a default as the value list SendCommand would carry.
func paramValues(v any) []string {
	switch t := v.(type) {
	case nil:
		return nil
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			out = append(out, jsonScalar(e))
		}

		return out
	default:
		return []string{jsonScalar(t)}
	}
}

// jsonScalar renders a scalar as text and anything else as JSON.
func jsonScalar(v any) string {
	if s := scalarString(v); s != "" || v == "" {
		return s
	}

	b, _ := json.Marshal(v)

	return string(b)
}

// documentSteps lists the plugins of a Command document: the runtimeConfig
// plugins of schema 1.2 or the mainSteps of schema 2.x.
func documentSteps(root map[string]any) []commandStep {
	var out []commandStep

	if rc, ok := root["runtimeConfig"].(map[string]any); ok {
		names := make([]string, 0, len(rc))
		for name := range rc {
			names = append(names, name)
		}

		sort.Strings(names)

		for _, name := range names {
			out = append(out, commandStep{Name: name, Action: name, Legacy: true})
		}
	}

	steps, _ := root["mainSteps"].([]any)
	for _, s := range steps {
		if step, ok := s.(map[string]any); ok {
			out = append(out, commandStep{Name: scalarString(step["name"]), Action: scalarString(step["action"])})
		}
	}

	return out
}

// validateParameters checks SendCommand parameters against the document's
// declared parameters and returns them with the defaults filled in.
func validateParameters(doc string, specs map[string]paramSpec, given map[string][]string) (map[string][]string, error) {
	undeclared := make([]string, 0)

	for name := range given {
		if _, ok := specs[name]; !ok {
			undeclared = append(undeclared, name)
		}
	}

	if len(undeclared) > 0 {
		sort.Strings(undeclared)

		return nil, ssmErrf(excInvalidParameters, errors.InvalidArgument,
			"Parameters %v are not defined in document %s.", undeclared, doc)
	}

	names := make([]string, 0, len(specs))
	for name := range specs {
		names = append(names, name)
	}

	sort.Strings(names)

	resolved := make(map[string][]string, len(specs))

	for _, name := range names {
		spec := specs[name]

		values, ok := given[name]
		if !ok {
			if !spec.hasDefault {
				return nil, ssmErrf(excInvalidParameters, errors.InvalidArgument,
					"Parameter \"%s\" is required by document %s but was not supplied.", name, doc)
			}

			resolved[name] = spec.defaults

			continue
		}

		if err := spec.check(name, values); err != nil {
			return nil, err
		}

		resolved[name] = values
	}

	return resolved, nil
}

// check validates the values supplied for one parameter.
func (p *paramSpec) check(name string, values []string) error {
	if err := p.checkShape(name, values); err != nil {
		return err
	}

	for _, v := range values {
		if err := p.checkValue(name, v); err != nil {
			return err
		}
	}

	return nil
}

// checkShape checks the value count and each value's type.
func (p *paramSpec) checkShape(name string, values []string) error {
	if p.typ == paramStringList {
		if p.minItems >= 0 && len(values) < p.minItems {
			return invalidParam(name, "needs at least %d values", p.minItems)
		}

		if p.maxItems >= 0 && len(values) > p.maxItems {
			return invalidParam(name, "accepts at most %d values", p.maxItems)
		}

		return nil
	}

	if len(values) != 1 {
		return invalidParam(name, "of type %s takes exactly one value", p.typ)
	}

	return p.checkScalar(name, values[0])
}

// checkScalar checks that a single value parses as the parameter's type.
func (p *paramSpec) checkScalar(name, v string) error {
	switch p.typ {
	case paramInteger:
		if _, err := strconv.Atoi(v); err != nil {
			return invalidParam(name, "must be an Integer, got %q", v)
		}
	case paramBoolean:
		if v != "true" && v != "false" {
			return invalidParam(name, "must be a Boolean, got %q", v)
		}
	case paramStringMap:
		var m map[string]any
		if json.Unmarshal([]byte(v), &m) != nil {
			return invalidParam(name, "must be a StringMap (a JSON object)")
		}
	case paramMapList:
		var l []map[string]any
		if json.Unmarshal([]byte(v), &l) != nil {
			return invalidParam(name, "must be a MapList (a JSON list of objects)")
		}
	}

	return nil
}

// checkValue checks one value against allowedValues, allowedPattern and the
// character limits.
func (p *paramSpec) checkValue(name, v string) error {
	if len(p.allowedValues) > 0 && !slices.Contains(p.allowedValues, v) {
		return invalidParam(name, "value %q is not one of the allowed values [%s]", v, strings.Join(p.allowedValues, ", "))
	}

	if p.allowedPattern != "" {
		// The pattern must match the whole value. One Go cannot compile is
		// not checked rather than rejecting every send.
		if re, err := regexp.Compile(`^(?:` + p.allowedPattern + `)$`); err == nil && !re.MatchString(v) {
			return invalidParam(name, "value %q does not match the allowed pattern %s", v, p.allowedPattern)
		}
	}

	if p.minChars >= 0 && len(v) < p.minChars {
		return invalidParam(name, "value must have at least %d characters", p.minChars)
	}

	if p.maxChars >= 0 && len(v) > p.maxChars {
		return invalidParam(name, "value must have at most %d characters", p.maxChars)
	}

	return nil
}

func invalidParam(name, format string, args ...any) error {
	return ssmErrf(excInvalidParameters, errors.InvalidArgument, "Parameter \"%s\" "+format+".", append([]any{name}, args...)...)
}
