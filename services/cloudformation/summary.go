package cloudformation

import (
	"slices"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// Capability values CloudFormation asks callers to acknowledge.
const (
	CapabilityIAM        = "CAPABILITY_IAM"
	CapabilityNamedIAM   = "CAPABILITY_NAMED_IAM"
	CapabilityAutoExpand = "CAPABILITY_AUTO_EXPAND"
)

// ExceptionInsufficientCapabilities is the error CreateStack and UpdateStack
// return when the caller did not acknowledge a required capability.
const ExceptionInsufficientCapabilities = "InsufficientCapabilitiesException"

// iamNameProps maps each IAM resource type to the property that gives it a
// custom name. A type with "" has no such property.
var iamNameProps = map[string]string{ //nolint:gochecknoglobals // static lookup table
	"AWS::IAM::AccessKey":           "",
	"AWS::IAM::Group":               "GroupName",
	"AWS::IAM::InstanceProfile":     "InstanceProfileName",
	"AWS::IAM::ManagedPolicy":       "ManagedPolicyName",
	"AWS::IAM::Policy":              "",
	"AWS::IAM::Role":                "RoleName",
	"AWS::IAM::User":                "UserName",
	"AWS::IAM::UserToGroupAddition": "",
}

// Summarize builds the ValidateTemplate view of a parsed template.
func Summarize(t *Template) *TemplateSummary {
	out := &TemplateSummary{Description: t.Description, DeclaredTransforms: transforms(t.Transform)}

	for _, name := range sortedKeys(t.Parameters) {
		def := t.Parameters[name]
		out.Parameters = append(out.Parameters, TemplateParameter{
			Key: name, DefaultValue: scalarString(def.Default), HasDefault: def.Default != nil,
			NoEcho: def.NoEcho, Description: def.Description,
		})
	}

	out.Capabilities, out.CapabilitiesReason = requiredCapabilities(t)

	return out
}

// requiredCapabilities reports CAPABILITY_IAM for IAM resources, or
// CAPABILITY_NAMED_IAM when any of them has a custom name.
func requiredCapabilities(t *Template) (caps []string, reason string) {
	types := map[string]bool{}
	named := false

	for _, r := range t.Resources {
		nameProp, ok := iamNameProps[r.Type]
		if !ok {
			continue
		}

		types[r.Type] = true

		if nameProp != "" {
			if _, has := r.Properties[nameProp]; has {
				named = true
			}
		}
	}

	if len(types) == 0 {
		return nil, ""
	}

	list := sortedKeys(types)
	reason = "The following resource(s) require capabilities: [" + strings.Join(list, ", ") + "]"

	if named {
		return []string{CapabilityNamedIAM}, reason
	}

	return []string{CapabilityIAM}, reason
}

// CheckCapabilities returns InsufficientCapabilitiesException when the
// template needs a capability the caller did not pass. CAPABILITY_NAMED_IAM
// also covers CAPABILITY_IAM.
func CheckCapabilities(t *Template, given []string) error {
	iamCaps, _ := requiredCapabilities(t)

	var missing []string

	for _, c := range iamCaps {
		ok := slices.Contains(given, c) || (c == CapabilityIAM && slices.Contains(given, CapabilityNamedIAM))
		if !ok {
			missing = append(missing, c)
		}
	}

	if len(transforms(t.Transform)) > 0 && !slices.Contains(given, CapabilityAutoExpand) {
		missing = append(missing, CapabilityAutoExpand)
	}

	if len(missing) == 0 {
		return nil
	}

	return NewException(ExceptionInsufficientCapabilities,
		cerrors.Newf(cerrors.InvalidArgument, "Requires capabilities : [%s]", strings.Join(missing, ", ")))
}

// transforms lists the macro names a Transform section declares, in order.
func transforms(v any) []string {
	var out []string

	switch t := v.(type) {
	case string:
		out = append(out, t)
	case []any:
		for _, e := range t {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
	case map[string]any:
		if s, ok := t["Name"].(string); ok {
			out = append(out, s)
		}
	}

	return out
}
