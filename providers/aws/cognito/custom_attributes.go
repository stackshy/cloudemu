package cognito

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/cognito/driver"
)

// Custom attribute limits: at most 50 per pool, and a name (without its
// custom: prefix) of 1 to 20 characters.
const (
	maxCustomAttributes = 50
	maxCustomNameLen    = 20
)

// AddCustomAttributes appends custom attributes to a pool's schema. Each name
// gets the custom: prefix (dev: for developer-only). Cognito never changes or
// removes an attribute once added, so a name already in the schema, or repeated
// in the request, is rejected and nothing is added. A name may be given with or
// without its custom: prefix.
func (m *Mock) AddCustomAttributes(_ context.Context, userPoolID string, attrs []driver.SchemaAttribute) error {
	if len(attrs) == 0 {
		return invalidParameter("1 validation error detected: Value null at 'customAttributes' failed to satisfy constraint: " +
			"Member must not be null")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	pool, ok := m.userPools.Get(userPoolID)
	if !ok {
		return poolNotFound(userPoolID)
	}

	pool = copyUserPool(pool)
	schema := pool.SchemaAttributes

	for i, a := range attrs {
		if n := len(bareCustomName(a.Name)); n < 1 || n > maxCustomNameLen {
			return invalidParameter("1 validation error detected: Value '%s' at 'customAttributes.%d.member.name' "+
				"failed to satisfy constraint: Member must have length between 1 and %d", a.Name, i+1, maxCustomNameLen)
		}

		if a.AttributeDataType == "" {
			a.AttributeDataType = driver.AttributeTypeString
		}

		added := customAttribute(a)
		if _, exists := schemaAttributeIn(schema, added.Name); exists {
			return invalidParameter("Existing attribute already has name %s.", added.Name)
		}

		schema = append(schema, added)
	}

	if countCustom(schema) > maxCustomAttributes {
		return invalidParameter("The user pool has reached the limit of %d custom attributes.", maxCustomAttributes)
	}

	pool.SchemaAttributes = schema
	pool.LastModifiedDate = m.now()
	m.userPools.Set(userPoolID, pool)

	return nil
}

func schemaAttributeIn(schema []driver.SchemaAttribute, name string) (driver.SchemaAttribute, bool) {
	for _, a := range schema {
		if a.Name == name {
			return a, true
		}
	}

	return driver.SchemaAttribute{}, false
}

func countCustom(schema []driver.SchemaAttribute) int {
	n := 0

	for _, a := range schema {
		if strings.HasPrefix(a.Name, "custom:") || strings.HasPrefix(a.Name, "dev:") {
			n++
		}
	}

	return n
}
