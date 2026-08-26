package cloudrun_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/api/option"
	run "google.golang.org/api/run/v2"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

// newRunWithURL boots an in-process GCP server and returns both a run/v2 SDK
// client and the base URL, so tests can also issue raw field-masked PATCHes the
// SDK's Patch helper does not model precisely.
func newRunWithURL(t *testing.T) (*run.Service, string) {
	t.Helper()

	cloud := cloudemu.NewGCP(config.WithContainerEngine(nil))
	ts := httptest.NewServer(gcpserver.New(gcpserver.Drivers{CloudRun: cloud.CloudRun}))
	t.Cleanup(ts.Close)

	svc, err := run.NewService(context.Background(),
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("run.NewService: %v", err)
	}

	return svc, ts.URL
}

// rawPatch issues a raw JSON PATCH with the given updateMask and returns the
// decoded response, mirroring a field-masked SDK/gcloud request.
func rawPatch(t *testing.T, base, resource, mask string, body any) map[string]any {
	t.Helper()

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		t.Fatalf("encode: %v", err)
	}

	url := base + "/v2/" + resource + "?updateMask=" + mask

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPatch, url, &buf)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH %s: %v", url, err)
	}

	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH %s -> %d: %s", url, resp.StatusCode, string(raw))
	}

	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("decode %q: %v", string(raw), err)
	}

	return m
}

func createWebService(t *testing.T, svc *run.Service, in *run.GoogleCloudRunV2Service) {
	t.Helper()

	if _, err := svc.Projects.Locations.Services.Create(parent, in).
		ServiceId("web").Context(context.Background()).Do(); err != nil {
		t.Fatalf("Create: %v", err)
	}
}

// TestSDKServicePatchTrafficMaskPreservesTemplate covers B1: a field-masked
// PATCH carrying only traffic must update traffic without wiping the container
// template (the data-loss bug).
func TestSDKServicePatchTrafficMaskPreservesTemplate(t *testing.T) {
	svc, base := newRunWithURL(t)
	ctx := context.Background()

	createWebService(t, svc, sdkService())

	rawPatch(t, base, parent+"/services/web", "traffic", map[string]any{
		"traffic": []map[string]any{
			{"type": "TRAFFIC_TARGET_ALLOCATION_TYPE_LATEST", "percent": 100, "tag": "prod"},
		},
	})

	got, err := svc.Projects.Locations.Services.Get(parent + "/services/web").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if len(got.Template.Containers) != 1 || got.Template.Containers[0].Image != "gcr.io/demo/web:v1" {
		t.Fatalf("container wiped by masked PATCH: %+v", got.Template.Containers)
	}

	if got.Template.ServiceAccount != "web@demo-project.iam.gserviceaccount.com" {
		t.Errorf("serviceAccount wiped: %q", got.Template.ServiceAccount)
	}

	if len(got.Traffic) != 1 || got.Traffic[0].Tag != "prod" {
		t.Errorf("traffic not updated: %+v", got.Traffic)
	}
}

// TestSDKServicePatchTrafficMaskNoNewRevision covers B2: a traffic-only update
// leaves the template intact, so no new revision is cut and latestCreated stays.
func TestSDKServicePatchTrafficMaskNoNewRevision(t *testing.T) {
	svc, base := newRunWithURL(t)
	ctx := context.Background()

	createWebService(t, svc, sdkService())

	before, err := svc.Projects.Locations.Services.Get(parent + "/services/web").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get before: %v", err)
	}

	rawPatch(t, base, parent+"/services/web", "traffic", map[string]any{
		"traffic": []map[string]any{
			{"type": "TRAFFIC_TARGET_ALLOCATION_TYPE_LATEST", "percent": 100, "tag": "prod"},
		},
	})

	after, err := svc.Projects.Locations.Services.Get(parent + "/services/web").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get after: %v", err)
	}

	if after.LatestCreatedRevision != before.LatestCreatedRevision {
		t.Errorf("latestCreatedRevision advanced: %q -> %q", before.LatestCreatedRevision, after.LatestCreatedRevision)
	}

	list, err := svc.Projects.Locations.Services.Revisions.List(parent + "/services/web").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Revisions.List: %v", err)
	}

	if len(list.Revisions) != 1 {
		t.Errorf("revisions = %d, want 1 (no spurious revision)", len(list.Revisions))
	}

	if after.Traffic[0].Tag != "prod" {
		t.Errorf("traffic not updated: %+v", after.Traffic)
	}
}

// TestSDKServiceResourceLimitsRoundTrip covers B3: container resources.limits
// (cpu/memory) survive create -> get.
func TestSDKServiceResourceLimitsRoundTrip(t *testing.T) {
	svc, _ := newRunWithURL(t)
	ctx := context.Background()

	in := sdkService()
	in.Template.Containers[0].Resources = &run.GoogleCloudRunV2ResourceRequirements{
		Limits: map[string]string{"cpu": "1000m", "memory": "512Mi"},
	}
	createWebService(t, svc, in)

	got, err := svc.Projects.Locations.Services.Get(parent + "/services/web").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	res := got.Template.Containers[0].Resources
	if res == nil {
		t.Fatal("resources dropped on round-trip")
	}

	if res.Limits["cpu"] != "1000m" || res.Limits["memory"] != "512Mi" {
		t.Errorf("limits = %+v, want cpu=1000m memory=512Mi", res.Limits)
	}
}

// TestSDKServiceTemplateLabelsAnnotationsRoundTrip covers B4: revisionTemplate
// labels and annotations survive create -> get.
func TestSDKServiceTemplateLabelsAnnotationsRoundTrip(t *testing.T) {
	svc, _ := newRunWithURL(t)
	ctx := context.Background()

	in := sdkService()
	in.Template.Labels = map[string]string{"team": "web"}
	in.Template.Annotations = map[string]string{"autoscaling.knative.dev/maxScale": "10"}
	createWebService(t, svc, in)

	got, err := svc.Projects.Locations.Services.Get(parent + "/services/web").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Template.Labels["team"] != "web" {
		t.Errorf("template labels = %+v, want team=web", got.Template.Labels)
	}

	if got.Template.Annotations["autoscaling.knative.dev/maxScale"] != "10" {
		t.Errorf("template annotations = %+v, want maxScale=10", got.Template.Annotations)
	}
}

// TestSDKServiceFullPutStillReplaces covers the regression: a maskless PUT
// (Terraform-style) still full-replaces — a field omitted from the new body is
// cleared, not preserved.
func TestSDKServiceFullPutStillReplaces(t *testing.T) {
	svc, _ := newRunWithURL(t)
	ctx := context.Background()

	createWebService(t, svc, sdkService())

	replacement := &run.GoogleCloudRunV2Service{
		Description: "replaced",
		Template: &run.GoogleCloudRunV2RevisionTemplate{
			Containers: []*run.GoogleCloudRunV2Container{{Image: "gcr.io/demo/web:v2"}},
		},
	}

	if _, err := svc.Projects.Locations.Services.Patch(parent+"/services/web", replacement).
		Context(ctx).Do(); err != nil {
		t.Fatalf("Patch: %v", err)
	}

	got, err := svc.Projects.Locations.Services.Get(parent + "/services/web").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Description != "replaced" {
		t.Errorf("description = %q, want replaced", got.Description)
	}

	if got.Template.Containers[0].Image != "gcr.io/demo/web:v2" {
		t.Errorf("image = %q, want v2", got.Template.Containers[0].Image)
	}

	if got.Template.ServiceAccount != "" {
		t.Errorf("serviceAccount = %q, want cleared by full replace", got.Template.ServiceAccount)
	}

	if got.Generation != 2 {
		t.Errorf("generation = %d, want 2", got.Generation)
	}
}
