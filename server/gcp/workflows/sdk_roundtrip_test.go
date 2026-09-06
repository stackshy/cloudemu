package workflows_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
	workflows "google.golang.org/api/workflows/v1"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

func newSDKClient(t *testing.T) (*workflows.Service, string) {
	t.Helper()

	cloud := cloudemu.NewGCP()
	srv := gcpserver.NewFromProvider(cloud)

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	svc, err := workflows.NewService(context.Background(),
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("workflows.NewService: %v", err)
	}

	return svc, "mock-project"
}

// TestSDKWorkflowLifecycle drives the real google.golang.org/api workflows client
// end to end: create (LRO must complete, not hang), get (computed fields stable),
// patch, and delete.
func TestSDKWorkflowLifecycle(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()
	location := "us-central1"
	parent := "projects/" + project + "/locations/" + location
	name := parent + "/workflows/wf"

	want := &workflows.Workflow{
		Description:    "greeter",
		SourceContents: "main:\n  steps:\n    - a:\n        return: hi",
		ServiceAccount: "sa@mock-project.iam.gserviceaccount.com",
		CallLogLevel:   "LOG_ALL_CALLS",
		Labels:         map[string]string{"team": "platform"},
		UserEnvVars:    map[string]string{"ENV": "test"},
	}

	op, err := svc.Projects.Locations.Workflows.Create(parent, want).WorkflowId("wf").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Workflows.Create: %v", err)
	}

	if !op.Done {
		t.Fatalf("create operation not done (would hang a Terraform apply)")
	}

	polled, err := svc.Projects.Locations.Operations.Get(op.Name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Operations.Get: %v", err)
	}

	if !polled.Done {
		t.Fatalf("polled operation not done")
	}

	got, err := svc.Projects.Locations.Workflows.Get(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Workflows.Get: %v", err)
	}

	if got.Name != name || got.State != "ACTIVE" || got.RevisionId == "" {
		t.Fatalf("unexpected workflow: %+v", got)
	}

	if got.Description != want.Description || got.SourceContents != want.SourceContents ||
		got.ServiceAccount != want.ServiceAccount || got.CallLogLevel != want.CallLogLevel ||
		got.Labels["team"] != "platform" || got.UserEnvVars["ENV"] != "test" {
		t.Fatalf("body round-trip mismatch: %+v", got)
	}

	if got.CreateTime == "" || got.UpdateTime == "" || got.RevisionCreateTime == "" {
		t.Fatalf("computed timestamps missing: %+v", got)
	}

	// Second Get -> byte-stable computed fields (no Terraform drift).
	got2, err := svc.Projects.Locations.Workflows.Get(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Workflows.Get(2): %v", err)
	}

	if got2.RevisionId != got.RevisionId || got2.CreateTime != got.CreateTime ||
		got2.UpdateTime != got.UpdateTime || got2.State != got.State {
		t.Fatalf("computed fields drifted across reads: %+v vs %+v", got2, got)
	}

	// Delete completes as a done LRO.
	delOp, err := svc.Projects.Locations.Workflows.Delete(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Workflows.Delete: %v", err)
	}

	if !delOp.Done {
		t.Fatalf("delete operation not done")
	}

	if _, err := svc.Projects.Locations.Workflows.Get(name).Context(ctx).Do(); err == nil {
		t.Fatalf("Get after delete succeeded, want 404")
	}
}

// TestSDKRevisionBump proves the drift-critical behavior over the wire: a patch
// that changes only description keeps revisionId stable; a patch that changes
// sourceContents mints a new stable revisionId.
func TestSDKRevisionBump(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()
	parent := "projects/" + project + "/locations/us-central1"
	name := parent + "/workflows/rev"

	if _, err := svc.Projects.Locations.Workflows.Create(parent, &workflows.Workflow{
		SourceContents: "SOURCE-A",
		ServiceAccount: "sa@mock-project.iam.gserviceaccount.com",
	}).WorkflowId("rev").Context(ctx).Do(); err != nil {
		t.Fatalf("Create: %v", err)
	}

	base, err := svc.Projects.Locations.Workflows.Get(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get(base): %v", err)
	}

	// Patch description only -> revisionId UNCHANGED.
	if _, err := svc.Projects.Locations.Workflows.Patch(name, &workflows.Workflow{Description: "updated"}).
		UpdateMask("description").Context(ctx).Do(); err != nil {
		t.Fatalf("Patch(description): %v", err)
	}

	afterDesc, err := svc.Projects.Locations.Workflows.Get(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get(afterDesc): %v", err)
	}

	if afterDesc.RevisionId != base.RevisionId {
		t.Fatalf("description patch bumped revisionId: %q -> %q", base.RevisionId, afterDesc.RevisionId)
	}

	if afterDesc.CreateTime != base.CreateTime {
		t.Fatalf("createTime changed on description patch")
	}

	// Patch sourceContents -> revisionId CHANGES.
	if _, err := svc.Projects.Locations.Workflows.Patch(name, &workflows.Workflow{SourceContents: "SOURCE-B"}).
		UpdateMask("sourceContents").Context(ctx).Do(); err != nil {
		t.Fatalf("Patch(sourceContents): %v", err)
	}

	afterSrc, err := svc.Projects.Locations.Workflows.Get(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get(afterSrc): %v", err)
	}

	if afterSrc.RevisionId == base.RevisionId {
		t.Fatalf("sourceContents patch did not bump revisionId (still %q)", base.RevisionId)
	}

	if afterSrc.CreateTime != base.CreateTime {
		t.Fatalf("createTime changed on source patch: %q -> %q", base.CreateTime, afterSrc.CreateTime)
	}

	// New revisionId is itself stable across a re-read.
	afterSrc2, err := svc.Projects.Locations.Workflows.Get(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get(afterSrc2): %v", err)
	}

	if afterSrc2.RevisionId != afterSrc.RevisionId {
		t.Fatalf("new revisionId not stable: %q -> %q", afterSrc.RevisionId, afterSrc2.RevisionId)
	}
}

// TestSharedPollerDoesNotStealSiblingOps guards the shared operations space: a
// poll for an operation name that was never created 404s (rather than a handler
// fabricating a done success), which is what keeps composer/scheduler/clouddeploy
// operations from being shadowed.
func TestSharedPollerDoesNotStealSiblingOps(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()

	unknown := "projects/" + project + "/locations/us-central1/operations/never-created-xyz"

	_, err := svc.Projects.Locations.Operations.Get(unknown).Context(ctx).Do()
	if err == nil {
		t.Fatalf("poll of unknown operation succeeded, want 404")
	}

	if gerr, ok := err.(*googleapi.Error); ok && gerr.Code != 404 {
		t.Fatalf("unknown operation poll code = %d, want 404", gerr.Code)
	}
}
