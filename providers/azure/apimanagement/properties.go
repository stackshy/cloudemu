package apimanagement

import (
	"encoding/json"
	"strings"
	"time"
)

const (
	// platformDedicated / platformConsumption are the computePlatform versions
	// Azure reports for the dedicated tiers and for the Consumption tier.
	platformDedicated   = "stv2"
	platformConsumption = "mtv1"

	// notificationSenderDefault is the sender address Azure assigns when the
	// caller sets none.
	notificationSenderDefault = "apimgmt-noreply@mail.windowsazure.com"
)

// Computed property keys.
const (
	propProvisioningState       = "provisioningState"
	propTargetProvisioningState = "targetProvisioningState"
	propCreatedAt               = "createdAtUtc"
	propGatewayURL              = "gatewayUrl"
	propPublicIPs               = "publicIPAddresses"
	propPlatformVersion         = "platformVersion"
	propRegionalGatewayURL      = "gatewayRegionalUrl"
	propPortalURL               = "portalUrl"
	propDeveloperPortalURL      = "developerPortalUrl"
	propManagementAPIURL        = "managementApiUrl"
	propScmURL                  = "scmUrl"
)

// defaultProperties are the writable properties Azure fills in when the caller
// leaves them unset. A caller-supplied value always wins.
func defaultProperties() map[string]any {
	return map[string]any{
		"virtualNetworkType":      "None",
		"publicNetworkAccess":     "Enabled",
		"notificationSenderEmail": notificationSenderDefault,
		"disableGateway":          false,
		"customProperties":        map[string]any{},
	}
}

// computedProperties returns the read-only properties Azure mints: the
// provisioning state, creation time, platform version and the endpoint URLs.
// The Consumption tier has only a gateway, so its regional gateway, portal,
// management and SCM endpoints are absent. A computed value always overwrites
// whatever the caller sent for that key.
func (s *Service) computedProperties() map[string]any {
	out := map[string]any{
		propProvisioningState:       s.ProvisioningState,
		propTargetProvisioningState: "",
		propCreatedAt:               s.CreatedAt.UTC().Format(time.RFC3339),
		propGatewayURL:              s.Endpoints().Gateway,
		propPublicIPs:               []string{},
		propPlatformVersion:         platformDedicated,
	}

	if s.SkuName == skuConsumption {
		out[propPlatformVersion] = platformConsumption

		return out
	}

	ep := s.Endpoints()
	out[propRegionalGatewayURL] = s.RegionalGatewayURL()
	out[propPortalURL] = ep.Portal
	out[propDeveloperPortalURL] = ep.DeveloperPortal
	out[propManagementAPIURL] = ep.ManagementAPI
	out[propScmURL] = ep.Scm

	return out
}

// computedKeys lists every property key computedProperties can emit, so a
// re-materialization after a SKU change drops the endpoints the new tier does
// not have (e.g. portalUrl after a move to Consumption).
//
//nolint:gochecknoglobals // static key list
var computedKeys = []string{
	propProvisioningState, propTargetProvisioningState, propCreatedAt, propGatewayURL, propPublicIPs,
	propPlatformVersion, propRegionalGatewayURL, propPortalURL, propDeveloperPortalURL,
	propManagementAPIURL, propScmURL,
}

// RegionalGatewayURL renders the primary region's gateway endpoint,
// https://<name>-<region>-01.regional.azure-api.net.
func (s *Service) RegionalGatewayURL() string {
	return "https://" + strings.ToLower(s.Name) + "-" + normalizeLocation(s.Location) + "-01.regional.azure-api.net"
}

// materializeProperties rebuilds s.Properties as the full block Azure returns:
// the caller's writable properties, Azure's defaults for the writable fields
// the caller left unset, and the computed read-only fields. It runs on every
// write (and on restore), so the Go library and the HTTP server hand back the
// same resource. The restore flag is a request-only switch: it always
// reads back false, as in Azure.
func (s *Service) materializeProperties() {
	obj := map[string]any{}
	if len(s.Properties) > 0 {
		if err := json.Unmarshal(s.Properties, &obj); err != nil {
			obj = map[string]any{}
		}
	}

	obj["restore"] = false

	for _, k := range computedKeys {
		delete(obj, k)
	}

	for k, v := range defaultProperties() {
		if cur, set := obj[k]; !set || cur == nil {
			obj[k] = v
		}
	}

	for k, v := range s.computedProperties() {
		obj[k] = v
	}

	raw, err := json.Marshal(obj)
	if err != nil {
		return
	}

	s.Properties = raw
}

// normalizeLocation folds an ARM location display name ("East US") to its
// programmatic form ("eastus"), the form ARM compares locations in.
func normalizeLocation(loc string) string {
	return strings.ToLower(strings.ReplaceAll(loc, " ", ""))
}
