package cloudformation

import (
	"strconv"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// buildTemplate reads the decoded tree into a Template. It checks each field's
// shape itself so a bad field gets a CloudFormation message, not a Go decoder
// error.
func buildTemplate(top map[string]any) (*Template, error) {
	t := &Template{Transform: top["Transform"]}

	var err error

	if t.FormatVersion, err = scalarField(top["AWSTemplateFormatVersion"], "AWSTemplateFormatVersion"); err != nil {
		return nil, err
	}

	if t.Description, err = scalarField(top["Description"], "Description"); err != nil {
		return nil, err
	}

	if t.Parameters, err = buildSection(top, "Parameters", buildParameter); err != nil {
		return nil, err
	}

	if t.Mappings, err = buildMappings(top["Mappings"]); err != nil {
		return nil, err
	}

	if t.Conditions, err = objectField(top["Conditions"], "Conditions"); err != nil {
		return nil, err
	}

	if t.Resources, err = buildSection(top, "Resources", buildResource); err != nil {
		return nil, err
	}

	if t.Outputs, err = buildSection(top, "Outputs", buildOutput); err != nil {
		return nil, err
	}

	return t, nil
}

// The spellings CloudFormation accepts for a boolean written as a string.
const (
	wordTrue  = "true"
	wordFalse = "false"
)

func formatErr(format string, args ...any) error {
	return cerrors.Newf(cerrors.InvalidArgument, formatErrPrefix+format, args...)
}

// scalarField reads a field that must be a scalar and returns its string form.
// A missing field gives "".
func scalarField(v any, path string) (string, error) {
	switch v.(type) {
	case map[string]any, []any:
		return "", formatErr("[/%s] must be a string", path)
	default:
		return scalarString(v), nil
	}
}

// buildSection reads a top-level section whose members are objects keyed by
// logical name.
func buildSection[T any](top map[string]any, name string, build func(m map[string]any, path string) (T, error)) (map[string]T, error) {
	raw, present := top[name]
	if !present || raw == nil {
		return nil, nil
	}

	members, ok := raw.(map[string]any)
	if !ok {
		return nil, formatErr("[/%s] must be an object", name)
	}

	out := make(map[string]T, len(members))

	for _, key := range sortedKeys(members) {
		path := name + "/" + key

		m, ok := members[key].(map[string]any)
		if !ok {
			return nil, formatErr("[/%s] Every %s member must be an object.", path, name)
		}

		v, err := build(m, path)
		if err != nil {
			return nil, err
		}

		out[key] = v
	}

	return out, nil
}

func buildParameter(m map[string]any, path string) (ParameterDef, error) {
	var (
		p   = ParameterDef{Default: m["Default"]}
		err error
	)

	if p.Type, err = scalarField(m["Type"], path+"/Type"); err != nil {
		return p, err
	}

	if p.Type == "" {
		return p, formatErr("[/%s] Every Parameters object must contain a Type member.", path)
	}

	if !validParamType(p.Type) {
		return p, formatErr("Unrecognized parameter type: %s", p.Type)
	}

	if p.Description, err = scalarField(m["Description"], path+"/Description"); err != nil {
		return p, err
	}

	if p.NoEcho, err = boolField(m["NoEcho"], path+"/NoEcho"); err != nil {
		return p, err
	}

	switch av := m["AllowedValues"].(type) {
	case nil:
	case []any:
		p.AllowedValues = av
	default:
		return p, formatErr("[/%s/AllowedValues] must be a list", path)
	}

	err = buildConstraints(m, path, &p)

	return p, err
}

// buildConstraints reads the value constraints a parameter may declare.
func buildConstraints(m map[string]any, path string, p *ParameterDef) error {
	var err error

	if p.AllowedPattern, err = scalarField(m["AllowedPattern"], path+"/AllowedPattern"); err != nil {
		return err
	}

	if p.ConstraintDescription, err = scalarField(m["ConstraintDescription"], path+"/ConstraintDescription"); err != nil {
		return err
	}

	if p.MinLength, err = intField(m["MinLength"], path+"/MinLength"); err != nil {
		return err
	}

	if p.MaxLength, err = intField(m["MaxLength"], path+"/MaxLength"); err != nil {
		return err
	}

	if p.MinValue, err = numberField(m["MinValue"], path+"/MinValue"); err != nil {
		return err
	}

	p.MaxValue, err = numberField(m["MaxValue"], path+"/MaxValue")

	return err
}

// numberField reads a number written as a number or a numeric string. A
// missing field gives nil.
func numberField(v any, path string) (*float64, error) {
	if v == nil {
		return nil, nil
	}

	switch v.(type) {
	case map[string]any, []any, bool:
		return nil, formatErr("[/%s] must be a number", path)
	}

	f, err := strconv.ParseFloat(strings.TrimSpace(scalarString(v)), 64)
	if err != nil {
		return nil, formatErr("[/%s] must be a number", path)
	}

	return &f, nil
}

// intField reads a whole number the way numberField reads any number.
func intField(v any, path string) (*int, error) {
	f, err := numberField(v, path)
	if err != nil || f == nil {
		return nil, err
	}

	if *f != float64(int(*f)) {
		return nil, formatErr("[/%s] must be an integer", path)
	}

	n := int(*f)

	return &n, nil
}

// objectField reads an optional section that must be an object.
func objectField(v any, path string) (map[string]any, error) {
	switch m := v.(type) {
	case nil:
		return nil, nil
	case map[string]any:
		return m, nil
	default:
		return nil, formatErr("[/%s] must be an object", path)
	}
}

// buildMappings reads the Mappings section: each map holds top-level keys,
// and each top-level key holds second-level keys and their values.
func buildMappings(raw any) (map[string]map[string]map[string]any, error) {
	maps, err := objectField(raw, "Mappings")
	if err != nil || maps == nil {
		return nil, err
	}

	out := make(map[string]map[string]map[string]any, len(maps))

	for _, name := range sortedKeys(maps) {
		top, ok := maps[name].(map[string]any)
		if !ok {
			return nil, formatErr("[/Mappings/%s] Every Mappings member %s must be a map", name, name)
		}

		inner := make(map[string]map[string]any, len(top))

		for _, key := range sortedKeys(top) {
			second, ok := top[key].(map[string]any)
			if !ok {
				return nil, formatErr("[/Mappings/%s/%s] Every Mappings attribute must be a map", name, key)
			}

			inner[key] = second
		}

		out[name] = inner
	}

	return out, nil
}

// boolField reads a boolean written as true/false or as the strings "true" and
// "false", both of which CloudFormation accepts.
func boolField(v any, path string) (bool, error) {
	switch b := v.(type) {
	case nil:
		return false, nil
	case bool:
		return b, nil
	case string:
		switch strings.ToLower(b) {
		case wordTrue:
			return true, nil
		case wordFalse:
			return false, nil
		}
	}

	return false, formatErr("[/%s] must be a boolean", path)
}

func buildResource(m map[string]any, path string) (ResourceDef, error) {
	r := ResourceDef{DependsOn: m["DependsOn"]}

	var err error

	if r.Type, err = scalarField(m["Type"], path+"/Type"); err != nil {
		return r, err
	}

	if r.Type == "" {
		return r, formatErr("[/%s] Every Resources object must contain a Type member.", path)
	}

	switch props := m["Properties"].(type) {
	case nil:
	case map[string]any:
		r.Properties = props
	default:
		return r, formatErr("[/%s/Properties] must be an object", path)
	}

	r.Condition, err = scalarField(m["Condition"], path+"/Condition")

	return r, err
}

func buildOutput(m map[string]any, path string) (OutputDef, error) {
	o := OutputDef{Value: m["Value"]}

	if o.Value == nil {
		return o, formatErr("[/%s] Every Outputs member must contain a Value object", path)
	}

	var err error

	if o.Description, err = scalarField(m["Description"], path+"/Description"); err != nil {
		return o, err
	}

	switch exp := m["Export"].(type) {
	case nil:
	case map[string]any:
		o.Export = &ExportDef{Name: exp["Name"]}
	default:
		return o, formatErr("[/%s/Export] must be an object", path)
	}

	o.Condition, err = scalarField(m["Condition"], path+"/Condition")

	return o, err
}
