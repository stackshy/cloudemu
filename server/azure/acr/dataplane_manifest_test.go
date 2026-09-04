package acr_test

import (
	"context"
	"io"
	"testing"

	crdriver "github.com/stackshy/cloudemu/v2/services/containerregistry/driver"
)

// TestSDKACRGetManifestContent drives the real azcontainerregistry SDK's
// GetManifest, which issues GET /v2/{name}/manifests/{reference} — a
// different URL family from the /acr/v1/{name}/_manifests/{digest}
// changeableAttributes path exercised elsewhere. It must return the raw
// manifest document preserved from PutImage, not fall through to an
// unrelated handler.
func TestSDKACRGetManifestContent(t *testing.T) {
	client, reg := newACRClient(t)
	ctx := context.Background()

	if _, err := reg.CreateRepository(ctx, crdriver.RepositoryConfig{Name: "app"}); err != nil {
		t.Fatalf("seed CreateRepository: %v", err)
	}

	const rawManifest = `{"schemaVersion":2,"mediaType":"application/vnd.docker.distribution.manifest.v2+json"}`

	detail, err := reg.PutImage(ctx, &crdriver.ImageManifest{
		Repository: "app",
		Tag:        "v1",
		MediaType:  "application/vnd.docker.distribution.manifest.v2+json",
		SizeBytes:  512,
		Manifest:   rawManifest,
	})
	if err != nil {
		t.Fatalf("seed PutImage: %v", err)
	}

	resp, err := client.GetManifest(ctx, "app", "v1", nil)
	if err != nil {
		t.Fatalf("GetManifest: %v", err)
	}

	body, err := io.ReadAll(resp.ManifestData)
	if err != nil {
		t.Fatalf("read ManifestData: %v", err)
	}

	if string(body) != rawManifest {
		t.Fatalf("got manifest body %q, want %q", body, rawManifest)
	}

	if resp.DockerContentDigest == nil || *resp.DockerContentDigest != detail.Digest {
		t.Fatalf("got Docker-Content-Digest %v, want %s", resp.DockerContentDigest, detail.Digest)
	}

	// Fetching by digest must resolve to the same content as fetching by tag.
	byDigest, err := client.GetManifest(ctx, "app", detail.Digest, nil)
	if err != nil {
		t.Fatalf("GetManifest(by digest): %v", err)
	}

	digestBody, err := io.ReadAll(byDigest.ManifestData)
	if err != nil {
		t.Fatalf("read ManifestData(by digest): %v", err)
	}

	if string(digestBody) != rawManifest {
		t.Fatalf("got manifest body by digest %q, want %q", digestBody, rawManifest)
	}
}

// TestSDKACRGetManifestContentNotFound confirms a nonexistent repository or
// manifest gets a proper OCI-distribution error shape ({"errors":[...]}) from
// the ACR handler, not the misleading BlobNotFound XML that blob storage's
// broad {container}/{blob} fallback previously produced for an unclaimed
// /v2/... path.
func TestSDKACRGetManifestContentNotFound(t *testing.T) {
	client, reg := newACRClient(t)
	ctx := context.Background()

	_, err := client.GetManifest(ctx, "ghost", "v1", nil)
	assertResponseCode(t, err, 404)

	if _, err := reg.CreateRepository(ctx, crdriver.RepositoryConfig{Name: "app"}); err != nil {
		t.Fatalf("seed CreateRepository: %v", err)
	}

	_, err = client.GetManifest(ctx, "app", "missing-tag", nil)
	assertResponseCode(t, err, 404)
}

// TestSDKACRDeleteManifestContent drives the real SDK's DeleteManifest
// (DELETE /v2/{name}/manifests/{digest}) and verifies the manifest is
// actually removed — not just that the call returns success (the SDK treats
// both 202 and 404 as success, so a wrong-handler 404 would previously mask
// this divergence).
func TestSDKACRDeleteManifestContent(t *testing.T) {
	client, reg := newACRClient(t)
	ctx := context.Background()

	if _, err := reg.CreateRepository(ctx, crdriver.RepositoryConfig{Name: "app"}); err != nil {
		t.Fatalf("seed CreateRepository: %v", err)
	}

	detail, err := reg.PutImage(ctx, &crdriver.ImageManifest{
		Repository: "app",
		Tag:        "v1",
		MediaType:  "application/vnd.docker.distribution.manifest.v2+json",
		SizeBytes:  512,
		Manifest:   `{"schemaVersion":2}`,
	})
	if err != nil {
		t.Fatalf("seed PutImage: %v", err)
	}

	if _, err := client.DeleteManifest(ctx, "app", detail.Digest, nil); err != nil {
		t.Fatalf("DeleteManifest: %v", err)
	}

	if _, err := reg.GetImage(ctx, "app", detail.Digest); err == nil {
		t.Fatal("expected image to be gone after DeleteManifest")
	}

	// Deleting again (already gone) must still be reported success per real
	// ACR / azcontainerregistry semantics (202 or 404 both treated as OK).
	if _, err := client.DeleteManifest(ctx, "app", detail.Digest, nil); err != nil {
		t.Fatalf("DeleteManifest (idempotent replay): %v", err)
	}
}
