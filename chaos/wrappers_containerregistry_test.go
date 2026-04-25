package chaos_test

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu"
	"github.com/stackshy/cloudemu/chaos"
	"github.com/stackshy/cloudemu/config"
	regdriver "github.com/stackshy/cloudemu/containerregistry/driver"
)

func newChaosContainerRegistry(t *testing.T) (regdriver.ContainerRegistry, *chaos.Engine) {
	t.Helper()

	e := chaos.New(config.RealClock{})
	t.Cleanup(e.Stop)

	return chaos.WrapContainerRegistry(cloudemu.NewAWS().ECR, e), e
}

func TestWrapContainerRegistryCreateRepositoryChaos(t *testing.T) {
	r, e := newChaosContainerRegistry(t)
	ctx := context.Background()

	if _, err := r.CreateRepository(ctx, regdriver.RepositoryConfig{Name: "ok"}); err != nil {
		t.Fatalf("baseline: %v", err)
	}

	e.Apply(chaos.ServiceOutage("containerregistry", time.Hour))

	if _, err := r.CreateRepository(ctx, regdriver.RepositoryConfig{Name: "fail"}); err == nil {
		t.Error("expected chaos error on CreateRepository")
	}
}

func TestWrapContainerRegistryDeleteRepositoryChaos(t *testing.T) {
	r, e := newChaosContainerRegistry(t)
	ctx := context.Background()
	_, _ = r.CreateRepository(ctx, regdriver.RepositoryConfig{Name: "del"})

	e.Apply(chaos.ServiceOutage("containerregistry", time.Hour))

	if err := r.DeleteRepository(ctx, "del", false); err == nil {
		t.Error("expected chaos error on DeleteRepository")
	}
}

func TestWrapContainerRegistryGetRepositoryChaos(t *testing.T) {
	r, e := newChaosContainerRegistry(t)
	ctx := context.Background()
	_, _ = r.CreateRepository(ctx, regdriver.RepositoryConfig{Name: "g"})

	e.Apply(chaos.ServiceOutage("containerregistry", time.Hour))

	if _, err := r.GetRepository(ctx, "g"); err == nil {
		t.Error("expected chaos error on GetRepository")
	}
}

func TestWrapContainerRegistryListRepositoriesChaos(t *testing.T) {
	r, e := newChaosContainerRegistry(t)
	ctx := context.Background()

	e.Apply(chaos.ServiceOutage("containerregistry", time.Hour))

	if _, err := r.ListRepositories(ctx); err == nil {
		t.Error("expected chaos error on ListRepositories")
	}
}

func TestWrapContainerRegistryPutImageChaos(t *testing.T) {
	r, e := newChaosContainerRegistry(t)
	ctx := context.Background()
	_, _ = r.CreateRepository(ctx, regdriver.RepositoryConfig{Name: "img"})

	e.Apply(chaos.ServiceOutage("containerregistry", time.Hour))

	m := &regdriver.ImageManifest{Repository: "img", Tag: "v1", Digest: "sha256:abc", SizeBytes: 100}
	if _, err := r.PutImage(ctx, m); err == nil {
		t.Error("expected chaos error on PutImage")
	}
}

func TestWrapContainerRegistryGetImageChaos(t *testing.T) {
	r, e := newChaosContainerRegistry(t)
	ctx := context.Background()
	_, _ = r.CreateRepository(ctx, regdriver.RepositoryConfig{Name: "gimg"})

	e.Apply(chaos.ServiceOutage("containerregistry", time.Hour))

	if _, err := r.GetImage(ctx, "gimg", "v1"); err == nil {
		t.Error("expected chaos error on GetImage")
	}
}

func TestWrapContainerRegistryListImagesChaos(t *testing.T) {
	r, e := newChaosContainerRegistry(t)
	ctx := context.Background()
	_, _ = r.CreateRepository(ctx, regdriver.RepositoryConfig{Name: "limg"})

	e.Apply(chaos.ServiceOutage("containerregistry", time.Hour))

	if _, err := r.ListImages(ctx, "limg"); err == nil {
		t.Error("expected chaos error on ListImages")
	}
}

func TestWrapContainerRegistryDeleteImageChaos(t *testing.T) {
	r, e := newChaosContainerRegistry(t)
	ctx := context.Background()
	_, _ = r.CreateRepository(ctx, regdriver.RepositoryConfig{Name: "dimg"})

	e.Apply(chaos.ServiceOutage("containerregistry", time.Hour))

	if err := r.DeleteImage(ctx, "dimg", "v1"); err == nil {
		t.Error("expected chaos error on DeleteImage")
	}
}
