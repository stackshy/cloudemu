package aps

import (
	"encoding/json"
	"time"

	"github.com/stackshy/cloudemu/v2/services/aps/driver"
)

// epochOrNil renders a time as a Unix-epoch-seconds float the APS SDK decodes
// into a *time.Time, or nil for the zero time.
func epochOrNil(t time.Time) *float64 {
	if t.IsZero() {
		return nil
	}

	secs := float64(t.Unix())

	return &secs
}

// statusBlock renders a {statusCode: ...} status object.
func statusBlock(code string) map[string]any {
	return map[string]any{"statusCode": code}
}

// workspaceToWire renders a workspace as its DescribeWorkspace
// WorkspaceDescription object.
func workspaceToWire(ws *driver.Workspace) map[string]any {
	out := map[string]any{
		"arn":                ws.Arn,
		"workspaceId":        ws.WorkspaceID,
		"status":             statusBlock(ws.Status),
		"createdAt":          epochOrNil(ws.CreatedAt),
		"prometheusEndpoint": ws.PrometheusEndpoint,
	}

	if ws.Alias != "" {
		out["alias"] = ws.Alias
	}

	if ws.KmsKeyArn != "" {
		out["kmsKeyArn"] = ws.KmsKeyArn
	}

	if ws.Tags != nil {
		out["tags"] = ws.Tags
	}

	return out
}

// workspaceSummaryToWire renders a workspace as its ListWorkspaces
// WorkspaceSummary object (no prometheusEndpoint).
func workspaceSummaryToWire(ws *driver.Workspace) map[string]any {
	out := map[string]any{
		"arn":         ws.Arn,
		"workspaceId": ws.WorkspaceID,
		"status":      statusBlock(ws.Status),
		"createdAt":   epochOrNil(ws.CreatedAt),
	}

	if ws.Alias != "" {
		out["alias"] = ws.Alias
	}

	if ws.KmsKeyArn != "" {
		out["kmsKeyArn"] = ws.KmsKeyArn
	}

	if ws.Tags != nil {
		out["tags"] = ws.Tags
	}

	return out
}

// ruleGroupsNamespaceToWire renders a namespace as its DescribeRuleGroupsNamespace
// RuleGroupsNamespaceDescription object. Data is the base64 string carried
// verbatim, emitted so the SDK base64-decodes it back to the exact bytes.
func ruleGroupsNamespaceToWire(ns *driver.RuleGroupsNamespace) map[string]any {
	out := map[string]any{
		"arn":        ns.Arn,
		"name":       ns.Name,
		"data":       ns.Data,
		"status":     statusBlock(ns.Status),
		"createdAt":  epochOrNil(ns.CreatedAt),
		"modifiedAt": epochOrNil(ns.ModifiedAt),
	}

	if ns.Tags != nil {
		out["tags"] = ns.Tags
	}

	return out
}

// ruleGroupsNamespaceSummaryToWire renders a namespace as its
// ListRuleGroupsNamespaces RuleGroupsNamespaceSummary object (no data).
func ruleGroupsNamespaceSummaryToWire(ns *driver.RuleGroupsNamespace) map[string]any {
	out := map[string]any{
		"arn":        ns.Arn,
		"name":       ns.Name,
		"status":     statusBlock(ns.Status),
		"createdAt":  epochOrNil(ns.CreatedAt),
		"modifiedAt": epochOrNil(ns.ModifiedAt),
	}

	if ns.Tags != nil {
		out["tags"] = ns.Tags
	}

	return out
}

// alertManagerToWire renders the alert-manager definition as its
// DescribeAlertManagerDefinition AlertManagerDefinitionDescription object.
func alertManagerToWire(d *driver.AlertManagerDefinition) map[string]any {
	return map[string]any{
		"data":       d.Data,
		"status":     statusBlock(d.Status),
		"createdAt":  epochOrNil(d.CreatedAt),
		"modifiedAt": epochOrNil(d.ModifiedAt),
	}
}

// loggingToWire renders the logging configuration as its
// DescribeLoggingConfiguration LoggingConfigurationMetadata object.
func loggingToWire(workspaceID string, l *driver.LoggingConfiguration) map[string]any {
	return map[string]any{
		"workspace":   workspaceID,
		"logGroupArn": l.LogGroupArn,
		"status":      statusBlock(l.Status),
		"createdAt":   epochOrNil(l.CreatedAt),
		"modifiedAt":  epochOrNil(l.ModifiedAt),
	}
}

// stringField extracts a modeled string field from a raw request body.
func stringField(raw map[string]json.RawMessage, key string) string {
	v, ok := raw[key]
	if !ok {
		return ""
	}

	var s string
	if json.Unmarshal(v, &s) != nil {
		return ""
	}

	return s
}

// tagsFromBody extracts the modeled tags map from a raw request body.
func tagsFromBody(raw map[string]json.RawMessage) map[string]string {
	v, ok := raw["tags"]
	if !ok {
		return nil
	}

	var tags map[string]string
	if json.Unmarshal(v, &tags) != nil {
		return nil
	}

	return tags
}
