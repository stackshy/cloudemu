package grafana

import (
	"time"

	"github.com/stackshy/cloudemu/v2/services/grafana/driver"
)

// epochOrNil renders a time as a Unix-epoch-seconds float the Grafana SDK
// decodes into a *time.Time, or nil for the zero time.
func epochOrNil(t time.Time) *float64 {
	if t.IsZero() {
		return nil
	}

	secs := float64(t.Unix())

	return &secs
}

// authenticationSummary renders a workspace's AuthenticationSummary object.
func authenticationSummary(w *driver.Workspace) map[string]any {
	return map[string]any{
		"providers":               orEmptyStrings(w.AuthenticationProviders),
		"samlConfigurationStatus": w.SamlConfigurationStatus,
	}
}

// workspaceToWire renders a workspace as its WorkspaceDescription restJson1
// object. WorkspaceDescription carries no arn field; the SDK and IaC tools
// derive the arn from region + account + id.
func workspaceToWire(w *driver.Workspace) map[string]any {
	out := map[string]any{
		"id":                w.ID,
		"name":              w.Name,
		"description":       w.Description,
		"status":            w.Status,
		"endpoint":          w.Endpoint,
		"grafanaVersion":    w.GrafanaVersion,
		"created":           epochOrNil(w.Created),
		"modified":          epochOrNil(w.Modified),
		"accountAccessType": w.AccountAccessType,
		"permissionType":    w.PermissionType,
		"authentication":    authenticationSummary(w),
		"dataSources":       orEmptyStrings(w.DataSources),
		"freeTrialConsumed": false,
	}

	if len(w.NotificationDestinations) > 0 {
		out["notificationDestinations"] = w.NotificationDestinations
	}

	if len(w.OrganizationalUnits) > 0 {
		out["organizationalUnits"] = w.OrganizationalUnits
	}

	putNonEmpty(out, "organizationRoleName", w.OrganizationRoleName)
	putNonEmpty(out, "workspaceRoleArn", w.WorkspaceRoleArn)
	putNonEmpty(out, "stackSetName", w.StackSetName)

	if len(w.VpcConfiguration) > 0 {
		out["vpcConfiguration"] = w.VpcConfiguration
	}

	if len(w.NetworkAccessControl) > 0 {
		out["networkAccessControl"] = w.NetworkAccessControl
	}

	if w.Tags != nil {
		out["tags"] = w.Tags
	}

	return out
}

// workspaceSummaryToWire renders a workspace as its ListWorkspaces
// WorkspaceSummary object.
func workspaceSummaryToWire(w *driver.Workspace) map[string]any {
	out := map[string]any{
		"id":             w.ID,
		"name":           w.Name,
		"description":    w.Description,
		"status":         w.Status,
		"endpoint":       w.Endpoint,
		"grafanaVersion": w.GrafanaVersion,
		"created":        epochOrNil(w.Created),
		"modified":       epochOrNil(w.Modified),
		"authentication": authenticationSummary(w),
	}

	if len(w.NotificationDestinations) > 0 {
		out["notificationDestinations"] = w.NotificationDestinations
	}

	if w.Tags != nil {
		out["tags"] = w.Tags
	}

	return out
}

// putNonEmpty sets key to val only when val is non-empty, so an optional field
// the caller never set is omitted from the wire object.
func putNonEmpty(m map[string]any, key, val string) {
	if val != "" {
		m[key] = val
	}
}

// orEmptyStrings returns in, or an empty (non-nil) slice, so a required list
// field always serializes as [] rather than null.
func orEmptyStrings(in []string) []string {
	if in == nil {
		return []string{}
	}

	return in
}
