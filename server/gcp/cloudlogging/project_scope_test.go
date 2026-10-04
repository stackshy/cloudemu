package cloudlogging_test

import (
	"context"
	"testing"
	"time"

	logging "google.golang.org/api/logging/v2"
)

// TestProjectScopeMetricsIsolated guards that a log-based metric name is
// unique per project: the same name in two projects is two metrics, and a
// delete in one leaves the other alone.
func TestProjectScopeMetricsIsolated(t *testing.T) {
	svc := newLoggingService(t)
	ctx := context.Background()

	filters := map[string]string{"p-a": `severity>=ERROR`, "p-b": `severity>=WARNING`}

	for project, filter := range filters {
		if _, err := svc.Projects.Metrics.Create("projects/"+project, &logging.LogMetric{
			Name: "shared", Filter: filter,
		}).Context(ctx).Do(); err != nil {
			t.Fatalf("Metrics.Create in %s: %v", project, err)
		}
	}

	for project, filter := range filters {
		got, err := svc.Projects.Metrics.Get("projects/" + project + "/metrics/shared").Context(ctx).Do()
		if err != nil {
			t.Fatalf("Metrics.Get in %s: %v", project, err)
		}

		if got.Filter != filter {
			t.Errorf("%s filter = %q, want %q", project, got.Filter, filter)
		}

		list, err := svc.Projects.Metrics.List("projects/" + project).Context(ctx).Do()
		if err != nil {
			t.Fatalf("Metrics.List in %s: %v", project, err)
		}

		if len(list.Metrics) != 1 {
			t.Errorf("%s lists %d metrics, want 1", project, len(list.Metrics))
		}
	}

	if _, err := svc.Projects.Metrics.Delete("projects/p-a/metrics/shared").Context(ctx).Do(); err != nil {
		t.Fatalf("Metrics.Delete in p-a: %v", err)
	}

	if _, err := svc.Projects.Metrics.Get("projects/p-b/metrics/shared").Context(ctx).Do(); err != nil {
		t.Fatalf("p-b metric gone after deleting p-a's: %v", err)
	}
}

// TestProjectScopeSinksIsolated guards that a sink name is unique per project.
func TestProjectScopeSinksIsolated(t *testing.T) {
	svc := newLoggingService(t)
	ctx := context.Background()

	dests := map[string]string{
		"p-a": "storage.googleapis.com/bucket-a",
		"p-b": "storage.googleapis.com/bucket-b",
	}

	for project, dest := range dests {
		if _, err := svc.Projects.Sinks.Create("projects/"+project, &logging.LogSink{
			Name: "shared", Destination: dest,
		}).Context(ctx).Do(); err != nil {
			t.Fatalf("Sinks.Create in %s: %v", project, err)
		}
	}

	for project, dest := range dests {
		got, err := svc.Projects.Sinks.Get("projects/" + project + "/sinks/shared").Context(ctx).Do()
		if err != nil {
			t.Fatalf("Sinks.Get in %s: %v", project, err)
		}

		if got.Destination != dest {
			t.Errorf("%s destination = %q, want %q", project, got.Destination, dest)
		}
	}

	if _, err := svc.Projects.Sinks.Delete("projects/p-a/sinks/shared").Context(ctx).Do(); err != nil {
		t.Fatalf("Sinks.Delete in p-a: %v", err)
	}

	if _, err := svc.Projects.Sinks.Get("projects/p-b/sinks/shared").Context(ctx).Do(); err != nil {
		t.Fatalf("p-b sink gone after deleting p-a's: %v", err)
	}
}

// TestProjectScopeEntriesIsolated guards that entries written to one project's
// logs are not listed under another project.
func TestProjectScopeEntriesIsolated(t *testing.T) {
	svc := newLoggingService(t)
	ctx := context.Background()

	now := time.Now().UTC()
	writeEntry(t, svc, "projects/p-a/logs/app", "from-a", now)
	writeEntry(t, svc, "projects/p-b/logs/other", "from-b", now)

	resp, err := svc.Entries.List(&logging.ListLogEntriesRequest{
		ResourceNames: []string{"projects/p-b"},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Entries.List in p-b: %v", err)
	}

	if len(resp.Entries) != 1 || resp.Entries[0].TextPayload != "from-b" {
		t.Fatalf("p-b entries = %+v, want only from-b", resp.Entries)
	}
}
