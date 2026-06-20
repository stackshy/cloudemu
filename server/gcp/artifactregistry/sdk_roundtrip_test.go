package artifactregistry_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/stackshy/cloudemu"
	crdriver "github.com/stackshy/cloudemu/containerregistry/driver"
	gcpserver "github.com/stackshy/cloudemu/server/gcp"
	ar "google.golang.org/api/artifactregistry/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

const (
	testParent = "projects/demo/locations/us"
)

func newARService(t *testing.T) (*ar.Service, crdriver.ContainerRegistry) {
	t.Helper()

	cloud := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.Drivers{ArtifactRegistry: cloud.ArtifactRegistry})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	svc, err := ar.NewService(context.Background(),
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("artifactregistry.NewService: %v", err)
	}

	return svc, cloud.ArtifactRegistry
}

func TestSDKArtifactRegistryRepositoryLifecycle(t *testing.T) {
	svc, _ := newARService(t)
	ctx := context.Background()

	op, err := svc.Projects.Locations.Repositories.Create(testParent, &ar.Repository{
		Format: "DOCKER",
		Labels: map[string]string{"team": "platform"},
	}).RepositoryId("myrepo").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if !op.Done {
		t.Fatalf("Create operation not marked done: %+v", op)
	}

	name := testParent + "/repositories/myrepo"

	repo, err := svc.Projects.Locations.Repositories.Get(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if repo.Name != name || repo.Format != "DOCKER" {
		t.Fatalf("Get returned %+v", repo)
	}

	list, err := svc.Projects.Locations.Repositories.List(testParent).Context(ctx).Do()
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(list.Repositories) != 1 {
		t.Fatalf("got %d repositories, want 1", len(list.Repositories))
	}

	if _, err := svc.Projects.Locations.Repositories.Delete(name).Context(ctx).Do(); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err = svc.Projects.Locations.Repositories.Get(name).Context(ctx).Do()
	assertGoogleAPICode(t, err, 404)
}

func TestSDKArtifactRegistryDockerImages(t *testing.T) {
	svc, reg := newARService(t)
	ctx := context.Background()

	if _, err := svc.Projects.Locations.Repositories.Create(testParent, &ar.Repository{Format: "DOCKER"}).
		RepositoryId("imgs").Context(ctx).Do(); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Artifact Registry has no REST "push image" call — images arrive via
	// docker push. Seed one through the driver to simulate that.
	if _, err := reg.PutImage(ctx, &crdriver.ImageManifest{
		Repository: "imgs",
		Tag:        "v1",
		MediaType:  "application/vnd.docker.distribution.manifest.v2+json",
		SizeBytes:  1024,
	}); err != nil {
		t.Fatalf("seed PutImage: %v", err)
	}

	listed, err := svc.Projects.Locations.Repositories.DockerImages.
		List(testParent + "/repositories/imgs").Context(ctx).Do()
	if err != nil {
		t.Fatalf("DockerImages.List: %v", err)
	}

	if len(listed.DockerImages) != 1 {
		t.Fatalf("got %d docker images, want 1", len(listed.DockerImages))
	}

	if !contains(listed.DockerImages[0].Tags, "v1") || listed.DockerImages[0].ImageSizeBytes != 1024 {
		t.Fatalf("DockerImages.List returned %+v", listed.DockerImages[0])
	}
}

func TestSDKArtifactRegistryErrors(t *testing.T) {
	svc, _ := newARService(t)
	ctx := context.Background()

	_, err := svc.Projects.Locations.Repositories.Get(testParent + "/repositories/ghost").Context(ctx).Do()
	assertGoogleAPICode(t, err, 404)

	mk := func() error {
		_, e := svc.Projects.Locations.Repositories.Create(testParent, &ar.Repository{Format: "DOCKER"}).
			RepositoryId("dup").Context(ctx).Do()

		return e
	}

	if err := mk(); err != nil {
		t.Fatalf("first Create: %v", err)
	}

	assertGoogleAPICode(t, mk(), 409)
}

func assertGoogleAPICode(t *testing.T, err error, want int) {
	t.Helper()

	var gerr *googleapi.Error
	if !errors.As(err, &gerr) {
		t.Fatalf("want *googleapi.Error with code %d, got %v", want, err)
	}

	if gerr.Code != want {
		t.Fatalf("got HTTP %d, want %d (%v)", gerr.Code, want, err)
	}
}

func contains(s []string, want string) bool {
	for _, v := range s {
		if v == want {
			return true
		}
	}

	return false
}
