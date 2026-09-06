package clouddeploy_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	clouddeploy "google.golang.org/api/clouddeploy/v1"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

func newSDKClient(t *testing.T) (*clouddeploy.Service, string) {
	t.Helper()

	cloud := cloudemu.NewGCP()
	srv := gcpserver.NewFromProvider(cloud)

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	svc, err := clouddeploy.NewService(context.Background(),
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("clouddeploy.NewService: %v", err)
	}

	return svc, "mock-project"
}

func TestSDKTargetLifecycle(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()
	location := "us-central1"
	parent := "projects/" + project + "/locations/" + location
	name := parent + "/targets/prod"

	want := &clouddeploy.Target{
		Description:     "production",
		RequireApproval: true,
		Gke:             &clouddeploy.GkeCluster{Cluster: "projects/mock-project/locations/us-central1/clusters/prod-gke"},
		ExecutionConfigs: []*clouddeploy.ExecutionConfig{
			{Usages: []string{"RENDER", "DEPLOY"}, ServiceAccount: "sa@mock-project.iam.gserviceaccount.com"},
		},
		Labels: map[string]string{"team": "sre"},
	}

	op, err := svc.Projects.Locations.Targets.Create(parent, want).TargetId("prod").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Targets.Create: %v", err)
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

	got, err := svc.Projects.Locations.Targets.Get(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Targets.Get: %v", err)
	}

	if got.Uid == "" || got.CreateTime == "" || got.Etag == "" {
		t.Fatalf("computed fields missing: uid=%q createTime=%q etag=%q", got.Uid, got.CreateTime, got.Etag)
	}

	if got.TargetId != "prod" {
		t.Fatalf("targetId = %q, want prod", got.TargetId)
	}

	if !got.RequireApproval {
		t.Fatalf("requireApproval not round-tripped")
	}

	if got.Gke == nil || !strings.HasSuffix(got.Gke.Cluster, "/prod-gke") {
		t.Fatalf("gke oneof not round-tripped: %+v", got.Gke)
	}

	if len(got.ExecutionConfigs) != 1 || strings.Join(got.ExecutionConfigs[0].Usages, ",") != "RENDER,DEPLOY" {
		t.Fatalf("executionConfigs.usages not round-tripped: %+v", got.ExecutionConfigs)
	}

	if got.Labels["team"] != "sre" {
		t.Fatalf("labels not round-tripped")
	}

	// Computed fields stable across reads (no Terraform drift).
	second, err := svc.Projects.Locations.Targets.Get(name).Do()
	if err != nil {
		t.Fatalf("second Get: %v", err)
	}

	if second.Uid != got.Uid || second.CreateTime != got.CreateTime || second.Etag != got.Etag {
		t.Fatalf("computed fields unstable across reads (drift): %+v vs %+v", got, second)
	}

	// Patch description + require_approval via updateMask -> LRO, applied.
	patchOp, err := svc.Projects.Locations.Targets.Patch(name, &clouddeploy.Target{Description: "prod-v2"}).
		UpdateMask("description").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Targets.Patch: %v", err)
	}

	if !patchOp.Done {
		t.Fatalf("patch operation not done")
	}

	updated, err := svc.Projects.Locations.Targets.Get(name).Do()
	if err != nil {
		t.Fatalf("Get after patch: %v", err)
	}

	if updated.Description != "prod-v2" {
		t.Fatalf("description after patch = %q", updated.Description)
	}

	// A field outside the mask is untouched (gke oneof preserved).
	if updated.Gke == nil || !strings.HasSuffix(updated.Gke.Cluster, "/prod-gke") {
		t.Fatalf("gke mutated by masked patch: %+v", updated.Gke)
	}

	delOp, err := svc.Projects.Locations.Targets.Delete(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Targets.Delete: %v", err)
	}

	if !delOp.Done {
		t.Fatalf("delete operation not done")
	}

	if _, err := svc.Projects.Locations.Targets.Get(name).Do(); err == nil {
		t.Fatalf("expected 404 after delete")
	}
}

func TestSDKPipelineLifecycle(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()
	location := "us-central1"
	parent := "projects/" + project + "/locations/" + location
	name := parent + "/deliveryPipelines/web"

	want := &clouddeploy.DeliveryPipeline{
		Description: "web delivery",
		SerialPipeline: &clouddeploy.SerialPipeline{
			Stages: []*clouddeploy.Stage{
				{TargetId: "staging", Profiles: []string{"dev"}},
				{TargetId: "prod", Profiles: []string{"prod"}},
			},
		},
		Labels: map[string]string{"app": "web"},
	}

	op, err := svc.Projects.Locations.DeliveryPipelines.Create(parent, want).
		DeliveryPipelineId("web").Context(ctx).Do()
	if err != nil {
		t.Fatalf("DeliveryPipelines.Create: %v", err)
	}

	if !op.Done {
		t.Fatalf("create operation not done")
	}

	got, err := svc.Projects.Locations.DeliveryPipelines.Get(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("DeliveryPipelines.Get: %v", err)
	}

	if got.Uid == "" || got.CreateTime == "" || got.Etag == "" {
		t.Fatalf("computed fields missing: %+v", got)
	}

	if got.Condition == nil || got.Condition.PipelineReadyCondition == nil ||
		!got.Condition.PipelineReadyCondition.Status {
		t.Fatalf("computed condition not populated: %+v", got.Condition)
	}

	if got.SerialPipeline == nil || len(got.SerialPipeline.Stages) != 2 {
		t.Fatalf("serialPipeline not round-tripped: %+v", got.SerialPipeline)
	}

	if got.SerialPipeline.Stages[0].TargetId != "staging" ||
		strings.Join(got.SerialPipeline.Stages[1].Profiles, ",") != "prod" {
		t.Fatalf("stages not round-tripped verbatim: %+v", got.SerialPipeline.Stages)
	}

	// Stable across reads.
	second, err := svc.Projects.Locations.DeliveryPipelines.Get(name).Do()
	if err != nil {
		t.Fatalf("second Get: %v", err)
	}

	if second.Uid != got.Uid || second.CreateTime != got.CreateTime ||
		second.Etag != got.Etag || second.Condition.PipelineReadyCondition.Status != true {
		t.Fatalf("computed fields unstable across reads (drift)")
	}

	// List.
	list, err := svc.Projects.Locations.DeliveryPipelines.List(parent).Do()
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(list.DeliveryPipelines) != 1 || !strings.HasSuffix(list.DeliveryPipelines[0].Name, "/web") {
		t.Fatalf("list = %+v", list.DeliveryPipelines)
	}

	// Patch stages via updateMask -> LRO, applied; description untouched.
	patchOp, err := svc.Projects.Locations.DeliveryPipelines.Patch(name, &clouddeploy.DeliveryPipeline{
		SerialPipeline: &clouddeploy.SerialPipeline{
			Stages: []*clouddeploy.Stage{{TargetId: "prod", Profiles: []string{"prod"}}},
		},
	}).UpdateMask("serialPipeline").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}

	if !patchOp.Done {
		t.Fatalf("patch operation not done")
	}

	updated, err := svc.Projects.Locations.DeliveryPipelines.Get(name).Do()
	if err != nil {
		t.Fatalf("Get after patch: %v", err)
	}

	if len(updated.SerialPipeline.Stages) != 1 {
		t.Fatalf("stages not updated: %+v", updated.SerialPipeline.Stages)
	}

	if updated.Description != "web delivery" {
		t.Fatalf("description mutated by masked patch: %q", updated.Description)
	}

	if _, err := svc.Projects.Locations.DeliveryPipelines.Delete(name).Do(); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err := svc.Projects.Locations.DeliveryPipelines.Get(name).Do(); err == nil {
		t.Fatalf("expected 404 after delete")
	}
}

// TestSDKTargetRequireApprovalExplicitFalse proves an explicit require_approval
// = false round-trips (bug class 8): a client that force-sends the zero value
// reads it back, so a Terraform config with require_approval = false sees no
// drift.
func TestSDKTargetRequireApprovalExplicitFalse(t *testing.T) {
	svc, project := newSDKClient(t)
	parent := "projects/" + project + "/locations/us-central1"
	name := parent + "/targets/noapprove"

	tgt := &clouddeploy.Target{
		Run:             &clouddeploy.CloudRunLocation{Location: "projects/mock-project/locations/us-central1"},
		RequireApproval: false,
		ForceSendFields: []string{"RequireApproval"},
	}

	if _, err := svc.Projects.Locations.Targets.Create(parent, tgt).TargetId("noapprove").Do(); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := svc.Projects.Locations.Targets.Get(name).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Run == nil || !strings.HasSuffix(got.Run.Location, "/us-central1") {
		t.Fatalf("run oneof not round-tripped: %+v", got.Run)
	}

	if got.RequireApproval {
		t.Fatalf("requireApproval should be false")
	}
}

func TestSDKTargetOneofViolation(t *testing.T) {
	svc, project := newSDKClient(t)
	parent := "projects/" + project + "/locations/us-central1"

	// No deployment-target block set -> 400.
	if _, err := svc.Projects.Locations.Targets.Create(parent, &clouddeploy.Target{}).TargetId("bad").Do(); err == nil {
		t.Fatalf("expected 400 for missing deployment-target oneof")
	}

	// Two blocks set -> 400.
	both := &clouddeploy.Target{
		Gke: &clouddeploy.GkeCluster{Cluster: "c"},
		Run: &clouddeploy.CloudRunLocation{Location: "l"},
	}

	if _, err := svc.Projects.Locations.Targets.Create(parent, both).TargetId("both").Do(); err == nil {
		t.Fatalf("expected 400 for two deployment-target blocks")
	}
}

func TestSDKPipelineGetNotFound(t *testing.T) {
	svc, project := newSDKClient(t)

	_, err := svc.Projects.Locations.DeliveryPipelines.Get(
		"projects/" + project + "/locations/us-central1/deliveryPipelines/ghost").Do()
	if err == nil {
		t.Fatalf("expected NOT_FOUND")
	}
}

func TestSDKPipelineCreateDuplicate(t *testing.T) {
	svc, project := newSDKClient(t)
	parent := "projects/" + project + "/locations/us-central1"

	p := &clouddeploy.DeliveryPipeline{}

	if _, err := svc.Projects.Locations.DeliveryPipelines.Create(parent, p).DeliveryPipelineId("dup").Do(); err != nil {
		t.Fatalf("first create: %v", err)
	}

	if _, err := svc.Projects.Locations.DeliveryPipelines.Create(parent, p).DeliveryPipelineId("dup").Do(); err == nil {
		t.Fatalf("expected ALREADY_EXISTS on duplicate create")
	}
}
