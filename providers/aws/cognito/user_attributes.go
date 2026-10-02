package cognito

import (
	"slices"
	"strconv"

	"github.com/stackshy/cloudemu/v2/services/cognito/driver"
)

// schemaError builds the InvalidParameterException Cognito sends when a user
// attribute does not fit the pool schema.
func schemaError(name, reason string) error {
	return invalidParameter("Attributes did not conform to the schema: %s: %s", name, reason)
}

// schemaAttribute looks up an attribute in the pool schema by its full name.
func schemaAttribute(pool *driver.UserPool, name string) (driver.SchemaAttribute, bool) {
	return schemaAttributeIn(pool.SchemaAttributes, name)
}

// validateAttributes checks user attributes against the pool schema: every name
// must exist, sub is never caller-set, an update may not touch an immutable
// attribute, and a string may not pass its maximum length.
func validateAttributes(pool *driver.UserPool, attrs []driver.Attribute, update bool) error {
	for _, attr := range attrs {
		a, ok := schemaAttribute(pool, attr.Name)
		if !ok {
			return schemaError(attr.Name, "Attribute does not exist in the schema.")
		}

		if attr.Name == attrSub || (update && !a.Mutable) {
			return schemaError(attr.Name, "Attribute cannot be updated. (changing an immutable attribute)")
		}

		if c := a.StringAttributeConstraints; c != nil && c.MaxLength != "" {
			if limit, err := strconv.Atoi(c.MaxLength); err == nil && len(attr.Value) > limit {
				return schemaError(attr.Name, "String must be no longer than "+c.MaxLength+" characters")
			}
		}
	}

	return nil
}

// mergeAttributes returns base with updates applied: an existing name is
// replaced in place and a new one is appended. base is not modified.
func mergeAttributes(base, updates []driver.Attribute) []driver.Attribute {
	out := slices.Clone(base)

	for _, u := range updates {
		i := slices.IndexFunc(out, func(a driver.Attribute) bool { return a.Name == u.Name })
		if i >= 0 {
			out[i].Value = u.Value

			continue
		}

		out = append(out, u)
	}

	return out
}

// attrValue returns the value of a named attribute, or "" when absent.
func attrValue(attrs []driver.Attribute, name string) string {
	v, _ := lastValue(attrs, name)

	return v
}

// lastValue returns the last value given for name and whether it was present.
func lastValue(attrs []driver.Attribute, name string) (string, bool) {
	for i := len(attrs) - 1; i >= 0; i-- {
		if attrs[i].Name == name {
			return attrs[i].Value, true
		}
	}

	return "", false
}

// selectAttributes applies ListUsers AttributesToGet: nil keeps everything, and
// an empty list keeps nothing.
func selectAttributes(attrs []driver.Attribute, want []string) []driver.Attribute {
	if want == nil {
		return attrs
	}

	return slices.DeleteFunc(attrs, func(a driver.Attribute) bool { return !slices.Contains(want, a.Name) })
}
