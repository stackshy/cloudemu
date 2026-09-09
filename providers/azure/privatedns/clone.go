package privatedns

import "github.com/stackshy/cloudemu/v2/services/privatedns/driver"

// cloneZone deep-copies a zone so stored and returned values never alias a
// caller's tags map. The computed NumberOf* counts copy by value.
//
//nolint:gocritic // hugeParam: clone by value is the intent.
func cloneZone(zone driver.PrivateZone) driver.PrivateZone {
	out := zone
	out.Tags = cloneStringMap(zone.Tags)

	return out
}

// cloneLink deep-copies a link, including its RegistrationEnabled pointer so an
// explicit false is preserved without aliasing the caller's bool.
//
//nolint:gocritic // hugeParam: clone by value is the intent.
func cloneLink(link driver.VirtualNetworkLink) driver.VirtualNetworkLink {
	out := link
	out.Tags = cloneStringMap(link.Tags)
	out.RegistrationEnabled = cloneBoolPtr(link.RegistrationEnabled)

	return out
}

// cloneRecord deep-copies a record set, including its TTL pointer and the
// generic-JSON RecordData map.
//
//nolint:gocritic // hugeParam: clone by value is the intent.
func cloneRecord(rs driver.RecordSet) driver.RecordSet {
	out := rs
	out.TTL = cloneInt64Ptr(rs.TTL)
	out.RecordData = cloneAnyMap(rs.RecordData)

	return out
}

// cloneBoolPtr copies a *bool so the stored pointer never aliases the caller's.
func cloneBoolPtr(b *bool) *bool {
	if b == nil {
		return nil
	}

	v := *b

	return &v
}

// cloneInt64Ptr copies a *int64.
func cloneInt64Ptr(n *int64) *int64 {
	if n == nil {
		return nil
	}

	v := *n

	return &v
}

// cloneAnyMap deep-copies a generic JSON map so no request-owned map or slice is
// aliased into the store.
func cloneAnyMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}

	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = cloneAnyValue(v)
	}

	return out
}

// cloneAnyValue recursively deep-copies a decoded JSON value.
func cloneAnyValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return cloneAnyMap(t)
	case []any:
		out := make([]any, len(t))
		for i := range t {
			out[i] = cloneAnyValue(t[i])
		}

		return out
	default:
		return v
	}
}

// cloneStringMap copies a string map, or returns nil for an empty one.
func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}

	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}

	return out
}
