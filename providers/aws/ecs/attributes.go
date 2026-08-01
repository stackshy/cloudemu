package ecs

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/ecs/driver"
)

// attrKey builds the store key scoping an attribute to its target so that a
// (targetId, name) pair is unique.
func attrKey(targetID, name string) string {
	return targetID + "\x00" + name
}

// PutAttributes upserts custom attributes onto their targets and returns them,
// mirroring AWS's create-or-replace semantics keyed by (targetId, name).
func (m *Mock) PutAttributes(_ context.Context, _ string, attrs []driver.Attribute) ([]driver.Attribute, error) {
	for i := range attrs {
		a := attrs[i]
		if a.Name == "" {
			return nil, apiErrf(errors.InvalidArgument, excInvalidParameter, "attribute name is required")
		}

		stored := a
		m.attributes.Set(attrKey(a.TargetID, a.Name), &stored)
	}

	out := make([]driver.Attribute, len(attrs))
	copy(out, attrs)

	return out, nil
}

// DeleteAttributes removes attributes by (targetId, name) and echoes the request.
func (m *Mock) DeleteAttributes(_ context.Context, _ string, attrs []driver.Attribute) ([]driver.Attribute, error) {
	for i := range attrs {
		m.attributes.Delete(attrKey(attrs[i].TargetID, attrs[i].Name))
	}

	out := make([]driver.Attribute, len(attrs))
	copy(out, attrs)

	return out, nil
}

// ListAttributes returns attributes of the given targetType in deterministic
// (targetId, name) order, optionally filtered by attribute name and value.
func (m *Mock) ListAttributes(
	_ context.Context, _, targetType, attributeName, attributeValue string,
) ([]driver.Attribute, error) {
	all := m.attributes.SortedValues()

	out := make([]driver.Attribute, 0, len(all))

	for _, a := range all {
		if targetType != "" && a.TargetType != targetType {
			continue
		}

		if attributeName != "" && a.Name != attributeName {
			continue
		}

		if attributeValue != "" && a.Value != attributeValue {
			continue
		}

		out = append(out, *a)
	}

	return out, nil
}
