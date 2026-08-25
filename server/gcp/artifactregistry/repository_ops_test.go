package artifactregistry_test

import (
	"context"
	"net/http/httptest"
	"testing"

	gapic "cloud.google.com/go/artifactregistry/apiv1"
	"cloud.google.com/go/artifactregistry/apiv1/artifactregistrypb"
	ar "google.golang.org/api/artifactregistry/v1"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
	crdriver "github.com/stackshy/cloudemu/v2/services/containerregistry/driver"
)

// TestSDKArtifactRegistryPatch guards the HIGH finding: PATCH previously 404'd
// ("unsupported repository operation"), so Terraform label/description updates
// failed. A patch with an update mask must now persist.
func TestSDKArtifactRegistryPatch(t *testing.T) {
	svc, _ := newARService(t)
	ctx := context.Background()

	name := testParent + "/repositories/patchme"

	if _, err := svc.Projects.Locations.Repositories.Create(testParent, &ar.Repository{
		Format: "DOCKER", Labels: map[string]string{"env": "dev"},
	}).RepositoryId("patchme").Context(ctx).Do(); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := svc.Projects.Locations.Repositories.Patch(name, &ar.Repository{
		Labels: map[string]string{"env": "prod", "team": "platform"}, Description: "prod repo",
	}).UpdateMask("labels,description").Context(ctx).Do(); err != nil {
		t.Fatalf("Patch: %v", err)
	}

	got, err := svc.Projects.Locations.Repositories.Get(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Labels["env"] != "prod" || got.Labels["team"] != "platform" {
		t.Fatalf("labels not patched: %+v", got.Labels)
	}

	if got.Description != "prod repo" {
		t.Fatalf("description=%q want 'prod repo'", got.Description)
	}
}

// TestSDKArtifactRegistryRepoIAM guards the HIGH finding: repo IAM colon-verbs
// (getIamPolicy/setIamPolicy/testIamPermissions) previously 404'd. They must now
// round-trip.
func TestSDKArtifactRegistryRepoIAM(t *testing.T) {
	svc, _ := newARService(t)
	ctx := context.Background()

	name := testParent + "/repositories/iam"

	if _, err := svc.Projects.Locations.Repositories.Create(testParent, &ar.Repository{Format: "DOCKER"}).
		RepositoryId("iam").Context(ctx).Do(); err != nil {
		t.Fatalf("Create: %v", err)
	}

	set, err := svc.Projects.Locations.Repositories.SetIamPolicy(name, &ar.SetIamPolicyRequest{
		Policy: &ar.Policy{Bindings: []*ar.Binding{{
			Role: "roles/artifactregistry.reader", Members: []string{"user:a@b.com"},
		}}},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("SetIamPolicy: %v", err)
	}

	if len(set.Bindings) != 1 || set.Etag == "" {
		t.Fatalf("SetIamPolicy returned %+v", set)
	}

	got, err := svc.Projects.Locations.Repositories.GetIamPolicy(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("GetIamPolicy: %v", err)
	}

	if len(got.Bindings) != 1 || got.Bindings[0].Role != "roles/artifactregistry.reader" {
		t.Fatalf("GetIamPolicy did not round-trip: %+v", got)
	}

	perms, err := svc.Projects.Locations.Repositories.TestIamPermissions(name, &ar.TestIamPermissionsRequest{
		Permissions: []string{"artifactregistry.repositories.get"},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("TestIamPermissions: %v", err)
	}

	if len(perms.Permissions) != 1 || perms.Permissions[0] != "artifactregistry.repositories.get" {
		t.Fatalf("TestIamPermissions returned %+v", perms.Permissions)
	}
}

// TestSDKArtifactRegistryRepoFields guards the finding that mode / sizeBytes /
// kmsKeyName were absent and updateTime was always empty.
func TestSDKArtifactRegistryRepoFields(t *testing.T) {
	svc, reg := newARService(t)
	ctx := context.Background()

	name := testParent + "/repositories/fields"
	kms := "projects/demo/locations/us/keyRings/kr/cryptoKeys/k"

	if _, err := svc.Projects.Locations.Repositories.Create(testParent, &ar.Repository{
		Format: "DOCKER", Mode: "STANDARD_REPOSITORY", KmsKeyName: kms,
	}).RepositoryId("fields").Context(ctx).Do(); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := reg.PutImage(ctx, &crdriver.ImageManifest{
		Repository: "fields", Tag: "v1", Digest: "sha256:abc", SizeBytes: 4096,
	}); err != nil {
		t.Fatalf("seed PutImage: %v", err)
	}

	got, err := svc.Projects.Locations.Repositories.Get(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Mode != "STANDARD_REPOSITORY" {
		t.Errorf("mode=%q want STANDARD_REPOSITORY", got.Mode)
	}

	if got.KmsKeyName != kms {
		t.Errorf("kmsKeyName=%q want %q", got.KmsKeyName, kms)
	}

	if got.SizeBytes != 4096 {
		t.Errorf("sizeBytes=%d want 4096", got.SizeBytes)
	}

	if got.CreateTime == "" || got.UpdateTime == "" {
		t.Errorf("create/update time empty: create=%q update=%q", got.CreateTime, got.UpdateTime)
	}
}

// TestSDKArtifactRegistryListPaging guards the finding that pageSize/pageToken
// were ignored on the repositories list.
func TestSDKArtifactRegistryListPaging(t *testing.T) {
	svc, _ := newARService(t)
	ctx := context.Background()

	for _, id := range []string{"p1", "p2", "p3"} {
		if _, err := svc.Projects.Locations.Repositories.Create(testParent, &ar.Repository{Format: "DOCKER"}).
			RepositoryId(id).Context(ctx).Do(); err != nil {
			t.Fatalf("Create %s: %v", id, err)
		}
	}

	first, err := svc.Projects.Locations.Repositories.List(testParent).PageSize(2).Context(ctx).Do()
	if err != nil {
		t.Fatalf("List page 1: %v", err)
	}

	if len(first.Repositories) != 2 || first.NextPageToken == "" {
		t.Fatalf("page 1 got %d repos, token=%q; want 2 + token", len(first.Repositories), first.NextPageToken)
	}

	second, err := svc.Projects.Locations.Repositories.List(testParent).
		PageSize(2).PageToken(first.NextPageToken).Context(ctx).Do()
	if err != nil {
		t.Fatalf("List page 2: %v", err)
	}

	if len(second.Repositories) != 1 || second.NextPageToken != "" {
		t.Fatalf("page 2 got %d repos, token=%q; want 1 + no token", len(second.Repositories), second.NextPageToken)
	}
}

// TestGAPICCreateRepositoryFormatEnum guards the WRONG_WIRE finding: the GAPIC
// apiv1 client serializes Format as a numeric proto enum (UseEnumNumbers), which
// previously failed to decode into the string field ("cannot unmarshal number").
// Create with a set Format must now succeed.
func TestGAPICCreateRepositoryFormatEnum(t *testing.T) {
	cloud := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.DriversFrom(cloud))
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	ctx := context.Background()

	client, err := gapic.NewRESTClient(ctx,
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
		option.WithHTTPClient(ts.Client()),
	)
	if err != nil {
		t.Fatalf("NewRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	op, err := client.CreateRepository(ctx, &artifactregistrypb.CreateRepositoryRequest{
		Parent:       "projects/demo/locations/us",
		RepositoryId: "enum-repo",
		Repository:   &artifactregistrypb.Repository{Format: artifactregistrypb.Repository_DOCKER},
	})
	if err != nil {
		t.Fatalf("CreateRepository (numeric Format enum): %v", err)
	}

	repo, err := op.Wait(ctx)
	if err != nil {
		t.Fatalf("op.Wait: %v", err)
	}

	if repo.GetFormat() != artifactregistrypb.Repository_DOCKER {
		t.Fatalf("format=%v want DOCKER", repo.GetFormat())
	}
}
