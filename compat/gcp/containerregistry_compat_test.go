package gcp

import (
	"context"
	"fmt"
	"testing"

	ar "google.golang.org/api/artifactregistry/v1"
	"google.golang.org/api/option"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/internal/compat"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
	crdriver "github.com/stackshy/cloudemu/v2/services/containerregistry/driver"
)

// TestGCPContainerRegistryCompat drives an Artifact Registry repository +
// docker-image lifecycle through the real google.golang.org/api/artifactregistry/v1
// client. Artifact Registry maps onto the portable "containerregistry" driver,
// so operation names match AWS ECR's in docs/coverage/coverage.json.
//
// The v1 REST API only exposes repository CRUD + a dockerImages list — image
// mutation (PutImage/TagImage/DeleteImage), scanning and lifecycle policies
// have no REST surface, so those coverage ops are gaps for GCP and are not
// asserted here.
func TestGCPContainerRegistryCompat(t *testing.T) {
	provider := cloudemu.NewGCP()
	sess := compat.BootGCP(t, gcpserver.Drivers{ArtifactRegistry: provider.ArtifactRegistry})
	ctx := context.Background()

	svcClient, err := ar.NewService(ctx,
		option.WithEndpoint(sess.Endpoint()),
		option.WithoutAuthentication(),
		option.WithHTTPClient(sess.Transport()),
	)
	if err != nil {
		t.Fatalf("artifactregistry service: %v", err)
	}

	const (
		svc      = "containerregistry"
		repoID   = "app-images"
		location = "us"
		imageTag = "v1"
		imageSz  = 1024
	)

	parent := "projects/" + compat.GCPProject + "/locations/" + location
	name := parent + "/repositories/" + repoID

	sess.Op(svc, "CreateRepository", func() error {
		op, err := svcClient.Projects.Locations.Repositories.Create(parent, &ar.Repository{
			Format: "DOCKER",
			Labels: map[string]string{"team": "platform"},
		}).RepositoryId(repoID).Context(ctx).Do()
		if err != nil {
			return err
		}

		if !op.Done {
			return fmt.Errorf("CreateRepository operation not done: %+v", op)
		}

		return nil
	})

	sess.Op(svc, "GetRepository", func() error {
		got, err := svcClient.Projects.Locations.Repositories.Get(name).Context(ctx).Do()
		if err != nil {
			return err
		}

		if got.Name != name {
			return fmt.Errorf("GetRepository name = %q, want %q", got.Name, name)
		}

		return nil
	})

	sess.Op(svc, "ListRepositories", func() error {
		list, err := svcClient.Projects.Locations.Repositories.List(parent).Context(ctx).Do()
		if err != nil {
			return err
		}

		if len(list.Repositories) != 1 {
			return fmt.Errorf("ListRepositories = %d, want 1", len(list.Repositories))
		}

		return nil
	})

	// Artifact Registry has no REST "push image" call — images arrive via
	// docker push. Seed one through the driver so the SDK ListImages below has
	// something to return. This is setup, not an attributed compat op.
	if _, err := provider.ArtifactRegistry.PutImage(ctx, &crdriver.ImageManifest{
		Repository: repoID,
		Tag:        imageTag,
		MediaType:  "application/vnd.docker.distribution.manifest.v2+json",
		SizeBytes:  imageSz,
	}); err != nil {
		t.Fatalf("seed PutImage: %v", err)
	}

	sess.Op(svc, "ListImages", func() error {
		listed, err := svcClient.Projects.Locations.Repositories.DockerImages.
			List(name).Context(ctx).Do()
		if err != nil {
			return err
		}

		if len(listed.DockerImages) != 1 {
			return fmt.Errorf("ListImages = %d, want 1", len(listed.DockerImages))
		}

		return nil
	})

	sess.Op(svc, "DeleteRepository", func() error {
		_, err := svcClient.Projects.Locations.Repositories.Delete(name).Context(ctx).Do()

		return err
	})
}

// TestArtifactRegistryPackageVersionDelete proves packages.delete and
// packages.versions.delete remove the backing image and return a done LRO,
// rather than 404 (the sub-collections were previously read-only GET). Each
// image is modeled as one package (id = digest) with one version (the digest).
func TestArtifactRegistryPackageVersionDelete(t *testing.T) {
	const (
		repoID   = "del-images"
		location = "us"
		imageSz  = 2048
	)

	provider := cloudemu.NewGCP()
	sess := compat.BootGCP(t, gcpserver.Drivers{ArtifactRegistry: provider.ArtifactRegistry})
	ctx := context.Background()

	svcClient, err := ar.NewService(ctx,
		option.WithEndpoint(sess.Endpoint()),
		option.WithoutAuthentication(),
		option.WithHTTPClient(sess.Transport()),
	)
	if err != nil {
		t.Fatalf("artifactregistry service: %v", err)
	}

	parent := "projects/" + compat.GCPProject + "/locations/" + location
	name := parent + "/repositories/" + repoID

	createOp, err := svcClient.Projects.Locations.Repositories.Create(parent, &ar.Repository{
		Format: "DOCKER",
	}).RepositoryId(repoID).Context(ctx).Do()
	if err != nil || !createOp.Done {
		t.Fatalf("create repo: %v (done=%v)", err, createOp != nil && createOp.Done)
	}

	// Seed two images (distinct digests) — one exercised via packages.delete, the
	// other via versions.delete.
	seed := func(tag string) string {
		img, perr := provider.ArtifactRegistry.PutImage(ctx, &crdriver.ImageManifest{
			Repository: repoID,
			Tag:        tag,
			MediaType:  "application/vnd.docker.distribution.manifest.v2+json",
			SizeBytes:  imageSz,
		})
		if perr != nil {
			t.Fatalf("seed PutImage %s: %v", tag, perr)
		}

		return img.Digest
	}

	pkgDigest := seed("pkg")
	verDigest := seed("ver")

	pkgName := name + "/packages/" + pkgDigest

	delPkgOp, err := svcClient.Projects.Locations.Repositories.Packages.Delete(pkgName).Context(ctx).Do()
	if err != nil {
		t.Fatalf("packages.delete: %v", err)
	}

	if !delPkgOp.Done {
		t.Fatalf("packages.delete operation not done: %+v", delPkgOp)
	}

	if _, err := svcClient.Projects.Locations.Repositories.Packages.Get(pkgName).Context(ctx).Do(); err == nil {
		t.Fatalf("package still readable after delete")
	}

	verName := name + "/packages/" + verDigest + "/versions/" + verDigest

	delVerOp, err := svcClient.Projects.Locations.Repositories.Packages.Versions.Delete(verName).Context(ctx).Do()
	if err != nil {
		t.Fatalf("versions.delete: %v", err)
	}

	if !delVerOp.Done {
		t.Fatalf("versions.delete operation not done: %+v", delVerOp)
	}
}
