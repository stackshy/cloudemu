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

// mergeResource applies a PATCH body over a stored resource. location and
// properties follow nil-mask semantics: a field the caller omits is preserved
// from the stored resource, a supplied one applies (properties merged
// key-by-key over the stored value). tags do not merge: real ARM resource-level
// PATCH (metricAlerts/actionGroups/activityLogAlerts/autoscaleSettings Update,
// same as the compute/network/loadbalancer Update and UpdateTags operations
// already fixed elsewhere in this codebase) SETS the tag collection wholesale
// when the request carries a tags key — a populated map replaces the stored
// set, an explicit tags:{} wipes it, and an absent tags key leaves the stored
// set untouched. The merge is a fresh resource, so the stored one is not
// mutated until the caller commits it.
func mergeResource(existing *armResource, patch *resourceRequest) *armResource {
	merged := &armResource{
		Location:   existing.Location,
		Tags:       existing.Tags,
		Properties: mergeAnyMap(existing.Properties, patch.Properties),
	}

	if patch.Location != "" {
		merged.Location = patch.Location
	}

	if patch.Tags != nil {
		merged.Tags = replacementTags(patch.Tags)
	}

	return merged
}

// replacementTags normalizes a PATCH body's tags for wholesale replacement: a
// populated map is cloned (so the store never aliases the request body), an
// empty map ({}) wipes the stored set to nil.
func replacementTags(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}

	out := make(map[string]string, len(in))
	for k, v := range in {
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
