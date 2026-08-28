package gcp_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
	logdriver "github.com/stackshy/cloudemu/v2/services/logging/driver"
)

const auditLogName = "cloudaudit.googleapis.com/activity"

// TestAuditLogRecordsMutatingOp verifies a mutating GCP API operation is
// auto-recorded as a Cloud Audit Log entry in Cloud Logging — the GCP analogue
// of AWS CloudTrail LookupEvents reflecting a mutating call.
func TestAuditLogRecordsMutatingOp(t *testing.T) {
	p := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.DriversFrom(p))

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	// PUT a Pub/Sub topic — a mutating operation the server handles.
	code, _ := do(t, ts, http.MethodPut, "/v1/projects/demo/topics/audit-topic", `{}`)
	if code < 200 || code >= 300 {
		t.Fatalf("create topic: status %d", code)
	}

	events, err := p.CloudLogging.GetLogEvents(context.Background(), &logdriver.LogQueryInput{
		LogGroup: auditLogName,
	})
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}

	if len(events) == 0 {
		t.Fatal("expected an audit log entry for the mutating op, got none")
	}

	joined := ""
	for i := range events {
		joined += events[i].Message
	}

	if !strings.Contains(joined, "AuditLog") || !strings.Contains(joined, "topics/audit-topic") {
		t.Fatalf("audit entry missing expected fields: %s", joined)
	}
}

// TestAuditLogSkipsReads verifies a read-only (GET) operation is NOT recorded —
// Admin Activity audit logs record writes/deletes/actions, not reads.
func TestAuditLogSkipsReads(t *testing.T) {
	p := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.DriversFrom(p))

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	// A GET (list topics) must not produce an audit entry.
	do(t, ts, http.MethodGet, "/v1/projects/demo/topics", "")

	events, err := p.CloudLogging.GetLogEvents(context.Background(), &logdriver.LogQueryInput{
		LogGroup: auditLogName,
	})
	// The audit log group may not exist yet (no writes) — a not-found is
	// equivalent to "no audit entries".
	if err == nil && len(events) != 0 {
		t.Fatalf("expected no audit entries for a read, got %d", len(events))
	}
}
