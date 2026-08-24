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
