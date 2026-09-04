package artifactregistry_test

import (
	"context"
	"testing"

	crdriver "github.com/stackshy/cloudemu/v2/services/containerregistry/driver"
	ar "google.golang.org/api/artifactregistry/v1"
)

// setupTaggedRepo creates a repository (optionally with dockerConfig.immutableTags)
// and seeds one image tagged v1, returning the package/version resource name
// (which doubles as the version id in this model, since a package has exactly
// one version keyed by its digest).
func setupTaggedRepo(t *testing.T, svc *ar.Service, reg crdriver.ContainerRegistry, repoID string, immutable bool) (pkgName, digest string) {
	t.Helper()

	ctx := context.Background()

	var docker *ar.DockerRepositoryConfig
	if immutable {
		docker = &ar.DockerRepositoryConfig{ImmutableTags: true}
	}

	if _, err := svc.Projects.Locations.Repositories.Create(testParent, &ar.Repository{
		Format:       "DOCKER",
		DockerConfig: docker,
	}).RepositoryId(repoID).Context(ctx).Do(); err != nil {
		t.Fatalf("Create repository: %v", err)
	}

	img, err := reg.PutImage(ctx, &crdriver.ImageManifest{
		Repository: repoID,
		Tag:        "v1",
		Digest:     "sha256:" + repoID,
		SizeBytes:  1024,
	})
	if err != nil {
		t.Fatalf("seed PutImage: %v", err)
	}

	return testParent + "/repositories/" + repoID + "/packages/" + img.Digest, img.Digest
}

// TestSDKArtifactRegistryTagsCreate guards packages.tags.create: a brand-new
// tag id targeting the package's own version succeeds and is visible via
// Tags.Get; a duplicate tag id is ALREADY_EXISTS; a version referencing a
// different (nonexistent) package is NOT_FOUND.
func TestSDKArtifactRegistryTagsCreate(t *testing.T) {
	svc, reg := newARService(t)
	ctx := context.Background()

	pkgName, digest := setupTaggedRepo(t, svc, reg, "tagcreate", false)

	created, err := svc.Projects.Locations.Repositories.Packages.Tags.
		Create(pkgName, &ar.Tag{Version: pkgName + "/versions/" + digest}).
		TagId("release").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Tags.Create: %v", err)
	}

	if created.Version != pkgName+"/versions/"+digest {
		t.Fatalf("created tag version=%q want %s", created.Version, pkgName+"/versions/"+digest)
	}

	got, err := svc.Projects.Locations.Repositories.Packages.Tags.Get(pkgName + "/tags/release").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Tags.Get after create: %v", err)
	}

	if got.Name != pkgName+"/tags/release" {
		t.Fatalf("Tags.Get name=%q want %s/tags/release", got.Name, pkgName)
	}

	_, err = svc.Projects.Locations.Repositories.Packages.Tags.
		Create(pkgName, &ar.Tag{Version: pkgName + "/versions/" + digest}).
		TagId("release").Context(ctx).Do()
	assertGoogleAPICode(t, err, 409)

	_, err = svc.Projects.Locations.Repositories.Packages.Tags.
		Create(pkgName, &ar.Tag{Version: pkgName + "/versions/sha256:doesnotexist"}).
		TagId("bad-version").Context(ctx).Do()
	assertGoogleAPICode(t, err, 404)
}

// TestSDKArtifactRegistryTagsDeleteImmutableGuard guards the immutableTags
// enforcement B-finding: a Docker repository created with
// dockerConfig.immutableTags=true rejects tags.delete with FAILED_PRECONDITION
// (mapped to HTTP 409 by the shared gcprest codec), while an identical
// non-immutable repository allows it.
func TestSDKArtifactRegistryTagsDeleteImmutableGuard(t *testing.T) {
	svc, reg := newARService(t)
	ctx := context.Background()

	t.Run("immutable repo blocks delete", func(t *testing.T) {
		pkgName, _ := setupTaggedRepo(t, svc, reg, "immdelete", true)

		_, err := svc.Projects.Locations.Repositories.Packages.Tags.Delete(pkgName + "/tags/v1").Context(ctx).Do()
		assertGoogleAPICode(t, err, 409)

		// The tag must still be there after the rejected delete.
		if _, err := svc.Projects.Locations.Repositories.Packages.Tags.
			Get(pkgName + "/tags/v1").Context(ctx).Do(); err != nil {
			t.Fatalf("tag should survive a blocked delete: %v", err)
		}
	})

	t.Run("mutable repo allows delete", func(t *testing.T) {
		pkgName, _ := setupTaggedRepo(t, svc, reg, "mutdelete", false)

		if _, err := svc.Projects.Locations.Repositories.Packages.Tags.
			Delete(pkgName + "/tags/v1").Context(ctx).Do(); err != nil {
			t.Fatalf("Tags.Delete on mutable repo: %v", err)
		}

		_, err := svc.Projects.Locations.Repositories.Packages.Tags.Get(pkgName + "/tags/v1").Context(ctx).Do()
		assertGoogleAPICode(t, err, 404)

		// The version/package itself must survive an untag.
		if _, err := svc.Projects.Locations.Repositories.Packages.Versions.
			Get(pkgName + "/versions/" + lastSegment(pkgName)).Context(ctx).Do(); err != nil {
			t.Fatalf("version should survive tags.delete: %v", err)
		}
	})

	t.Run("delete of unknown tag is not found", func(t *testing.T) {
		pkgName, _ := setupTaggedRepo(t, svc, reg, "missingtag", false)

		_, err := svc.Projects.Locations.Repositories.Packages.Tags.
			Delete(pkgName + "/tags/ghost").Context(ctx).Do()
		assertGoogleAPICode(t, err, 404)
	})
}

// TestSDKArtifactRegistryTagsPatchImmutableGuard guards packages.tags.patch:
// blocked by FAILED_PRECONDITION on an immutable-tags repository, allowed
// (confirming the same version) on a mutable one, and NOT_FOUND when the
// patch targets a version outside the tag's own package.
func TestSDKArtifactRegistryTagsPatchImmutableGuard(t *testing.T) {
	svc, reg := newARService(t)
	ctx := context.Background()

	t.Run("immutable repo blocks patch", func(t *testing.T) {
		pkgName, digest := setupTaggedRepo(t, svc, reg, "immpatch", true)

		_, err := svc.Projects.Locations.Repositories.Packages.Tags.
			Patch(pkgName+"/tags/v1", &ar.Tag{Version: pkgName + "/versions/" + digest}).
			UpdateMask("version").Context(ctx).Do()
		assertGoogleAPICode(t, err, 409)
	})

	t.Run("mutable repo allows patch to the same version", func(t *testing.T) {
		pkgName, digest := setupTaggedRepo(t, svc, reg, "mutpatch", false)

		patched, err := svc.Projects.Locations.Repositories.Packages.Tags.
			Patch(pkgName+"/tags/v1", &ar.Tag{Version: pkgName + "/versions/" + digest}).
			UpdateMask("version").Context(ctx).Do()
		if err != nil {
			t.Fatalf("Tags.Patch: %v", err)
		}

		if patched.Version != pkgName+"/versions/"+digest {
			t.Fatalf("patched version=%q want %s", patched.Version, pkgName+"/versions/"+digest)
		}
	})

	t.Run("patch referencing a foreign version is not found", func(t *testing.T) {
		pkgName, _ := setupTaggedRepo(t, svc, reg, "patchforeign", false)

		_, err := svc.Projects.Locations.Repositories.Packages.Tags.
			Patch(pkgName+"/tags/v1", &ar.Tag{Version: pkgName + "/versions/sha256:elsewhere"}).
			UpdateMask("version").Context(ctx).Do()
		assertGoogleAPICode(t, err, 404)
	})

	t.Run("patch of an unknown tag is not found", func(t *testing.T) {
		pkgName, digest := setupTaggedRepo(t, svc, reg, "patchmissing", false)

		_, err := svc.Projects.Locations.Repositories.Packages.Tags.
			Patch(pkgName+"/tags/ghost", &ar.Tag{Version: pkgName + "/versions/" + digest}).
			UpdateMask("version").Context(ctx).Do()
		assertGoogleAPICode(t, err, 404)
	})
}

// lastSegment returns the final "/"-delimited segment of a resource name.
func lastSegment(name string) string {
	for i := len(name) - 1; i >= 0; i-- {
		if name[i] == '/' {
			return name[i+1:]
		}
	}

	return name
}
