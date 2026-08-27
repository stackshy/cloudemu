package monitor

import "github.com/stackshy/cloudemu/v2/server/wire/azurearm"

// resourceRequest is the inbound CreateOrUpdate body shared by every
// microsoft.insights ARM resource type. The whole properties object is captured
// as-is so it round-trips untouched.
type resourceRequest struct {
	Location   string            `json:"location"`
	Tags       map[string]string `json:"tags,omitempty"`
	Properties map[string]any    `json:"properties"`
}

// resourceResponse is the ARM resource envelope.
type resourceResponse struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Type       string            `json:"type"`
	Location   string            `json:"location"`
	Tags       map[string]string `json:"tags,omitempty"`
	Properties map[string]any    `json:"properties"`
}

// resourceListResponse is the ARM list envelope. Value is always a non-nil slice
// so an empty list serializes as {"value":[]} rather than {"value":null}.
type resourceListResponse struct {
	Value []resourceResponse `json:"value"`
}

// toResourceJSON renders a stored resource under the request's path scope.
func toResourceJSON(rp *azurearm.ResourcePath, kind string, res *armResource) resourceResponse {
	location := res.Location
	if location == "" {
		location = defaultLocation
	}

	props := res.Properties
	if props == nil {
		props = map[string]any{}
	}

	return resourceResponse{
		ID:         azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, kind, rp.ResourceName),
		Name:       rp.ResourceName,
		Type:       armType(kind),
		Location:   location,
		Tags:       res.Tags,
		Properties: props,
	}
}

// withProvisioningState injects a terminal provisioningState into a metric
// alert's properties (real metricAlerts carry provisioningState=Succeeded), so a
// state-refresh read sees a settled resource.
func withProvisioningState(props map[string]any) map[string]any {
	if props == nil {
		props = map[string]any{}
	}

	if _, ok := props["provisioningState"]; !ok {
		props["provisioningState"] = "Succeeded"
	}

	return props
}

// mergeResource applies a PATCH body over a stored resource under nil-mask
// semantics: a top-level field the caller omits (location, tags, properties) is
// preserved from the stored resource, while a supplied one is merged key-by-key
// over the stored value. The merge is a fresh resource, so the stored one is not
// mutated until the caller commits it.
func mergeResource(existing *armResource, patch *resourceRequest) *armResource {
	merged := &armResource{
		Location:   existing.Location,
		Tags:       mergeStringMap(existing.Tags, patch.Tags),
		Properties: mergeAnyMap(existing.Properties, patch.Properties),
	}

	if patch.Location != "" {
		merged.Location = patch.Location
	}

	return merged
}

// mergeStringMap overlays overlay onto a copy of base, preserving base keys the
// overlay omits. Returns nil only when both are nil.
func mergeStringMap(base, overlay map[string]string) map[string]string {
	if base == nil && overlay == nil {
		return nil
	}

	out := make(map[string]string, len(base)+len(overlay))
	for k, v := range base {
		out[k] = v
	}

	for k, v := range overlay {
		out[k] = v
	}

	return out
}

// mergeAnyMap overlays overlay onto a copy of base, preserving base keys the
// overlay omits. Returns nil only when both are nil.
func mergeAnyMap(base, overlay map[string]any) map[string]any {
	if base == nil && overlay == nil {
		return nil
	}

	out := make(map[string]any, len(base)+len(overlay))
	for k, v := range base {
		out[k] = v
	}

	for k, v := range overlay {
		out[k] = v
	}

	return out
}
