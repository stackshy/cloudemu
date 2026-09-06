package cognito

import "github.com/stackshy/cloudemu/v2/services/cognito/driver"

// The default user-pool schema Cognito seeds on CreateUserPool: the 20 standard
// OIDC attributes, in the order real Cognito returns them. Most are mutable,
// optional String attributes bounded to [0, 2048]; the exceptions (sub, the two
// verified-flag Booleans, birthdate's fixed length, and the updated_at Number)
// are spelled out individually.

// String-length bounds shared by the standard string attributes.
const (
	stdStringMin  = "0"
	stdStringMax  = "2048"
	subStringMin  = "1"
	dateFixedLen  = "10"
	numberMinZero = "0"
)

// defaultSchemaAttributes returns a fresh copy of the 20 standard schema
// attributes in Cognito's canonical order.
func defaultSchemaAttributes() []driver.SchemaAttribute {
	// The standard string attributes interleave with the typed special cases in
	// Cognito's canonical ordering, so assemble by explicit name lookup.
	order := []string{
		"name", "given_name", "family_name", "middle_name", "nickname",
		"preferred_username", "profile", "picture", "website", "email",
		"email_verified", "gender", "birthdate", "zoneinfo", "locale",
		"phone_number", "phone_number_verified", "address", "updated_at",
	}

	attrs := make([]driver.SchemaAttribute, 0, 1+len(order))
	attrs = append(attrs, subAttribute())

	for _, name := range order {
		attrs = append(attrs, schemaAttrByName(name))
	}

	return attrs
}

// schemaAttrByName builds one standard attribute by name.
func schemaAttrByName(name string) driver.SchemaAttribute {
	switch name {
	case "email_verified", "phone_number_verified":
		return booleanAttribute(name)
	case "birthdate":
		return stringAttribute(name, dateFixedLen, dateFixedLen)
	case "updated_at":
		return updatedAtAttribute()
	default:
		return stringAttribute(name, stdStringMin, stdStringMax)
	}
}

// subAttribute is the immutable, required "sub" identifier attribute.
func subAttribute() driver.SchemaAttribute {
	return driver.SchemaAttribute{
		Name:                       "sub",
		AttributeDataType:          driver.AttributeTypeString,
		DeveloperOnlyAttribute:     false,
		Mutable:                    false,
		Required:                   true,
		StringAttributeConstraints: &driver.StringAttributeConstraints{MinLength: subStringMin, MaxLength: stdStringMax},
	}
}

// stringAttribute builds a mutable, optional String attribute with the given
// length bounds.
func stringAttribute(name, minLen, maxLen string) driver.SchemaAttribute {
	return driver.SchemaAttribute{
		Name:                       name,
		AttributeDataType:          driver.AttributeTypeString,
		DeveloperOnlyAttribute:     false,
		Mutable:                    true,
		Required:                   false,
		StringAttributeConstraints: &driver.StringAttributeConstraints{MinLength: minLen, MaxLength: maxLen},
	}
}

// booleanAttribute builds a mutable, optional Boolean attribute (no
// constraints).
func booleanAttribute(name string) driver.SchemaAttribute {
	return driver.SchemaAttribute{
		Name:                   name,
		AttributeDataType:      driver.AttributeTypeBoolean,
		DeveloperOnlyAttribute: false,
		Mutable:                true,
		Required:               false,
	}
}

// updatedAtAttribute is the mutable Number attribute bounded at MinValue 0.
func updatedAtAttribute() driver.SchemaAttribute {
	return driver.SchemaAttribute{
		Name:                       "updated_at",
		AttributeDataType:          driver.AttributeTypeNumber,
		DeveloperOnlyAttribute:     false,
		Mutable:                    true,
		Required:                   false,
		NumberAttributeConstraints: &driver.NumberAttributeConstraints{MinValue: numberMinZero},
	}
}
