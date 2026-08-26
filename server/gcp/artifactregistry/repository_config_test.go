package artifactregistry_test

import (
	"context"
	"testing"

	ar "google.golang.org/api/artifactregistry/v1"
)

// TestSDKArtifactRegistryImmutableTagsRoundTrip guards B1: a repository created
// with dockerConfig.immutableTags must return the same dockerConfig on Get
// (previously dropped, causing perpetual Terraform drift), and a patch flipping
// it must persist.
func TestSDKArtifactRegistryImmutableTagsRoundTrip(t *testing.T) {
	svc, _ := newARService(t)
	ctx := context.Background()

	name := testParent + "/repositories/immutable"

	if _, err := svc.Projects.Locations.Repositories.Create(testParent, &ar.Repository{
		Format:       "DOCKER",
		DockerConfig: &ar.DockerRepositoryConfig{ImmutableTags: true},
	}).RepositoryId("immutable").Context(ctx).Do(); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := svc.Projects.Locations.Repositories.Get(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.DockerConfig == nil || !got.DockerConfig.ImmutableTags {
		t.Fatalf("dockerConfig=%+v want immutableTags=true", got.DockerConfig)
	}

	if _, err := svc.Projects.Locations.Repositories.Patch(name, &ar.Repository{
		DockerConfig:    &ar.DockerRepositoryConfig{ImmutableTags: false},
		ForceSendFields: []string{"DockerConfig"},
	}).UpdateMask("dockerConfig").Context(ctx).Do(); err != nil {
		t.Fatalf("Patch dockerConfig: %v", err)
	}

	got, err = svc.Projects.Locations.Repositories.Get(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get after patch: %v", err)
	}

	if got.DockerConfig != nil && got.DockerConfig.ImmutableTags {
		t.Fatalf("dockerConfig=%+v want immutableTags cleared", got.DockerConfig)
	}
}

// TestSDKArtifactRegistryCleanupPoliciesRoundTrip guards B2: cleanupPolicies and
// cleanupPolicyDryRun must round-trip on create+get and honor a patch mask.
func TestSDKArtifactRegistryCleanupPoliciesRoundTrip(t *testing.T) {
	svc, _ := newARService(t)
	ctx := context.Background()

	name := testParent + "/repositories/cleanup"

	const keepCount = 5

	policies := map[string]ar.CleanupPolicy{
		"keep-recent": {
			Id:                 "keep-recent",
			Action:             "KEEP",
			MostRecentVersions: &ar.CleanupPolicyMostRecentVersions{KeepCount: keepCount},
		},
	}

	if _, err := svc.Projects.Locations.Repositories.Create(testParent, &ar.Repository{
		Format:              "DOCKER",
		CleanupPolicies:     policies,
		CleanupPolicyDryRun: true,
	}).RepositoryId("cleanup").Context(ctx).Do(); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := svc.Projects.Locations.Repositories.Get(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	pol, ok := got.CleanupPolicies["keep-recent"]
	if !ok {
		t.Fatalf("cleanupPolicies=%+v want key keep-recent", got.CleanupPolicies)
	}

	if pol.Action != "KEEP" || pol.MostRecentVersions == nil || pol.MostRecentVersions.KeepCount != keepCount {
		t.Fatalf("cleanup policy=%+v want KEEP keepCount=%d", pol, keepCount)
	}

	if !got.CleanupPolicyDryRun {
		t.Fatalf("cleanupPolicyDryRun=false want true")
	}

	if _, err := svc.Projects.Locations.Repositories.Patch(name, &ar.Repository{
		CleanupPolicyDryRun: false,
		ForceSendFields:     []string{"CleanupPolicyDryRun"},
	}).UpdateMask("cleanupPolicyDryRun").Context(ctx).Do(); err != nil {
		t.Fatalf("Patch cleanupPolicyDryRun: %v", err)
	}

	got, err = svc.Projects.Locations.Repositories.Get(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get after patch: %v", err)
	}

	if got.CleanupPolicyDryRun {
		t.Fatalf("cleanupPolicyDryRun=true want cleared after patch")
	}

	if _, ok := got.CleanupPolicies["keep-recent"]; !ok {
		t.Fatalf("cleanupPolicies dropped by unrelated patch: %+v", got.CleanupPolicies)
	}
}

// TestSDKArtifactRegistryListFilter guards B3: repositories.list must honor a
// name= filter and narrow the result set.
func TestSDKArtifactRegistryListFilter(t *testing.T) {
	svc, _ := newARService(t)
	ctx := context.Background()

	for _, id := range []string{"alpha", "beta", "gamma"} {
		if _, err := svc.Projects.Locations.Repositories.Create(testParent, &ar.Repository{Format: "DOCKER"}).
			RepositoryId(id).Context(ctx).Do(); err != nil {
			t.Fatalf("Create %s: %v", id, err)
		}
	}

	want := testParent + "/repositories/beta"

	list, err := svc.Projects.Locations.Repositories.List(testParent).
		Filter(`name="` + want + `"`).Context(ctx).Do()
	if err != nil {
		t.Fatalf("List with filter: %v", err)
	}

	if len(list.Repositories) != 1 || list.Repositories[0].Name != want {
		t.Fatalf("filter returned %+v want only %s", list.Repositories, want)
	}
}

// TestSDKArtifactRegistryListOrderBy guards B4: repositories.list must honor
// orderBy=name desc instead of always sorting ascending.
func TestSDKArtifactRegistryListOrderBy(t *testing.T) {
	svc, _ := newARService(t)
	ctx := context.Background()

	for _, id := range []string{"aaa", "mmm", "zzz"} {
		if _, err := svc.Projects.Locations.Repositories.Create(testParent, &ar.Repository{Format: "DOCKER"}).
			RepositoryId(id).Context(ctx).Do(); err != nil {
			t.Fatalf("Create %s: %v", id, err)
		}
	}

	list, err := svc.Projects.Locations.Repositories.List(testParent).
		OrderBy("name desc").Context(ctx).Do()
	if err != nil {
		t.Fatalf("List orderBy: %v", err)
	}

	wantOrder := []string{
		testParent + "/repositories/zzz",
		testParent + "/repositories/mmm",
		testParent + "/repositories/aaa",
	}

	if len(list.Repositories) != len(wantOrder) {
		t.Fatalf("got %d repositories, want %d", len(list.Repositories), len(wantOrder))
	}

	for i, w := range wantOrder {
		if list.Repositories[i].Name != w {
			t.Fatalf("orderBy=name desc pos %d = %s want %s", i, list.Repositories[i].Name, w)
		}
	}
}

// TestSDKArtifactRegistryConfigNoRegression guards that the pre-existing
// format/mode round-trip and updateMask PATCH still work alongside the new
// fields.
func TestSDKArtifactRegistryConfigNoRegression(t *testing.T) {
	svc, _ := newARService(t)
	ctx := context.Background()

	name := testParent + "/repositories/noreg"

	if _, err := svc.Projects.Locations.Repositories.Create(testParent, &ar.Repository{
		Format:      "DOCKER",
		Mode:        "STANDARD_REPOSITORY",
		Description: "orig",
		Labels:      map[string]string{"env": "dev"},
	}).RepositoryId("noreg").Context(ctx).Do(); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := svc.Projects.Locations.Repositories.Patch(name, &ar.Repository{
		Description: "updated",
	}).UpdateMask("description").Context(ctx).Do(); err != nil {
		t.Fatalf("Patch: %v", err)
	}

	got, err := svc.Projects.Locations.Repositories.Get(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Format != "DOCKER" || got.Mode != "STANDARD_REPOSITORY" {
		t.Fatalf("format=%q mode=%q want DOCKER/STANDARD_REPOSITORY", got.Format, got.Mode)
	}

	if got.Description != "updated" {
		t.Fatalf("description=%q want updated", got.Description)
	}

	if got.Labels["env"] != "dev" {
		t.Fatalf("labels=%+v want env=dev preserved through description patch", got.Labels)
	}
}
