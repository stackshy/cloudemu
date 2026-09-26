package cloudformation

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// Template is the parsed CloudFormation document. Only the sections the
// orchestrator acts on are modeled. Scalar fields such as Description accept any
// scalar and hold its string form, the way CloudFormation reads them.
type Template struct {
	FormatVersion string
	Description   string
	Parameters    map[string]ParameterDef
	Mappings      map[string]map[string]map[string]any
	Conditions    map[string]any
	Resources     map[string]ResourceDef
	Outputs       map[string]OutputDef
	Transform     any
}

// ParameterDef is a template parameter declaration.
type ParameterDef struct {
	Type          string
	Default       any
	Description   string
	AllowedValues []any
	NoEcho        bool
	// AllowedPattern must match the whole value. An empty pattern means none.
	AllowedPattern        string
	MinLength             *int
	MaxLength             *int
	MinValue              *float64
	MaxValue              *float64
	ConstraintDescription string
}

// ResourceDef is one resource declaration keyed by logical ID in the template.
type ResourceDef struct {
	Type       string
	Properties map[string]any
	DependsOn  any
	// Condition names the condition that decides whether the resource exists.
	Condition string
}

// OutputDef is one output declaration.
type OutputDef struct {
	Value       any
	Description string
	Export      *ExportDef
	// Condition names the condition that decides whether the output exists.
	Condition string
}

// ExportDef is an output's Export block.
type ExportDef struct {
	Name any
}

// ParseTemplate parses a CloudFormation template body in JSON or YAML. A body
// whose first non-space character is "{" is JSON. Anything else is YAML, where
// the short-form tags such as !Ref and !GetAtt expand to their long forms.
// Numbers decode as json.Number in both formats, so the trees compare equal.
func ParseTemplate(body string) (*Template, error) {
	if strings.TrimSpace(body) == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, MsgNoTemplate)
	}

	tree, err := decodeTemplate(body)
	if err != nil {
		return nil, err
	}

	top, ok := tree.(map[string]any)
	if !ok {
		return nil, cerrors.New(cerrors.InvalidArgument, formatErrPrefix+"template must be an object")
	}

	if sectionErr := checkSections(top); sectionErr != nil {
		return nil, sectionErr
	}

	t, err := buildTemplate(top)
	if err != nil {
		return nil, err
	}

	if len(t.Resources) == 0 {
		return nil, cerrors.New(cerrors.InvalidArgument, formatErrPrefix+"At least one Resources member must be defined.")
	}

	if vErr := validate(t); vErr != nil {
		return nil, vErr
	}

	return t, nil
}

// templateSections are the top-level keys CloudFormation accepts.
var templateSections = map[string]bool{ //nolint:gochecknoglobals // static lookup table
	"AWSTemplateFormatVersion": true, "Description": true, "Metadata": true,
	"Parameters": true, "Rules": true, "Mappings": true, "Conditions": true,
	"Transform": true, "Resources": true, "Outputs": true, "Hooks": true,
}

func checkSections(top map[string]any) error {
	var bad []string

	for _, k := range sortedKeys(top) {
		if !templateSections[k] {
			bad = append(bad, k)
		}
	}

	if len(bad) > 0 {
		return cerrors.Newf(cerrors.InvalidArgument,
			formatErrPrefix+"Invalid template property or properties [%s]", strings.Join(bad, ", "))
	}

	return nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	return keys
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
			return wordTrue
		}

		return wordFalse
	case json.Number:
		return t.String()
	case noValue:
		return ""
	case []any:
		parts := make([]string, len(t))
		for i, e := range t {
			parts[i] = scalarString(e)
		}

		return strings.Join(parts, ",")
	case float64:
		// Render integers without a trailing ".0".
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}

		return fmt.Sprintf("%g", t)
	default:
		return fmt.Sprintf("%v", t)
	}
}
