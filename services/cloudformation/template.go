package cloudformation

import (
	"encoding/json"
	"fmt"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// Template is the parsed CloudFormation document. Only the sections the
// orchestrator acts on are modeled; unknown top-level keys are ignored.
type Template struct {
	FormatVersion string                  `json:"AWSTemplateFormatVersion"`
	Description   string                  `json:"Description"`
	Parameters    map[string]ParameterDef `json:"Parameters"`
	Resources     map[string]ResourceDef  `json:"Resources"`
	Outputs       map[string]OutputDef    `json:"Outputs"`
}

// ParameterDef is a template parameter declaration.
type ParameterDef struct {
	Type          string `json:"Type"`
	Default       any    `json:"Default"`
	Description   string `json:"Description"`
	AllowedValues []any  `json:"AllowedValues"`
	NoEcho        bool   `json:"NoEcho"`
}

// ResourceDef is one resource declaration keyed by logical ID in the template.
type ResourceDef struct {
	Type       string         `json:"Type"`
	Properties map[string]any `json:"Properties"`
	DependsOn  any            `json:"DependsOn"`
}

// OutputDef is one output declaration.
type OutputDef struct {
	Value       any    `json:"Value"`
	Description string `json:"Description"`
	Export      *struct {
		Name any `json:"Name"`
	} `json:"Export"`
}

// ParseTemplate parses a CloudFormation template body. Only JSON is supported;
// YAML (which additionally needs the short-form intrinsic tags !Ref/!GetAtt) is
// deferred. An empty body or one with no Resources section is rejected the way
// CloudFormation rejects a template with no resources.
func ParseTemplate(body string) (*Template, error) {
	if body == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "template body is empty")
	}

	var t Template
	if err := json.Unmarshal([]byte(body), &t); err != nil {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "template format error: %v", err)
	}

	if len(t.Resources) == 0 {
		return nil, cerrors.New(cerrors.InvalidArgument,
			"template format error: at least one Resources member must be defined")
	}

	for id, r := range t.Resources {
		if r.Type == "" {
			return nil, cerrors.Newf(cerrors.InvalidArgument,
				"template format error: resource %q has no Type", id)
		}
	}

	return &t, nil
}

// DependsOnList normalizes a resource's DependsOn (a string or a list of
// strings) to a slice.
func (r ResourceDef) DependsOnList() []string {
	switch v := r.DependsOn.(type) {
	case string:
		if v == "" {
			return nil
		}

		return []string{v}
	case []any:
		out := make([]string, 0, len(v))

		for _, e := range v {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}

		return out
	default:
		return nil
	}
}

// Stringify renders a decoded JSON scalar as the string a parameter value or
// intrinsic result carries (integers without a trailing ".0"). Exported for the
// orchestrator, which stringifies parameter defaults.
func Stringify(v any) string {
	return scalarString(v)
}

// scalarString renders a JSON scalar (string, number, bool) as the string a
// parameter value or intrinsic result carries.
func scalarString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		if t {
			return "true"
		}

		return "false"
	case json.Number:
		return t.String()
	case float64:
		// JSON numbers decode as float64; render integers without a trailing ".0".
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}

		return fmt.Sprintf("%g", t)
	default:
		return fmt.Sprintf("%v", t)
	}
}
