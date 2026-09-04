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

// TestSDKSinksWriterIdentity guards the writer-identity contract a real
// Terraform/gcloud user depends on: uniqueWriterIdentity=true yields an identity
// distinct from the shared non-unique account (so terraform-google infers
// unique_writer_identity=true and sees no drift), the default yields the shared
// account, and customWriterIdentity is honored verbatim.
func TestSDKSinksWriterIdentity(t *testing.T) {
	svc := newLoggingService(t)
	ctx := context.Background()
	parent := "projects/" + testProject

	const nonUnique = "serviceAccount:cloud-logs@system.gserviceaccount.com"

	unique, err := svc.Projects.Sinks.Create(parent, &logging.LogSink{
		Name: "uniq", Destination: "storage.googleapis.com/b",
	}).UniqueWriterIdentity(true).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Sinks.Create unique: %v", err)
	}

	if unique.WriterIdentity == "" || unique.WriterIdentity == nonUnique {
		t.Errorf("unique writerIdentity = %q, want a distinct non-shared account", unique.WriterIdentity)
	}

	shared, err := svc.Projects.Sinks.Create(parent, &logging.LogSink{
		Name: "shared", Destination: "storage.googleapis.com/b",
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Sinks.Create shared: %v", err)
	}

	if shared.WriterIdentity != nonUnique {
		t.Errorf("shared writerIdentity = %q, want %q", shared.WriterIdentity, nonUnique)
	}

	custom, err := svc.Projects.Sinks.Create(parent, &logging.LogSink{
		Name: "cust", Destination: "storage.googleapis.com/b",
	}).CustomWriterIdentity("serviceAccount:me@x.iam.gserviceaccount.com").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Sinks.Create custom: %v", err)
	}

	if custom.WriterIdentity != "serviceAccount:me@x.iam.gserviceaccount.com" {
		t.Errorf("custom writerIdentity = %q", custom.WriterIdentity)
	}
}

// TestSDKSinksMaskedPatch guards updateMask partial semantics: a patch naming
// only "filter" updates the filter and must leave destination, description and
// writerIdentity untouched (a full-replace would silently clear them — the bug
// this test locks down).
func TestSDKSinksMaskedPatch(t *testing.T) {
	svc := newLoggingService(t)
	ctx := context.Background()
	parent := "projects/" + testProject
	sinkName := parent + "/sinks/patch-sink"

	if _, err := svc.Projects.Sinks.Create(parent, &logging.LogSink{
		Name:        "patch-sink",
		Destination: "storage.googleapis.com/keep",
		Filter:      "severity>=ERROR",
		Description: "keep me",
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Sinks.Create: %v", err)
	}

	updated, err := svc.Projects.Sinks.Patch(sinkName, &logging.LogSink{
		Filter: "severity>=WARNING",
	}).UpdateMask("filter").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Sinks.Patch: %v", err)
	}

	if updated.Filter != "severity>=WARNING" {
		t.Errorf("filter = %q, want severity>=WARNING", updated.Filter)
	}

	if updated.Destination != "storage.googleapis.com/keep" {
		t.Errorf("destination cleared by masked patch: %q", updated.Destination)
	}

	if updated.Description != "keep me" {
		t.Errorf("description cleared by masked patch: %q", updated.Description)
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
