package cloudlogging_test

import (
	"context"
	"testing"
	"time"

	logging "google.golang.org/api/logging/v2"
)

// writeEntry writes a single text entry to logName at ts.
func writeEntry(t *testing.T, svc *logging.Service, logName, text string, ts time.Time) {
	t.Helper()

	if _, err := svc.Entries.Write(&logging.WriteLogEntriesRequest{
		LogName: logName,
		Entries: []*logging.LogEntry{
			{Timestamp: ts.Format(time.RFC3339Nano), TextPayload: text},
		},
	}).Context(context.Background()).Do(); err != nil {
		t.Fatalf("Entries.Write: %v", err)
	}
}

// TestSDKEntriesListWithoutLogNameFilter guards the fix for the logName-optional
// finding: entries.list with only resourceNames (no logName= filter) must read
// across every log in the project, not 400.
func TestSDKEntriesListWithoutLogNameFilter(t *testing.T) {
	svc := newLoggingService(t)
	ctx := context.Background()

	base := time.Now().UTC().Truncate(time.Millisecond)
	writeEntry(t, svc, "projects/"+testProject+"/logs/app-a", "from-a", base)
	writeEntry(t, svc, "projects/"+testProject+"/logs/app-b", "from-b", base.Add(time.Second))

	resp, err := svc.Entries.List(&logging.ListLogEntriesRequest{
		ResourceNames: []string{"projects/" + testProject},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Entries.List without logName filter: %v", err)
	}

	if len(resp.Entries) != 2 {
		t.Fatalf("read-all returned %d entries, want 2: %+v", len(resp.Entries), resp.Entries)
	}

	logs := map[string]bool{}
	for _, e := range resp.Entries {
		logs[e.LogName] = true
	}

	if !logs["projects/"+testProject+"/logs/app-a"] || !logs["projects/"+testProject+"/logs/app-b"] {
		t.Fatalf("read-all did not span both logs: %v", logs)
	}
}

// TestSDKEntriesListMetadataFields guards insertId, resource and receiveTimestamp
// being populated on read-back.
func TestSDKEntriesListMetadataFields(t *testing.T) {
	svc := newLoggingService(t)
	ctx := context.Background()

	logName := "projects/" + testProject + "/logs/meta"
	base := time.Now().UTC().Truncate(time.Millisecond)

	// An explicit resource must round-trip; the writer supplies no insertId, so
	// the server must assign one.
	if _, err := svc.Entries.Write(&logging.WriteLogEntriesRequest{
		LogName: logName,
		Resource: &logging.MonitoredResource{
			Type:   "gce_instance",
			Labels: map[string]string{"instance_id": "i-123", "zone": "us-central1-a"},
		},
		Entries: []*logging.LogEntry{
			{Timestamp: base.Format(time.RFC3339Nano), TextPayload: "hello"},
		},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Entries.Write: %v", err)
	}

	resp, err := svc.Entries.List(&logging.ListLogEntriesRequest{
		ResourceNames: []string{"projects/" + testProject},
		Filter:        `logName="` + logName + `"`,
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Entries.List: %v", err)
	}

	if len(resp.Entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(resp.Entries))
	}

	e := resp.Entries[0]
	if e.InsertId == "" {
		t.Error("insertId was not populated")
	}

	if e.ReceiveTimestamp == "" {
		t.Error("receiveTimestamp was not populated")
	}

	if e.Resource == nil || e.Resource.Type != "gce_instance" {
		t.Fatalf("resource did not round-trip: %+v", e.Resource)
	}

	if e.Resource.Labels["instance_id"] != "i-123" {
		t.Errorf("resource labels lost: %v", e.Resource.Labels)
	}
}

// TestSDKEntriesListDefaultResource guards that an entry written with no resource
// still reads back the default "global" MonitoredResource.
func TestSDKEntriesListDefaultResource(t *testing.T) {
	svc := newLoggingService(t)
	ctx := context.Background()

	logName := "projects/" + testProject + "/logs/no-resource"
	writeEntry(t, svc, logName, "x", time.Now().UTC())

	resp, err := svc.Entries.List(&logging.ListLogEntriesRequest{
		ResourceNames: []string{"projects/" + testProject},
		Filter:        `logName="` + logName + `"`,
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Entries.List: %v", err)
	}

	if len(resp.Entries) != 1 || resp.Entries[0].Resource == nil {
		t.Fatalf("expected a default resource, got %+v", resp.Entries)
	}

	if resp.Entries[0].Resource.Type != "global" {
		t.Errorf("default resource type = %q, want global", resp.Entries[0].Resource.Type)
	}
}

// TestSDKEntriesListPagination guards pageSize + nextPageToken paging.
func TestSDKEntriesListPagination(t *testing.T) {
	svc := newLoggingService(t)
	ctx := context.Background()

	logName := "projects/" + testProject + "/logs/paged"
	base := time.Now().UTC().Truncate(time.Millisecond)
	for i := range 3 {
		writeEntry(t, svc, logName, "entry", base.Add(time.Duration(i)*time.Second))
	}

	first, err := svc.Entries.List(&logging.ListLogEntriesRequest{
		ResourceNames: []string{"projects/" + testProject},
		Filter:        `logName="` + logName + `"`,
		PageSize:      1,
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Entries.List page 1: %v", err)
	}

	if len(first.Entries) != 1 {
		t.Fatalf("page 1 returned %d entries, want 1", len(first.Entries))
	}

	if first.NextPageToken == "" {
		t.Fatal("page 1 had no nextPageToken with more entries remaining")
	}

	// Walk all pages and confirm every entry is seen exactly once.
	seen := len(first.Entries)
	token := first.NextPageToken
	for token != "" {
		page, err := svc.Entries.List(&logging.ListLogEntriesRequest{
			ResourceNames: []string{"projects/" + testProject},
			Filter:        `logName="` + logName + `"`,
			PageSize:      1,
			PageToken:     token,
		}).Context(ctx).Do()
		if err != nil {
			t.Fatalf("Entries.List page token=%q: %v", token, err)
		}

		seen += len(page.Entries)
		token = page.NextPageToken
	}

	if seen != 3 {
		t.Fatalf("paged through %d entries, want 3", seen)
	}
}
