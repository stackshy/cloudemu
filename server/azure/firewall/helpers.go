package firewall

// Small generic-JSON extraction helpers shared by the firewall and policy
// operations. All are defensive: a missing or wrongly-typed field yields the
// zero value rather than panicking, so a malformed request body never 500s.

// skuFromProps reads properties.sku.{name,tier}. Both may be empty.
func skuFromProps(props map[string]any) (name, tier string) {
	raw, ok := props[skuKey].(map[string]any)
	if !ok {
		return "", ""
	}

	return stringField(raw, "name"), stringField(raw, "tier")
}

// resolveThreatIntelMode reads properties.threatIntelMode, defaulting to Alert.
func resolveThreatIntelMode(props map[string]any) string {
	if mode := stringField(props, threatIntelModeKey); mode != "" {
		return mode
	}

	return threatIntelModeDefault
}

// subResourceID reads properties.<key>.id (a SubResource {id} reference).
func subResourceID(props map[string]any, key string) string {
	return nestedID(props, key)
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
// result is empty. Read-only inputs a client echoed back (provisioningState) and
// the modeled keys are dropped so they never double up on read.
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

// optionalSubResource wraps a non-empty id in a SubResource, or returns nil.
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
