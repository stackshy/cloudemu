package appinsights

import (
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

const (
	providerName  = "Microsoft.Insights"
	typeComponent = "components"
	armType       = "Microsoft.Insights/components"

	// Property defaults documented for Microsoft.Insights/components. A create
	// that omits these gets the same values real Azure fills in, so a Terraform
	// plan that leaves them at their default reads back the default and does not
	// drift.
	defaultAppType       = "web"
	defaultFlowType      = "Bluefield"
	defaultRequestSource = "rest"
	defaultRetentionDays = 90
	defaultIngestionMode = "ApplicationInsights"
	accessEnabled        = "Enabled"

	provisioningSucceeded = "Succeeded"

	// computedKeyCount is how many computed properties toResponse injects
	// (ApplicationId, AppId, InstrumentationKey, ConnectionString,
	// provisioningState, CreationDate, TenantId) — used only to size the response
	// map so it does not grow while they are added.
	computedKeyCount = 7
)

// computedKeys are the properties Azure owns: it generates or derives them and a
// client may never set them on write. They are stripped from an inbound
// properties map so a caller cannot pin a different InstrumentationKey/AppId (the
// REST contract forbids it) and are re-injected from the stored state on read.
//
//nolint:gochecknoglobals // immutable lookup set, not mutable program state
var computedKeys = map[string]struct{}{
	"instrumentationkey": {},
	"appid":              {},
	"applicationid":      {},
	"connectionstring":   {},
	"provisioningstate":  {},
	"creationdate":       {},
	"tenantid":           {},
}

// componentRequest is the inbound CreateOrUpdate / Update body. kind and tags are
// top-level (the property-echo overlay cannot reach top-level fields, so kind is
// modeled explicitly here); the writable properties are captured as a generic
// map so any field beyond the ones defaulted below still round-trips.
type componentRequest struct {
	Location   string            `json:"location"`
	Kind       string            `json:"kind"`
	Tags       map[string]string `json:"tags,omitempty"`
	Properties map[string]any    `json:"properties"`
}

// componentResponse is the ARM component envelope. kind is a top-level field, as
// in the real Microsoft.Insights/components resource.
type componentResponse struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Type       string            `json:"type"`
	Location   string            `json:"location"`
	Kind       string            `json:"kind"`
	Tags       map[string]string `json:"tags,omitempty"`
	Properties map[string]any    `json:"properties"`
}

// componentListResponse is the ARM list envelope; Value is always non-nil so an
// empty list serializes as {"value":[]}.
type componentListResponse struct {
	Value []componentResponse `json:"value"`
}

// applyDefaults fills the documented property defaults into a fresh writable
// property map, without overwriting a value the caller supplied. Computed keys
// are removed first so a client can never pin them.
func applyDefaults(props map[string]any) map[string]any {
	out := make(map[string]any, len(props))

	for k, v := range props {
		if _, computed := computedKeys[strings.ToLower(k)]; computed {
			continue
		}

		out[k] = v
	}

	setDefault(out, "Application_Type", defaultAppType)
	setDefault(out, "Flow_Type", defaultFlowType)
	setDefault(out, "Request_Source", defaultRequestSource)
	setDefault(out, "RetentionInDays", defaultRetentionDays)
	setDefault(out, "IngestionMode", defaultIngestionMode)
	setDefault(out, "publicNetworkAccessForIngestion", accessEnabled)
	setDefault(out, "publicNetworkAccessForQuery", accessEnabled)

	return out
}

func setDefault(m map[string]any, key string, val any) {
	if _, ok := m[key]; !ok {
		m[key] = val
	}
}

// mergePatchProps overlays a PATCH body's writable properties onto the stored
// ones under nil-mask semantics: a key the caller omits is preserved, a supplied
// one applies. Computed keys in the patch are ignored (they are re-injected from
// state on read).
func mergePatchProps(stored, patch map[string]any) map[string]any {
	out := make(map[string]any, len(stored)+len(patch))
	for k, v := range stored {
		out[k] = v
	}

	for k, v := range patch {
		if _, computed := computedKeys[strings.ToLower(k)]; computed {
			continue
		}

		out[k] = v
	}

	return out
}

// toResponse renders a stored component, injecting the computed keys from state
// so InstrumentationKey/AppId/ConnectionString/ApplicationId are stable across
// every read.
func toResponse(cs *componentState) componentResponse {
	props := make(map[string]any, len(cs.Props)+computedKeyCount)
	for k, v := range cs.Props {
		props[k] = v
	}

	props["ApplicationId"] = cs.Name
	props["AppId"] = cs.AppID
	props["InstrumentationKey"] = cs.InstrumentationKey
	props["ConnectionString"] = connectionString(cs)
	props["provisioningState"] = provisioningSucceeded
	props["CreationDate"] = cs.CreationDate
	props["TenantId"] = cs.TenantID

	return componentResponse{
		ID:         azurearm.BuildResourceID(cs.Subscription, cs.ResourceGroup, providerName, typeComponent, cs.Name),
		Name:       cs.Name,
		Type:       armType,
		Location:   cs.Location,
		Kind:       cs.Kind,
		Tags:       cs.Tags,
		Properties: props,
	}
}

// connectionString builds the modern Application Insights connection string from
// the component's stored instrumentation key, region and app id.
func connectionString(cs *componentState) string {
	region := regionCode(cs.Location)

	return "InstrumentationKey=" + cs.InstrumentationKey +
		";IngestionEndpoint=https://" + region + ".in.applicationinsights.azure.com/" +
		";LiveEndpoint=https://" + region + ".livediagnostics.monitor.azure.com/" +
		";ApplicationId=" + cs.AppID
}

// regionCode normalizes a display location ("South Central US") into the
// endpoint region token ("southcentralus") Azure embeds in the connection
// string's ingestion/live endpoints.
func regionCode(location string) string {
	code := strings.ToLower(strings.ReplaceAll(location, " ", ""))
	if code == "" {
		return "westus2"
	}

	return code
}
