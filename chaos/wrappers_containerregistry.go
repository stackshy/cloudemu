package chaos

import (
	"context"

	regdriver "github.com/stackshy/cloudemu/containerregistry/driver"
)

// chaosContainerRegistry wraps a container registry driver. Hot-path:
// repository CRUD + image push/pull/list/delete. Lifecycle policies and
// scanning delegate through.
type chaosContainerRegistry struct {
	regdriver.ContainerRegistry
	engine *Engine
}

// WrapContainerRegistry returns a container registry driver that consults
// engine on repository and image data-plane calls.
func WrapContainerRegistry(inner regdriver.ContainerRegistry, engine *Engine) regdriver.ContainerRegistry {
	return &chaosContainerRegistry{ContainerRegistry: inner, engine: engine}
}

func (c *chaosContainerRegistry) CreateRepository(
	ctx context.Context, cfg regdriver.RepositoryConfig,
) (*regdriver.Repository, error) {
	if err := applyChaos(ctx, c.engine, "containerregistry", "CreateRepository"); err != nil {
		return nil, err
	}

	return c.ContainerRegistry.CreateRepository(ctx, cfg)
}

func (c *chaosContainerRegistry) DeleteRepository(ctx context.Context, name string, force bool) error {
	if err := applyChaos(ctx, c.engine, "containerregistry", "DeleteRepository"); err != nil {
		return err
	}

	return c.ContainerRegistry.DeleteRepository(ctx, name, force)
}

func (c *chaosContainerRegistry) GetRepository(ctx context.Context, name string) (*regdriver.Repository, error) {
	if err := applyChaos(ctx, c.engine, "containerregistry", "GetRepository"); err != nil {
		return nil, err
	}

	return c.ContainerRegistry.GetRepository(ctx, name)
}

func (c *chaosContainerRegistry) ListRepositories(ctx context.Context) ([]regdriver.Repository, error) {
	if err := applyChaos(ctx, c.engine, "containerregistry", "ListRepositories"); err != nil {
		return nil, err
	}

	return c.ContainerRegistry.ListRepositories(ctx)
}

func (c *chaosContainerRegistry) PutImage(
	ctx context.Context, manifest *regdriver.ImageManifest,
) (*regdriver.ImageDetail, error) {
	if err := applyChaos(ctx, c.engine, "containerregistry", "PutImage"); err != nil {
		return nil, err
	}

	return c.ContainerRegistry.PutImage(ctx, manifest)
}

func (c *chaosContainerRegistry) GetImage(
	ctx context.Context, repository, reference string,
) (*regdriver.ImageDetail, error) {
	if err := applyChaos(ctx, c.engine, "containerregistry", "GetImage"); err != nil {
		return nil, err
	}

	return c.ContainerRegistry.GetImage(ctx, repository, reference)
}

func (c *chaosContainerRegistry) ListImages(ctx context.Context, repository string) ([]regdriver.ImageDetail, error) {
	if err := applyChaos(ctx, c.engine, "containerregistry", "ListImages"); err != nil {
		return nil, err
	}

	return c.ContainerRegistry.ListImages(ctx, repository)
}

func (c *chaosContainerRegistry) DeleteImage(ctx context.Context, repository, reference string) error {
	if err := applyChaos(ctx, c.engine, "containerregistry", "DeleteImage"); err != nil {
		return err
	}

	return c.ContainerRegistry.DeleteImage(ctx, repository, reference)
}
