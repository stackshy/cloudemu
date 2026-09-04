package acr_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	azacr "github.com/Azure/azure-sdk-for-go/sdk/containers/azcontainerregistry"

	crdriver "github.com/stackshy/cloudemu/v2/services/containerregistry/driver"
)

// digestOf returns the digest of tag in repo via the driver, so tests can drive
// PATCH /_manifests/{digest} without depending on push ordering.
func digestOf(t *testing.T, reg crdriver.ContainerRegistry, repo, tag string) string {
	t.Helper()

	img, err := reg.GetImage(context.Background(), repo, tag)
	if err != nil {
		t.Fatalf("GetImage(%s:%s): %v", repo, tag, err)
	}

	return img.Digest
}

// TestSDKACRTagDeleteEnabledBlocksDeleteTag drives the real azcontainerregistry
// SDK's UpdateTagProperties/DeleteTag: locking a tag's deleteEnabled blocks
// untagging, and clearing the lock lets it through.
func TestSDKACRTagDeleteEnabledBlocksDeleteTag(t *testing.T) {
	client, reg := newACRClient(t)
	ctx := context.Background()

	if _, err := reg.CreateRepository(ctx, crdriver.RepositoryConfig{Name: "app"}); err != nil {
		t.Fatalf("seed CreateRepository: %v", err)
	}

	seedImage(t, reg, "app", "v1")

	if _, err := client.UpdateTagProperties(ctx, "app", "v1", &azacr.ClientUpdateTagPropertiesOptions{
		Value: &azacr.TagWriteableProperties{CanDelete: to.Ptr(false)},
	}); err != nil {
		t.Fatalf("UpdateTagProperties(deleteEnabled=false): %v", err)
	}

	_, err := client.DeleteTag(ctx, "app", "v1", nil)
	assertResponseCode(t, err, http.StatusConflict)

	if _, err := client.UpdateTagProperties(ctx, "app", "v1", &azacr.ClientUpdateTagPropertiesOptions{
		Value: &azacr.TagWriteableProperties{CanDelete: to.Ptr(true)},
	}); err != nil {
		t.Fatalf("UpdateTagProperties(deleteEnabled=true): %v", err)
	}

	if _, err := client.DeleteTag(ctx, "app", "v1", nil); err != nil {
		t.Fatalf("DeleteTag after unlock: %v", err)
	}
}

// TestSDKACRTagWriteEnabledBlocksOverwrite locks a tag's writeEnabled and
// proves a push that would move it to a new digest is rejected, while the
// tag's existing digest is left untouched; clearing the lock lets the push
// through. ACR's data plane has no push endpoint, so the push itself goes
// through the driver directly, matching what `docker push` would do.
func TestSDKACRTagWriteEnabledBlocksOverwrite(t *testing.T) {
	client, reg := newACRClient(t)
	ctx := context.Background()

	if _, err := reg.CreateRepository(ctx, crdriver.RepositoryConfig{Name: "app"}); err != nil {
		t.Fatalf("seed CreateRepository: %v", err)
	}

	seedImage(t, reg, "app", "v1")
	original := digestOf(t, reg, "app", "v1")

	if _, err := client.UpdateTagProperties(ctx, "app", "v1", &azacr.ClientUpdateTagPropertiesOptions{
		Value: &azacr.TagWriteableProperties{CanWrite: to.Ptr(false)},
	}); err != nil {
		t.Fatalf("UpdateTagProperties(writeEnabled=false): %v", err)
	}

	if _, err := reg.PutImage(ctx, &crdriver.ImageManifest{
		Repository: "app",
		Tag:        "v1",
		SizeBytes:  4096,
	}); err == nil {
		t.Fatal("expected write-locked overwrite to be rejected")
	}

	if got := digestOf(t, reg, "app", "v1"); got != original {
		t.Fatalf("tag digest changed under write lock: got %s, want %s", got, original)
	}

	if _, err := client.UpdateTagProperties(ctx, "app", "v1", &azacr.ClientUpdateTagPropertiesOptions{
		Value: &azacr.TagWriteableProperties{CanWrite: to.Ptr(true)},
	}); err != nil {
		t.Fatalf("UpdateTagProperties(writeEnabled=true): %v", err)
	}

	if _, err := reg.PutImage(ctx, &crdriver.ImageManifest{
		Repository: "app",
		Tag:        "v1",
		SizeBytes:  4096,
	}); err != nil {
		t.Fatalf("PutImage after unlock: %v", err)
	}

	if got := digestOf(t, reg, "app", "v1"); got == original {
		t.Fatal("expected tag digest to move after unlock")
	}
}

// TestSDKACRTagListEnabledHidesFromList proves a listEnabled=false tag drops
// out of the _tags listing while remaining directly gettable by name.
func TestSDKACRTagListEnabledHidesFromList(t *testing.T) {
	client, reg := newACRClient(t)
	ctx := context.Background()

	if _, err := reg.CreateRepository(ctx, crdriver.RepositoryConfig{Name: "app"}); err != nil {
		t.Fatalf("seed CreateRepository: %v", err)
	}

	seedImage(t, reg, "app", "v1")
	seedImage(t, reg, "app", "v2")

	if _, err := client.UpdateTagProperties(ctx, "app", "v1", &azacr.ClientUpdateTagPropertiesOptions{
		Value: &azacr.TagWriteableProperties{CanList: to.Ptr(false)},
	}); err != nil {
		t.Fatalf("UpdateTagProperties(listEnabled=false): %v", err)
	}

	var tags []string

	pager := client.NewListTagsPager("app", nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("ListTags: %v", err)
		}

		for _, tag := range page.Tags {
			tags = append(tags, *tag.Name)
		}
	}

	if contains(tags, "v1") {
		t.Fatalf("list-locked tag v1 should be hidden, tags=%v", tags)
	}

	if !contains(tags, "v2") {
		t.Fatalf("tag v2 should still be listed, tags=%v", tags)
	}

	props, err := client.GetTagProperties(ctx, "app", "v1", nil)
	if err != nil {
		t.Fatalf("GetTagProperties(v1) should still succeed when only listEnabled is false: %v", err)
	}

	if props.Tag.ChangeableAttributes.CanList == nil || *props.Tag.ChangeableAttributes.CanList {
		t.Fatal("expected reported listEnabled=false")
	}
}

// TestSDKACRRepositoryAttributesLifecycle covers repository-level
// changeableAttributes: listEnabled hides the repo from the catalog, and
// deleteEnabled blocks DeleteRepository until cleared.
func TestSDKACRRepositoryAttributesLifecycle(t *testing.T) {
	client, reg := newACRClient(t)
	ctx := context.Background()

	if _, err := reg.CreateRepository(ctx, crdriver.RepositoryConfig{Name: "locked"}); err != nil {
		t.Fatalf("seed CreateRepository: %v", err)
	}

	if _, err := client.UpdateRepositoryProperties(ctx, "locked", &azacr.ClientUpdateRepositoryPropertiesOptions{
		Value: &azacr.RepositoryWriteableProperties{CanList: to.Ptr(false), CanDelete: to.Ptr(false)},
	}); err != nil {
		t.Fatalf("UpdateRepositoryProperties: %v", err)
	}

	var names []string

	pager := client.NewListRepositoriesPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("ListRepositories: %v", err)
		}

		for _, n := range page.Repositories.Names {
			names = append(names, *n)
		}
	}

	if contains(names, "locked") {
		t.Fatalf("list-locked repository should be hidden from catalog, names=%v", names)
	}

	if _, err := client.GetRepositoryProperties(ctx, "locked", nil); err != nil {
		t.Fatalf("GetRepositoryProperties should still succeed when only listEnabled is false: %v", err)
	}

	_, err := client.DeleteRepository(ctx, "locked", nil)
	assertResponseCode(t, err, http.StatusConflict)

	if _, err := client.UpdateRepositoryProperties(ctx, "locked", &azacr.ClientUpdateRepositoryPropertiesOptions{
		Value: &azacr.RepositoryWriteableProperties{CanDelete: to.Ptr(true)},
	}); err != nil {
		t.Fatalf("UpdateRepositoryProperties(deleteEnabled=true): %v", err)
	}

	if _, err := client.DeleteRepository(ctx, "locked", nil); err != nil {
		t.Fatalf("DeleteRepository after unlock: %v", err)
	}
}

// TestSDKACRRepositoryWriteEnabledBlocksPush proves a write-locked repository
// rejects any push, tag-scoped or not.
func TestSDKACRRepositoryWriteEnabledBlocksPush(t *testing.T) {
	client, reg := newACRClient(t)
	ctx := context.Background()

	if _, err := reg.CreateRepository(ctx, crdriver.RepositoryConfig{Name: "app"}); err != nil {
		t.Fatalf("seed CreateRepository: %v", err)
	}

	if _, err := client.UpdateRepositoryProperties(ctx, "app", &azacr.ClientUpdateRepositoryPropertiesOptions{
		Value: &azacr.RepositoryWriteableProperties{CanWrite: to.Ptr(false)},
	}); err != nil {
		t.Fatalf("UpdateRepositoryProperties(writeEnabled=false): %v", err)
	}

	if _, err := reg.PutImage(ctx, &crdriver.ImageManifest{Repository: "app", Tag: "v1", SizeBytes: 512}); err == nil {
		t.Fatal("expected push to a write-locked repository to be rejected")
	}
}

// TestSDKACRManifestAttributesLifecycle covers manifest-level
// changeableAttributes: listEnabled hides the manifest from _manifests, and
// deleteEnabled blocks deletion until cleared.
func TestSDKACRManifestAttributesLifecycle(t *testing.T) {
	client, reg := newACRClient(t)
	ctx := context.Background()

	if _, err := reg.CreateRepository(ctx, crdriver.RepositoryConfig{Name: "app"}); err != nil {
		t.Fatalf("seed CreateRepository: %v", err)
	}

	seedImage(t, reg, "app", "v1")
	digest := digestOf(t, reg, "app", "v1")

	if _, err := client.UpdateManifestProperties(ctx, "app", digest, &azacr.ClientUpdateManifestPropertiesOptions{
		Value: &azacr.ManifestWriteableProperties{CanList: to.Ptr(false), CanDelete: to.Ptr(false)},
	}); err != nil {
		t.Fatalf("UpdateManifestProperties: %v", err)
	}

	var digests []string

	pager := client.NewListManifestsPager("app", nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("ListManifests: %v", err)
		}

		for _, m := range page.Manifests.Attributes {
			if m.Digest != nil {
				digests = append(digests, *m.Digest)
			}
		}
	}

	if contains(digests, digest) {
		t.Fatalf("list-locked manifest should be hidden from _manifests, digests=%v", digests)
	}

	if _, err := client.GetManifestProperties(ctx, "app", digest, nil); err != nil {
		t.Fatalf("GetManifestProperties should still succeed when only listEnabled is false: %v", err)
	}

	if err := reg.DeleteImage(ctx, "app", digest); err == nil {
		t.Fatal("expected delete of a delete-locked manifest to be rejected")
	}

	if _, err := client.UpdateManifestProperties(ctx, "app", digest, &azacr.ClientUpdateManifestPropertiesOptions{
		Value: &azacr.ManifestWriteableProperties{CanDelete: to.Ptr(true)},
	}); err != nil {
		t.Fatalf("UpdateManifestProperties(deleteEnabled=true): %v", err)
	}

	if err := reg.DeleteImage(ctx, "app", digest); err != nil {
		t.Fatalf("DeleteImage after unlock: %v", err)
	}
}

// TestSDKACRUpdateAttributesNotFound proves PATCH on an unknown repository,
// tag, or manifest 404s rather than silently creating a lock record.
func TestSDKACRUpdateAttributesNotFound(t *testing.T) {
	client, reg := newACRClient(t)
	ctx := context.Background()

	if _, err := reg.CreateRepository(ctx, crdriver.RepositoryConfig{Name: "app"}); err != nil {
		t.Fatalf("seed CreateRepository: %v", err)
	}

	seedImage(t, reg, "app", "v1")

	_, err := client.UpdateRepositoryProperties(ctx, "ghost", &azacr.ClientUpdateRepositoryPropertiesOptions{
		Value: &azacr.RepositoryWriteableProperties{CanDelete: to.Ptr(false)},
	})
	assertResponseCode(t, err, http.StatusNotFound)

	_, err = client.UpdateTagProperties(ctx, "app", "ghost", &azacr.ClientUpdateTagPropertiesOptions{
		Value: &azacr.TagWriteableProperties{CanDelete: to.Ptr(false)},
	})
	assertResponseCode(t, err, http.StatusNotFound)

	_, err = client.UpdateManifestProperties(ctx, "app", "sha256:0000000000000000", &azacr.ClientUpdateManifestPropertiesOptions{
		Value: &azacr.ManifestWriteableProperties{CanDelete: to.Ptr(false)},
	})
	assertResponseCode(t, err, http.StatusNotFound)
}
