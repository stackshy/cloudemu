package cloudfunctions_test

import (
	"context"
	"testing"

	cloudfunctions "google.golang.org/api/cloudfunctions/v1"
)

// TestSDKGen1MetadataFields reproduces the missing-fields finding: Get after
// Create must return non-empty serviceAccountEmail/ingressSettings/dockerRegistry/
// buildId, and versionId must be 1 at create and bump on update.
func TestSDKGen1MetadataFields(t *testing.T) {
	svc := newGCPSDKService(t)
	ctx := context.Background()

	parent := "projects/demo/locations/us-central1"
	name := parent + "/functions/meta"

	if _, err := svc.Projects.Locations.Functions.Create(parent, &cloudfunctions.CloudFunction{
		Name:    name,
		Runtime: "go121",
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := svc.Projects.Locations.Functions.Get(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.ServiceAccountEmail == "" {
		t.Fatal("serviceAccountEmail empty, want a default")
	}

	if got.IngressSettings == "" {
		t.Fatal("ingressSettings empty, want ALLOW_ALL")
	}

	if got.DockerRegistry == "" {
		t.Fatal("dockerRegistry empty, want ARTIFACT_REGISTRY")
	}

	if got.BuildId == "" {
		t.Fatal("buildId empty, want a generated id")
	}

	if got.VersionId != 1 {
		t.Fatalf("versionId = %d at create, want 1", got.VersionId)
	}

	firstBuild := got.BuildId

	// A deploy (PATCH) bumps the versionId and cuts a fresh build.
	if _, err := svc.Projects.Locations.Functions.Patch(name, &cloudfunctions.CloudFunction{
		Name:              name,
		AvailableMemoryMb: 256,
	}).UpdateMask("availableMemoryMb").Context(ctx).Do(); err != nil {
		t.Fatalf("Patch: %v", err)
	}

	got2, err := svc.Projects.Locations.Functions.Get(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get after patch: %v", err)
	}

	if got2.VersionId != 2 {
		t.Fatalf("versionId = %d after patch, want 2", got2.VersionId)
	}

	if got2.BuildId == firstBuild {
		t.Fatalf("buildId unchanged after patch: %q", got2.BuildId)
	}
}

// TestSDKGen1MetadataHonorsBody confirms client-supplied ingress/serviceAccount/
// dockerRegistry survive the round-trip instead of being overwritten by defaults.
func TestSDKGen1MetadataHonorsBody(t *testing.T) {
	svc := newGCPSDKService(t)
	ctx := context.Background()

	parent := "projects/demo/locations/us-central1"
	name := parent + "/functions/custom"

	if _, err := svc.Projects.Locations.Functions.Create(parent, &cloudfunctions.CloudFunction{
		Name:                name,
		Runtime:             "go121",
		IngressSettings:     "ALLOW_INTERNAL_ONLY",
		ServiceAccountEmail: "svc@demo.iam.gserviceaccount.com",
		DockerRegistry:      "CONTAINER_REGISTRY",
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := svc.Projects.Locations.Functions.Get(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.IngressSettings != "ALLOW_INTERNAL_ONLY" {
		t.Fatalf("ingressSettings = %q, want ALLOW_INTERNAL_ONLY", got.IngressSettings)
	}

	if got.ServiceAccountEmail != "svc@demo.iam.gserviceaccount.com" {
		t.Fatalf("serviceAccountEmail = %q, want the supplied value", got.ServiceAccountEmail)
	}

	if got.DockerRegistry != "CONTAINER_REGISTRY" {
		t.Fatalf("dockerRegistry = %q, want CONTAINER_REGISTRY", got.DockerRegistry)
	}
}

// TestSDKCloudFunctionsTestIamPermissions reproduces the missing testIamPermissions
// verb: real GCP returns the held permission subset; CloudEmu (no IAM enforcement)
// echoes the requested set rather than 405.
func TestSDKCloudFunctionsTestIamPermissions(t *testing.T) {
	svc := newGCPSDKService(t)
	ctx := context.Background()

	parent := "projects/demo/locations/us-central1"
	name := parent + "/functions/hello"

	if _, err := svc.Projects.Locations.Functions.Create(parent, &cloudfunctions.CloudFunction{
		Name:    name,
		Runtime: "go121",
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Create: %v", err)
	}

	want := []string{"cloudfunctions.functions.invoke", "cloudfunctions.functions.get"}

	resp, err := svc.Projects.Locations.Functions.TestIamPermissions(name,
		&cloudfunctions.TestIamPermissionsRequest{Permissions: want}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("TestIamPermissions: %v", err)
	}

	if len(resp.Permissions) != len(want) {
		t.Fatalf("permissions = %v, want %v", resp.Permissions, want)
	}
}

// TestSDKCloudFunctionsListPagination reproduces the ignored-pagination finding:
// with two functions, PageSize(1) must return one function plus a nextPageToken,
// and the token must fetch the second.
func TestSDKCloudFunctionsListPagination(t *testing.T) {
	svc := newGCPSDKService(t)
	ctx := context.Background()

	parent := "projects/demo/locations/us-central1"

	for _, id := range []string{"fn-a", "fn-b"} {
		if _, err := svc.Projects.Locations.Functions.Create(parent, &cloudfunctions.CloudFunction{
			Name:    parent + "/functions/" + id,
			Runtime: "go121",
		}).Context(ctx).Do(); err != nil {
			t.Fatalf("Create %s: %v", id, err)
		}
	}

	page1, err := svc.Projects.Locations.Functions.List(parent).PageSize(1).Context(ctx).Do()
	if err != nil {
		t.Fatalf("List page 1: %v", err)
	}

	if len(page1.Functions) != 1 {
		t.Fatalf("page 1 returned %d functions, want 1", len(page1.Functions))
	}

	if page1.NextPageToken == "" {
		t.Fatal("page 1 has no nextPageToken, want one")
	}

	page2, err := svc.Projects.Locations.Functions.List(parent).
		PageSize(1).PageToken(page1.NextPageToken).Context(ctx).Do()
	if err != nil {
		t.Fatalf("List page 2: %v", err)
	}

	if len(page2.Functions) != 1 {
		t.Fatalf("page 2 returned %d functions, want 1", len(page2.Functions))
	}

	if page1.Functions[0].Name == page2.Functions[0].Name {
		t.Fatalf("page 1 and 2 returned the same function %q", page1.Functions[0].Name)
	}
}
