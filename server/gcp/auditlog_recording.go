package gcp

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/config"
	logdriver "github.com/stackshy/cloudemu/v2/services/logging/driver"
)

const (
	// auditLogGroup is the conventional Cloud Logging log name real Cloud Audit
	// Logs Admin Activity entries are written under. Reading the audit trail is a
	// Cloud Logging entries:list filtered on this logName.
	auditLogGroup = "cloudaudit.googleapis.com/activity"
	// defaultAuditPrincipal is the caller recorded when a request carries no
	// identity the emulator can attribute (auth is accepted but not verified).
	defaultAuditPrincipal = "cloudemu@localhost"
	// genericAuditService is the service name used for API paths rooted at a
	// bare version segment (e.g. "/v1/projects/...") where no service prefix
	// identifies the owning API.
	genericAuditService = "googleapis.com"
)

// recordAuditLogEvent derives a Cloud Audit Log Admin Activity entry from a
// served GCP REST request and writes it into Cloud Logging, so a client reading
// the audit trail sees real API activity. It runs as the server's post-dispatch
// observer. Read-only requests (GET) and Cloud Logging's own operations are
// skipped — Admin Activity logs record writes/deletes/actions, and recording
// logging reads/writes would flood the log with its own traffic.
func recordAuditLogEvent(logs logdriver.Logging, r *http.Request, clock config.Clock) {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return
	}

	if isLoggingOperation(r.URL.Path) {
		return
	}

	service := auditServiceName(r.URL.Path)
	entry := map[string]any{
		"@type":        "type.googleapis.com/google.cloud.audit.AuditLog",
		"serviceName":  service,
		"methodName":   r.Method + " " + r.URL.Path,
		"resourceName": strings.TrimPrefix(r.URL.Path, "/"),
		"authenticationInfo": map[string]any{
			"principalEmail": defaultAuditPrincipal,
		},
	}

	msg, err := json.Marshal(entry)
	if err != nil {
		return
	}

	ctx := context.Background()
	now := clock.Now().UTC()

	_, _ = logs.CreateLogGroup(ctx, logdriver.LogGroupConfig{Name: auditLogGroup})
	_, _ = logs.CreateLogStream(ctx, auditLogGroup, service)
	_ = logs.PutLogEvents(ctx, auditLogGroup, service, []logdriver.LogEvent{
		{Timestamp: now, Message: string(msg)},
	})
}

// isLoggingOperation reports whether a path targets Cloud Logging itself
// (entries:write/list or the projects/{p}/logs family), which must not be
// audit-recorded to avoid the log capturing its own traffic.
func isLoggingOperation(path string) bool {
	return strings.Contains(path, "/entries:") || strings.Contains(path, "/logs")
}

// auditServiceName derives the GCP service name from a REST path, e.g.
// "/compute/v1/projects/..." -> "compute.googleapis.com". Paths rooted at a
// bare version (e.g. "/v1/projects/...") fall back to a generic service.
func auditServiceName(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		return genericAuditService
	}

	first := parts[0]
	if first == "v1" || first == "v2" || first == "v3" || strings.HasPrefix(first, "v1") {
		return genericAuditService
	}

	return first + ".googleapis.com"
}
