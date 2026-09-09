package privatedns

// Small generic-JSON extraction helpers for the record-set operations. All are
// defensive: a missing or wrongly-typed field yields the zero value rather than
// panicking, so a malformed request body never 500s.

// extractTTL reads properties.ttl as an int64. JSON decodes numbers to float64,
// so a whole-number ttl is converted back to an int64 to serialize as an integer
// (not a float) on read.
func extractTTL(props map[string]any) *int64 {
	raw, ok := props[ttlKey]
	if !ok {
		return nil
	}

	f, ok := raw.(float64)
	if !ok {
		return nil
	}

	v := int64(f)

	return &v
}

// stripKeys returns a copy of props with the given keys removed, or nil when the
// result is empty. The computed read-only keys a client may have echoed back are
// dropped so they never double up on read.
func stripKeys(props map[string]any, keys ...string) map[string]any {
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

// recordFQDN computes a record set's fully-qualified domain name with the
// trailing dot Azure appends. The apex record (name "@") resolves to the zone
// name itself.
func recordFQDN(name, zone string) string {
	if name == "@" || name == "" {
		return zone + "."
	}

	return name + "." + zone + "."
}
