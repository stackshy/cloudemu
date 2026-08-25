package cloudlogging_test

import (
	"context"
	"testing"

	logging "google.golang.org/api/logging/v2"
)

// TestSDKLogMetricsLifecycle guards create/get/list/update/delete of log-based
// metrics.
func TestSDKLogMetricsLifecycle(t *testing.T) {
	svc := newLoggingService(t)
	ctx := context.Background()

	parent := "projects/" + testProject
	metricName := parent + "/metrics/error-count"

	created, err := svc.Projects.Metrics.Create(parent, &logging.LogMetric{
		Name:        "error-count",
		Description: "count of error logs",
		Filter:      `severity>=ERROR`,
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Metrics.Create: %v", err)
	}

	if created.Name != "error-count" {
		t.Errorf("created metric name = %q, want error-count", created.Name)
	}

	got, err := svc.Projects.Metrics.Get(metricName).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Metrics.Get: %v", err)
	}

	if got.Filter != `severity>=ERROR` {
		t.Errorf("get filter = %q", got.Filter)
	}

	list, err := svc.Projects.Metrics.List(parent).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Metrics.List: %v", err)
	}

	if len(list.Metrics) != 1 || list.Metrics[0].Name != "error-count" {
		t.Fatalf("Metrics.List = %+v, want one error-count", list.Metrics)
	}

	updated, err := svc.Projects.Metrics.Update(metricName, &logging.LogMetric{
		Name:        "error-count",
		Description: "updated",
		Filter:      `severity>=WARNING`,
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Metrics.Update: %v", err)
	}

	if updated.Filter != `severity>=WARNING` {
		t.Errorf("updated filter = %q", updated.Filter)
	}

	if _, err := svc.Projects.Metrics.Delete(metricName).Context(ctx).Do(); err != nil {
		t.Fatalf("Metrics.Delete: %v", err)
	}

	after, err := svc.Projects.Metrics.List(parent).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Metrics.List after delete: %v", err)
	}

	if len(after.Metrics) != 0 {
		t.Fatalf("metric still present after delete: %+v", after.Metrics)
	}
}
