package cloudrun_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	run "google.golang.org/api/run/v2"
)

// decodeOpResponse unmarshals an LRO's inlined response payload into v.
func decodeOpResponse(t *testing.T, op *run.GoogleLongrunningOperation, v any) {
	t.Helper()

	if !op.Done {
		t.Fatalf("operation %q not done", op.Name)
	}

	if len(op.Response) == 0 {
		t.Fatalf("operation %q has no response payload", op.Name)
	}

	if err := json.Unmarshal(op.Response, v); err != nil {
		t.Fatalf("decode op response: %v", err)
	}
}

func sdkService() *run.GoogleCloudRunV2Service {
	return &run.GoogleCloudRunV2Service{
		Description: "web frontend",
		Ingress:     "INGRESS_TRAFFIC_ALL",
		Template: &run.GoogleCloudRunV2RevisionTemplate{
			ServiceAccount: "web@demo-project.iam.gserviceaccount.com",
			Timeout:        "300s",
			Scaling:        &run.GoogleCloudRunV2RevisionScaling{MinInstanceCount: 1, MaxInstanceCount: 5},
			Containers: []*run.GoogleCloudRunV2Container{{
				Image: "gcr.io/demo/web:v1",
				Ports: []*run.GoogleCloudRunV2ContainerPort{{ContainerPort: 8080}},
			}},
		},
	}
}

// TestSDKServiceCreateGetListDelete covers the [BLOCKER] finding: the Services
// surface exists — create reconciles to a URL + ready revision + traffic, and
// Get/List/Delete work.
func TestSDKServiceCreateGetListDelete(t *testing.T) {
	svc := newRun(t, nil)
	ctx := context.Background()

	op, err := svc.Projects.Locations.Services.Create(parent, sdkService()).ServiceId("web").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Services.Create: %v", err)
	}

	var created run.GoogleCloudRunV2Service
	decodeOpResponse(t, op, &created)

	if created.Uri == "" || !strings.HasPrefix(created.Uri, "https://web-") {
		t.Errorf("uri = %q, want https://web-…run.app", created.Uri)
	}

	got, err := svc.Projects.Locations.Services.Get(parent + "/services/web").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Services.Get: %v", err)
	}

	if got.Uri == "" {
		t.Error("get: uri empty")
	}

	if !strings.Contains(got.LatestReadyRevision, "/services/web/revisions/web-00001-") {
		t.Errorf("latestReadyRevision = %q", got.LatestReadyRevision)
	}

	if got.LatestCreatedRevision != got.LatestReadyRevision {
		t.Errorf("latestCreated=%q latestReady=%q, want equal", got.LatestCreatedRevision, got.LatestReadyRevision)
	}

	if got.TerminalCondition == nil || got.TerminalCondition.Type != "Ready" ||
		got.TerminalCondition.State != "CONDITION_SUCCEEDED" {
		t.Errorf("terminalCondition = %+v", got.TerminalCondition)
	}

	if len(got.Traffic) != 1 || got.Traffic[0].Percent != 100 {
		t.Errorf("traffic = %+v, want 100%% latest", got.Traffic)
	}

	if len(got.TrafficStatuses) != 1 || got.TrafficStatuses[0].Revision == "" {
		t.Errorf("trafficStatuses = %+v", got.TrafficStatuses)
	}

	if got.Template.ServiceAccount != "web@demo-project.iam.gserviceaccount.com" || got.Template.Timeout != "300s" {
		t.Errorf("template round-trip lost fields: %+v", got.Template)
	}

	list, err := svc.Projects.Locations.Services.List(parent).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Services.List: %v", err)
	}

	if len(list.Services) != 1 {
		t.Fatalf("list services = %d, want 1", len(list.Services))
	}

	delOp, err := svc.Projects.Locations.Services.Delete(parent + "/services/web").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Services.Delete: %v", err)
	}

	if !delOp.Done {
		t.Fatal("delete op not done")
	}

	if _, err := svc.Projects.Locations.Services.Get(parent + "/services/web").Context(ctx).Do(); err == nil {
		t.Fatal("get after delete: want error")
	}
}

// TestSDKServicePatchCreatesRevision covers the [BLOCKER] finding's patch verb:
// updating a service materializes a new revision and bumps generation.
func TestSDKServicePatchCreatesRevision(t *testing.T) {
	svc := newRun(t, nil)
	ctx := context.Background()

	if _, err := svc.Projects.Locations.Services.Create(parent, sdkService()).
		ServiceId("web").Context(ctx).Do(); err != nil {
		t.Fatalf("Create: %v", err)
	}

	updated := sdkService()
	updated.Template.Containers[0].Image = "gcr.io/demo/web:v2"

	op, err := svc.Projects.Locations.Services.Patch(parent+"/services/web", updated).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}

	if !op.Done {
		t.Fatal("patch op not done")
	}

	got, err := svc.Projects.Locations.Services.Get(parent + "/services/web").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Generation != 2 {
		t.Errorf("generation = %d, want 2", got.Generation)
	}

	if !strings.Contains(got.LatestReadyRevision, "/revisions/web-00002-") {
		t.Errorf("latestReadyRevision = %q, want web-00002-…", got.LatestReadyRevision)
	}

	if got.Template.Containers[0].Image != "gcr.io/demo/web:v2" {
		t.Errorf("image = %q, want v2", got.Template.Containers[0].Image)
	}
}

// TestSDKServiceRevisions covers the [BLOCKER] finding's revisions surface:
// list/get/delete of the revisions a service materializes.
func TestSDKServiceRevisions(t *testing.T) {
	svc := newRun(t, nil)
	ctx := context.Background()

	if _, err := svc.Projects.Locations.Services.Create(parent, sdkService()).
		ServiceId("web").Context(ctx).Do(); err != nil {
		t.Fatalf("Create: %v", err)
	}

	updated := sdkService()
	updated.Template.Containers[0].Image = "gcr.io/demo/web:v2"
	if _, err := svc.Projects.Locations.Services.Patch(parent+"/services/web", updated).Context(ctx).Do(); err != nil {
		t.Fatalf("Patch: %v", err)
	}

	list, err := svc.Projects.Locations.Services.Revisions.List(parent + "/services/web").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Revisions.List: %v", err)
	}

	if len(list.Revisions) != 2 {
		t.Fatalf("revisions = %d, want 2", len(list.Revisions))
	}

	first := list.Revisions[0].Name

	rev, err := svc.Projects.Locations.Services.Revisions.Get(first).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Revisions.Get: %v", err)
	}

	if rev.Service == "" || !strings.HasSuffix(rev.Service, "/services/web") {
		t.Errorf("revision.service = %q", rev.Service)
	}

	delOp, err := svc.Projects.Locations.Services.Revisions.Delete(first).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Revisions.Delete: %v", err)
	}

	if !delOp.Done {
		t.Fatal("revision delete op not done")
	}

	after, err := svc.Projects.Locations.Services.Revisions.List(parent + "/services/web").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Revisions.List after delete: %v", err)
	}

	if len(after.Revisions) != 1 {
		t.Fatalf("revisions after delete = %d, want 1", len(after.Revisions))
	}
}

// TestSDKServiceIam covers the service IAM surface (getIamPolicy/setIamPolicy).
func TestSDKServiceIam(t *testing.T) {
	svc := newRun(t, nil)
	ctx := context.Background()

	if _, err := svc.Projects.Locations.Services.Create(parent, sdkService()).
		ServiceId("web").Context(ctx).Do(); err != nil {
		t.Fatalf("Create: %v", err)
	}

	resource := parent + "/services/web"
	set := &run.GoogleIamV1SetIamPolicyRequest{Policy: &run.GoogleIamV1Policy{
		Bindings: []*run.GoogleIamV1Binding{{
			Role:    "roles/run.invoker",
			Members: []string{"allUsers"},
		}},
	}}
	if _, err := svc.Projects.Locations.Services.SetIamPolicy(resource, set).Context(ctx).Do(); err != nil {
		t.Fatalf("SetIamPolicy: %v", err)
	}

	pol, err := svc.Projects.Locations.Services.GetIamPolicy(resource).Context(ctx).Do()
	if err != nil {
		t.Fatalf("GetIamPolicy: %v", err)
	}

	if len(pol.Bindings) != 1 || pol.Bindings[0].Members[0] != "allUsers" {
		t.Fatalf("policy = %+v", pol.Bindings)
	}
}

// TestSDKServicesListPagination covers pageSize/pageToken over the services list.
func TestSDKServicesListPagination(t *testing.T) {
	svc := newRun(t, nil)
	ctx := context.Background()

	for _, id := range []string{"a", "b"} {
		if _, err := svc.Projects.Locations.Services.Create(parent, sdkService()).
			ServiceId(id).Context(ctx).Do(); err != nil {
			t.Fatalf("Create %s: %v", id, err)
		}
	}

	page1, err := svc.Projects.Locations.Services.List(parent).PageSize(1).Context(ctx).Do()
	if err != nil {
		t.Fatalf("List page1: %v", err)
	}

	if len(page1.Services) != 1 || page1.NextPageToken == "" {
		t.Fatalf("page1 services=%d token=%q", len(page1.Services), page1.NextPageToken)
	}

	page2, err := svc.Projects.Locations.Services.List(parent).
		PageSize(1).PageToken(page1.NextPageToken).Context(ctx).Do()
	if err != nil {
		t.Fatalf("List page2: %v", err)
	}

	if len(page2.Services) != 1 || page2.NextPageToken != "" {
		t.Fatalf("page2 services=%d token=%q", len(page2.Services), page2.NextPageToken)
	}
}
