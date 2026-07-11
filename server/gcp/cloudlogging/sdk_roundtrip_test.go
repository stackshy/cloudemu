package cloudlogging_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	logging "google.golang.org/api/logging/v2"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu"
	gcpserver "github.com/stackshy/cloudemu/server/gcp"
)

const testProject = "demo"

func newLoggingService(t *testing.T) *logging.Service {
	t.Helper()

	cloud := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.Drivers{CloudLogging: cloud.CloudLogging})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	svc, err := logging.NewService(context.Background(),
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("logging.NewService: %v", err)
	}

	return svc
}

func TestSDKCloudLoggingWriteAndList(t *testing.T) {
	svc := newLoggingService(t)
	ctx := context.Background()

	logName := "projects/" + testProject + "/logs/app-log"
	base := time.Now().UTC().Truncate(time.Millisecond)

	if _, err := svc.Entries.Write(&logging.WriteLogEntriesRequest{
		LogName: logName,
		Resource: &logging.MonitoredResource{
			Type:   "global",
			Labels: map[string]string{"project_id": testProject},
		},
		Entries: []*logging.LogEntry{
			{Timestamp: base.Format(time.RFC3339Nano), TextPayload: "hello world"},
			{Timestamp: base.Add(time.Second).Format(time.RFC3339Nano), TextPayload: "error: boom"},
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

	if len(resp.Entries) != 2 {
		t.Fatalf("got %d entries, want 2: %+v", len(resp.Entries), resp.Entries)
	}

	if resp.Entries[0].TextPayload != "hello world" {
		t.Fatalf("first entry payload = %q, want hello world", resp.Entries[0].TextPayload)
	}

	if resp.Entries[0].LogName != logName {
		t.Fatalf("first entry logName = %q, want %q", resp.Entries[0].LogName, logName)
	}
}

func TestSDKCloudLoggingLogsLifecycle(t *testing.T) {
	svc := newLoggingService(t)
	ctx := context.Background()

	// A write lazily creates the log.
	if _, err := svc.Entries.Write(&logging.WriteLogEntriesRequest{
		LogName: "projects/" + testProject + "/logs/svc-log",
		Entries: []*logging.LogEntry{
			{TextPayload: "started"},
		},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Entries.Write: %v", err)
	}

	list, err := svc.Projects.Logs.List("projects/" + testProject).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Projects.Logs.List: %v", err)
	}

	found := false
	for _, n := range list.LogNames {
		if n == "projects/"+testProject+"/logs/svc-log" {
			found = true
		}
	}

	if !found {
		t.Fatalf("Logs.List = %v, want it to contain svc-log", list.LogNames)
	}

	if _, err := svc.Projects.Logs.Delete("projects/" + testProject + "/logs/svc-log").Context(ctx).Do(); err != nil {
		t.Fatalf("Projects.Logs.Delete: %v", err)
	}

	after, err := svc.Projects.Logs.List("projects/" + testProject).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Projects.Logs.List after delete: %v", err)
	}

	for _, n := range after.LogNames {
		if n == "projects/"+testProject+"/logs/svc-log" {
			t.Fatalf("svc-log still present after delete: %v", after.LogNames)
		}
	}
}

func TestSDKCloudLoggingErrors(t *testing.T) {
	svc := newLoggingService(t)
	ctx := context.Background()

	// Listing entries for a log that was never written is a not-found.
	_, err := svc.Entries.List(&logging.ListLogEntriesRequest{
		ResourceNames: []string{"projects/" + testProject},
		Filter:        `logName="projects/` + testProject + `/logs/missing"`,
	}).Context(ctx).Do()
	if err == nil {
		t.Fatal("Entries.List(missing log): want error, got nil")
	}
}
