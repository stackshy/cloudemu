package artifactregistry_test

import (
	"context"
	"strings"
	"testing"

	ar "google.golang.org/api/artifactregistry/v1"

	crdriver "github.com/stackshy/cloudemu/v2/services/containerregistry/driver"
)

// TestSDKArtifactRegistrySubCollections guards the BLOCKER finding: packages /
// versions / tags / files list endpoints previously returned the Repository
// body (only dockerImages was special-cased), so clients silently decoded empty
// lists. They must now return the correctly-shaped sub-collections populated
// from repository state.
func TestSDKArtifactRegistrySubCollections(t *testing.T) {
	svc, reg := newARService(t)
	ctx := context.Background()

	repo := testParent + "/repositories/sub"

	if _, err := svc.Projects.Locations.Repositories.Create(testParent, &ar.Repository{Format: "DOCKER"}).
		RepositoryId("sub").Context(ctx).Do(); err != nil {
		t.Fatalf("Create: %v", err)
	}

	seed := func(digest, tag string, size int64) {
		if _, err := reg.PutImage(ctx, &crdriver.ImageManifest{
			Repository: "sub", Tag: tag, Digest: digest, SizeBytes: size,
			MediaType: "application/vnd.docker.distribution.manifest.v2+json",
		}); err != nil {
			t.Fatalf("seed PutImage: %v", err)
		}
	}

	seed("sha256:aaa", "v1", 1024)
	seed("sha256:bbb", "v2", 2048)

	pkgs, err := svc.Projects.Locations.Repositories.Packages.List(repo).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Packages.List: %v", err)
	}

	if len(pkgs.Packages) != 2 {
		t.Fatalf("got %d packages, want 2 (Repository body leaked?)", len(pkgs.Packages))
	}

	if !strings.Contains(pkgs.Packages[0].Name, "/packages/") {
		t.Fatalf("package name not a package resource: %q", pkgs.Packages[0].Name)
	}

	pkgParent := repo + "/packages/sha256:aaa"

	vers, err := svc.Projects.Locations.Repositories.Packages.Versions.List(pkgParent).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Versions.List: %v", err)
	}

	if len(vers.Versions) != 1 || !strings.Contains(vers.Versions[0].Name, "/versions/sha256:aaa") {
		t.Fatalf("Versions.List returned %+v", vers.Versions)
	}

	tags, err := svc.Projects.Locations.Repositories.Packages.Tags.List(pkgParent).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Tags.List: %v", err)
	}

	if len(tags.Tags) != 1 || !strings.HasSuffix(tags.Tags[0].Name, "/tags/v1") {
		t.Fatalf("Tags.List returned %+v", tags.Tags)
	}

	files, err := svc.Projects.Locations.Repositories.Files.List(repo).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Files.List: %v", err)
	}

	if len(files.Files) != 2 {
		t.Fatalf("got %d files, want 2", len(files.Files))
	}
}
