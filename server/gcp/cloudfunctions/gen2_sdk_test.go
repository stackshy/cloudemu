package cloudfunctions_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	cloudfunctions2 "google.golang.org/api/cloudfunctions/v2"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

func newGCPV2Service(t *testing.T) *cloudfunctions2.Service {
	t.Helper()

	cloud := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.Drivers{CloudFunctions: cloud.CloudFunctions})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	svc, err := cloudfunctions2.NewService(context.Background(),
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("NewService (v2): %v", err)
	}

	return svc
}

// TestSDKGen2CreateGetListDelete reproduces the gen2 (v2 API) blocker: the modern
// functions/apiv2 + google_cloudfunctions2_function surface must work — create
// reconciles to ACTIVE with a Cloud Run-backed serviceConfig.uri, and get/list/
// delete round-trip.
func TestSDKGen2CreateGetListDelete(t *testing.T) {
	svc := newGCPV2Service(t)
	ctx := context.Background()

	parent := "projects/demo/locations/us-central1"

	op, err := svc.Projects.Locations.Functions.Create(parent, &cloudfunctions2.Function{
		Environment: "GEN_2",
		BuildConfig: &cloudfunctions2.BuildConfig{
			Runtime:    "go121",
			EntryPoint: "Hello",
			Source: &cloudfunctions2.Source{
				StorageSource: &cloudfunctions2.StorageSource{Bucket: "b", Object: "o.zip"},
			},
		},
		ServiceConfig: &cloudfunctions2.ServiceConfig{
			AvailableMemory: "512M",
			TimeoutSeconds:  120,
		},
	}).FunctionId("g2").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Create (gen2): %v", err)
	}

	if !op.Done {
		t.Fatal("Create operation not done")
	}

	name := parent + "/functions/g2"

	got, err := svc.Projects.Locations.Functions.Get(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get (gen2): %v", err)
	}

	if got.State != "ACTIVE" {
		t.Fatalf("State = %q, want ACTIVE", got.State)
	}

	if got.Environment != "GEN_2" {
		t.Fatalf("Environment = %q, want GEN_2", got.Environment)
	}

	if got.ServiceConfig == nil || got.ServiceConfig.Uri == "" {
		t.Fatalf("serviceConfig.uri missing: %+v", got.ServiceConfig)
	}

	// Client-set config must survive the round-trip.
	if got.ServiceConfig.AvailableMemory != "512M" {
		t.Fatalf("availableMemory = %q, want 512M", got.ServiceConfig.AvailableMemory)
	}

	if got.ServiceConfig.TimeoutSeconds != 120 {
		t.Fatalf("timeoutSeconds = %d, want 120", got.ServiceConfig.TimeoutSeconds)
	}

	if got.BuildConfig == nil || got.BuildConfig.Runtime != "go121" {
		t.Fatalf("buildConfig.runtime missing: %+v", got.BuildConfig)
	}

	// Output-only defaults must be populated.
	if got.ServiceConfig.ServiceAccountEmail == "" || got.ServiceConfig.IngressSettings == "" {
		t.Fatalf("serviceConfig defaults empty: %+v", got.ServiceConfig)
	}

	if !strings.HasSuffix(got.Name, "/functions/g2") {
		t.Fatalf("Name = %q, want suffix /functions/g2", got.Name)
	}

	listResp, err := svc.Projects.Locations.Functions.List(parent).Context(ctx).Do()
	if err != nil {
		t.Fatalf("List (gen2): %v", err)
	}

	if len(listResp.Functions) != 1 {
		t.Fatalf("listed %d gen2 functions, want 1", len(listResp.Functions))
	}

	delOp, err := svc.Projects.Locations.Functions.Delete(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Delete (gen2): %v", err)
	}

	if !delOp.Done {
		t.Fatal("Delete operation not done")
	}

	if _, err := svc.Projects.Locations.Functions.Get(name).Context(ctx).Do(); err == nil {
		t.Fatal("post-delete Get returned nil error, want NotFound")
	}
}

// TestSDKGen2Patch confirms a gen2 update merges config and stays ACTIVE.
func TestSDKGen2Patch(t *testing.T) {
	svc := newGCPV2Service(t)
	ctx := context.Background()

	parent := "projects/demo/locations/us-central1"
	name := parent + "/functions/g2"

	if _, err := svc.Projects.Locations.Functions.Create(parent, &cloudfunctions2.Function{
		BuildConfig: &cloudfunctions2.BuildConfig{Runtime: "go121", EntryPoint: "Hello"},
	}).FunctionId("g2").Context(ctx).Do(); err != nil {
		t.Fatalf("Create: %v", err)
	}

	op, err := svc.Projects.Locations.Functions.Patch(name, &cloudfunctions2.Function{
		ServiceConfig: &cloudfunctions2.ServiceConfig{AvailableMemory: "1024M"},
	}).UpdateMask("serviceConfig.availableMemory").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}

	if !op.Done {
		t.Fatal("Patch operation not done")
	}

	got, err := svc.Projects.Locations.Functions.Get(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get after patch: %v", err)
	}

	if got.ServiceConfig.AvailableMemory != "1024M" {
		t.Fatalf("availableMemory = %q, want 1024M", got.ServiceConfig.AvailableMemory)
	}

	if got.State != "ACTIVE" {
		t.Fatalf("State = %q, want ACTIVE", got.State)
	}
}

// TestSDKGen2GetMissing confirms a missing gen2 function 404s.
func TestSDKGen2GetMissing(t *testing.T) {
	svc := newGCPV2Service(t)

	name := "projects/demo/locations/us-central1/functions/ghost"
	if _, err := svc.Projects.Locations.Functions.Get(name).Context(context.Background()).Do(); err == nil {
		t.Fatal("Get on missing gen2 function returned nil error, want NotFound")
	}
}
