package cloudlogging_test

import (
	"context"
	"testing"

	logging "google.golang.org/api/logging/v2"
)

// TestSDKSinksLifecycle guards create/get/list/update/delete of export sinks.
func TestSDKSinksLifecycle(t *testing.T) {
	svc := newLoggingService(t)
	ctx := context.Background()

	parent := "projects/" + testProject
	sinkName := parent + "/sinks/my-sink"

	created, err := svc.Projects.Sinks.Create(parent, &logging.LogSink{
		Name:        "my-sink",
		Destination: "storage.googleapis.com/my-bucket",
		Filter:      `severity>=ERROR`,
		Description: "errors to GCS",
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Sinks.Create: %v", err)
	}

	if created.Name != "my-sink" {
		t.Errorf("created sink name = %q, want my-sink", created.Name)
	}

	if created.WriterIdentity == "" {
		t.Error("created sink has no writerIdentity")
	}

	got, err := svc.Projects.Sinks.Get(sinkName).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Sinks.Get: %v", err)
	}

	if got.Destination != "storage.googleapis.com/my-bucket" {
		t.Errorf("get destination = %q", got.Destination)
	}

	list, err := svc.Projects.Sinks.List(parent).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Sinks.List: %v", err)
	}

	if len(list.Sinks) != 1 || list.Sinks[0].Name != "my-sink" {
		t.Fatalf("Sinks.List = %+v, want one my-sink", list.Sinks)
	}

	updated, err := svc.Projects.Sinks.Update(sinkName, &logging.LogSink{
		Name:        "my-sink",
		Destination: "storage.googleapis.com/other-bucket",
		Filter:      `severity>=WARNING`,
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Sinks.Update: %v", err)
	}

	if updated.Destination != "storage.googleapis.com/other-bucket" {
		t.Errorf("updated destination = %q", updated.Destination)
	}

	if _, err := svc.Projects.Sinks.Delete(sinkName).Context(ctx).Do(); err != nil {
		t.Fatalf("Sinks.Delete: %v", err)
	}

	after, err := svc.Projects.Sinks.List(parent).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Sinks.List after delete: %v", err)
	}

	if len(after.Sinks) != 0 {
		t.Fatalf("sink still present after delete: %+v", after.Sinks)
	}
}

// TestSDKSinksGetMissing guards a not-found error for an unknown sink.
func TestSDKSinksGetMissing(t *testing.T) {
	svc := newLoggingService(t)

	_, err := svc.Projects.Sinks.Get("projects/" + testProject + "/sinks/nope").
		Context(context.Background()).Do()
	if err == nil {
		t.Fatal("Sinks.Get(missing): want error, got nil")
	}
}
