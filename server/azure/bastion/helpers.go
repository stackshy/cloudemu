package bastion

// Small generic-JSON extraction helpers shared by the bastion operations. All
// are defensive: a missing or wrongly-typed field yields the zero value rather
// than panicking, so a malformed request body never 500s.

// skuName reads the top-level sku.name, or "".
func skuName(sku *skuJSON) string {
	if sku == nil {
		return ""
	}

	return sku.Name
}

// scaleUnitsFromProps reads properties.scaleUnits (a JSON number decodes to
// float64), or 0 when absent so the store applies the default.
func scaleUnitsFromProps(props map[string]any) int {
	if v, ok := props[scaleUnitsKey].(float64); ok {
		return int(v)
	}

	return 0
}

// boolField reads m[key] as a *bool: the pointer target when present, or nil so
// an unset flag stays distinguishable from an explicit false.
func boolField(m map[string]any, key string) *bool {
	v, ok := m[key].(bool)
	if !ok {
		return nil
	}

	return &v
}

// boolOrFalse projects a *bool as an always-present boolean (nil → false),
// matching Azure, which returns every feature toggle as an explicit boolean.
func boolOrFalse(p *bool) bool {
	return p != nil && *p
}

// nestedID reads m[key].id when m[key] is a JSON object.
func nestedID(m map[string]any, key string) string {
	obj, ok := m[key].(map[string]any)
	if !ok {
		return ""
	}

	return stringField(obj, "id")
}

// stringField reads m[key] as a string, or "".
func stringField(m map[string]any, key string) string {
	s, _ := m[key].(string)

	return s
}

// stripKeys returns a copy of props with the given keys removed, or nil when the
// result is empty. The modeled keys and read-only inputs a client echoed back are
// dropped so they never double up on read.
func stripKeys(props map[string]any, keys []string) map[string]any {
	if len(props) == 0 {
		return nil
	}

	drop := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		drop[k] = struct{}{}
	}

	out := make(map[string]any, len(props))

	for k, v := range props {
		if _, skip := drop[k]; skip {
			continue
		}

		out[k] = v
	}

	if len(out) == 0 {
		return nil
	}

	return out
}

// optionalSubResource wraps a non-empty id in a subResource, or returns nil.
func optionalSubResource(id string) *subResource {
	if id == "" {
		return nil
	}

	return &subResource{ID: id}
}

// firstNonEmpty returns a if non-empty, otherwise b.
func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}

	return b
}
