package frontdoor

import "github.com/stackshy/cloudemu/v2/services/frontdoor/driver"

// cloneProfile deep-copies a profile so stored and returned values never alias a
// caller's maps.
//
//nolint:gocritic // hugeParam: clone by value is the intent.
func cloneProfile(p driver.AzureFrontDoorProfile) driver.AzureFrontDoorProfile {
	out := p
	out.Identity = cloneAnyMap(p.Identity)
	out.Tags = cloneStringMap(p.Tags)
	out.OtherProps = cloneAnyMap(p.OtherProps)

	return out
}

// cloneEndpoint deep-copies an endpoint.
//
//nolint:gocritic // hugeParam: clone by value is the intent.
func cloneEndpoint(e driver.AzureFrontDoorEndpoint) driver.AzureFrontDoorEndpoint {
	out := e
	out.Tags = cloneStringMap(e.Tags)
	out.Properties = cloneAnyMap(e.Properties)

	return out
}

// cloneOriginGroup deep-copies an origin group.
func cloneOriginGroup(g driver.AzureFrontDoorOriginGroup) driver.AzureFrontDoorOriginGroup {
	out := g
	out.Properties = cloneAnyMap(g.Properties)

	return out
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
