package azureai

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/azureai/driver"
)

//nolint:gocritic // cfg matches the driver signature; forwarded unchanged.
func (a *AzureAI) CreateMLWorkspace(ctx context.Context, cfg driver.MLWorkspaceConfig) (*driver.MLWorkspace, error) {
	return cast[*driver.MLWorkspace](a.do(ctx, "CreateMLWorkspace", cfg, func() (any, error) {
		return a.drv.CreateMLWorkspace(ctx, cfg)
	}))
}

func (a *AzureAI) GetMLWorkspace(ctx context.Context, rg, name string) (*driver.MLWorkspace, error) {
	return cast[*driver.MLWorkspace](a.do(ctx, "GetMLWorkspace", name, func() (any, error) {
		return a.drv.GetMLWorkspace(ctx, rg, name)
	}))
}

func (a *AzureAI) DeleteMLWorkspace(ctx context.Context, rg, name string) error {
	return a.act(ctx, "DeleteMLWorkspace", name, func() error { return a.drv.DeleteMLWorkspace(ctx, rg, name) })
}

func (a *AzureAI) UpdateMLWorkspaceTags(ctx context.Context, rg, name string, tags map[string]string) (*driver.MLWorkspace, error) {
	return cast[*driver.MLWorkspace](a.do(ctx, "UpdateMLWorkspaceTags", name, func() (any, error) {
		return a.drv.UpdateMLWorkspaceTags(ctx, rg, name, tags)
	}))
}

func (a *AzureAI) ListMLWorkspacesByResourceGroup(ctx context.Context, rg string) ([]driver.MLWorkspace, error) {
	return cast[[]driver.MLWorkspace](a.do(ctx, "ListMLWorkspacesByResourceGroup", rg, func() (any, error) {
		return a.drv.ListMLWorkspacesByResourceGroup(ctx, rg)
	}))
}

func (a *AzureAI) ListMLWorkspaces(ctx context.Context) ([]driver.MLWorkspace, error) {
	return cast[[]driver.MLWorkspace](a.do(ctx, "ListMLWorkspaces", nil, func() (any, error) { return a.drv.ListMLWorkspaces(ctx) }))
}

//nolint:gocritic // cfg matches the driver signature; forwarded unchanged.
func (a *AzureAI) CreateCompute(ctx context.Context, cfg driver.ComputeConfig) (*driver.Compute, error) {
	return cast[*driver.Compute](a.do(ctx, "CreateCompute", cfg, func() (any, error) { return a.drv.CreateCompute(ctx, cfg) }))
}

func (a *AzureAI) GetCompute(ctx context.Context, rg, ws, name string) (*driver.Compute, error) {
	return cast[*driver.Compute](a.do(ctx, "GetCompute", name, func() (any, error) { return a.drv.GetCompute(ctx, rg, ws, name) }))
}

func (a *AzureAI) DeleteCompute(ctx context.Context, rg, ws, name string) error {
	return a.act(ctx, "DeleteCompute", name, func() error { return a.drv.DeleteCompute(ctx, rg, ws, name) })
}

func (a *AzureAI) ListComputes(ctx context.Context, rg, ws string) ([]driver.Compute, error) {
	return cast[[]driver.Compute](a.do(ctx, "ListComputes", ws, func() (any, error) { return a.drv.ListComputes(ctx, rg, ws) }))
}

func (a *AzureAI) StartCompute(ctx context.Context, rg, ws, name string) error {
	return a.act(ctx, "StartCompute", name, func() error { return a.drv.StartCompute(ctx, rg, ws, name) })
}

func (a *AzureAI) StopCompute(ctx context.Context, rg, ws, name string) error {
	return a.act(ctx, "StopCompute", name, func() error { return a.drv.StopCompute(ctx, rg, ws, name) })
}

func (a *AzureAI) RestartCompute(ctx context.Context, rg, ws, name string) error {
	return a.act(ctx, "RestartCompute", name, func() error { return a.drv.RestartCompute(ctx, rg, ws, name) })
}

//nolint:gocritic // cfg matches the driver signature; forwarded unchanged.
func (a *AzureAI) CreateEndpoint(ctx context.Context, cfg driver.EndpointConfig) (*driver.Endpoint, error) {
	return cast[*driver.Endpoint](a.do(ctx, "CreateEndpoint", cfg, func() (any, error) { return a.drv.CreateEndpoint(ctx, cfg) }))
}

func (a *AzureAI) GetEndpoint(ctx context.Context, rg, ws, kind, name string) (*driver.Endpoint, error) {
	return cast[*driver.Endpoint](a.do(ctx, "GetEndpoint", name, func() (any, error) {
		return a.drv.GetEndpoint(ctx, rg, ws, kind, name)
	}))
}

func (a *AzureAI) DeleteEndpoint(ctx context.Context, rg, ws, kind, name string) error {
	return a.act(ctx, "DeleteEndpoint", name, func() error { return a.drv.DeleteEndpoint(ctx, rg, ws, kind, name) })
}

func (a *AzureAI) ListEndpoints(ctx context.Context, rg, ws, kind string) ([]driver.Endpoint, error) {
	return cast[[]driver.Endpoint](a.do(ctx, "ListEndpoints", ws, func() (any, error) {
		return a.drv.ListEndpoints(ctx, rg, ws, kind)
	}))
}

//nolint:gocritic // cfg matches the driver signature; forwarded unchanged.
func (a *AzureAI) CreateEndpointDeployment(ctx context.Context, cfg driver.EndpointDeploymentConfig) (*driver.EndpointDeployment, error) {
	return cast[*driver.EndpointDeployment](a.do(ctx, "CreateEndpointDeployment", cfg, func() (any, error) {
		return a.drv.CreateEndpointDeployment(ctx, cfg)
	}))
}

func (a *AzureAI) GetEndpointDeployment(ctx context.Context, rg, ws, kind, ep, name string) (*driver.EndpointDeployment, error) {
	return cast[*driver.EndpointDeployment](a.do(ctx, "GetEndpointDeployment", name, func() (any, error) {
		return a.drv.GetEndpointDeployment(ctx, rg, ws, kind, ep, name)
	}))
}

func (a *AzureAI) DeleteEndpointDeployment(ctx context.Context, rg, ws, kind, ep, name string) error {
	return a.act(ctx, "DeleteEndpointDeployment", name, func() error {
		return a.drv.DeleteEndpointDeployment(ctx, rg, ws, kind, ep, name)
	})
}

func (a *AzureAI) ListEndpointDeployments(ctx context.Context, rg, ws, kind, ep string) ([]driver.EndpointDeployment, error) {
	return cast[[]driver.EndpointDeployment](a.do(ctx, "ListEndpointDeployments", ep, func() (any, error) {
		return a.drv.ListEndpointDeployments(ctx, rg, ws, kind, ep)
	}))
}

//nolint:gocritic // cfg matches the driver signature; forwarded unchanged.
func (a *AzureAI) CreateJob(ctx context.Context, cfg driver.JobConfig) (*driver.Job, error) {
	return cast[*driver.Job](a.do(ctx, "CreateJob", cfg, func() (any, error) { return a.drv.CreateJob(ctx, cfg) }))
}

func (a *AzureAI) GetJob(ctx context.Context, rg, ws, name string) (*driver.Job, error) {
	return cast[*driver.Job](a.do(ctx, "GetJob", name, func() (any, error) { return a.drv.GetJob(ctx, rg, ws, name) }))
}

func (a *AzureAI) ListJobs(ctx context.Context, rg, ws string) ([]driver.Job, error) {
	return cast[[]driver.Job](a.do(ctx, "ListJobs", ws, func() (any, error) { return a.drv.ListJobs(ctx, rg, ws) }))
}

func (a *AzureAI) CancelJob(ctx context.Context, rg, ws, name string) error {
	return a.act(ctx, "CancelJob", name, func() error { return a.drv.CancelJob(ctx, rg, ws, name) })
}

//nolint:gocritic // cfg matches the driver signature; forwarded unchanged.
func (a *AzureAI) CreateAsset(ctx context.Context, cfg driver.AssetConfig) (*driver.Asset, error) {
	return cast[*driver.Asset](a.do(ctx, "CreateAsset", cfg, func() (any, error) { return a.drv.CreateAsset(ctx, cfg) }))
}

func (a *AzureAI) GetAsset(ctx context.Context, rg, ws, assetType, name, version string) (*driver.Asset, error) {
	return cast[*driver.Asset](a.do(ctx, "GetAsset", name, func() (any, error) {
		return a.drv.GetAsset(ctx, rg, ws, assetType, name, version)
	}))
}

func (a *AzureAI) DeleteAsset(ctx context.Context, rg, ws, assetType, name, version string) error {
	return a.act(ctx, "DeleteAsset", name, func() error { return a.drv.DeleteAsset(ctx, rg, ws, assetType, name, version) })
}

func (a *AzureAI) ListAssetVersions(ctx context.Context, rg, ws, assetType, name string) ([]driver.Asset, error) {
	return cast[[]driver.Asset](a.do(ctx, "ListAssetVersions", name, func() (any, error) {
		return a.drv.ListAssetVersions(ctx, rg, ws, assetType, name)
	}))
}

func (a *AzureAI) ListAssetContainers(ctx context.Context, rg, ws, assetType string) ([]driver.Asset, error) {
	return cast[[]driver.Asset](a.do(ctx, "ListAssetContainers", assetType, func() (any, error) {
		return a.drv.ListAssetContainers(ctx, rg, ws, assetType)
	}))
}

//nolint:gocritic // cfg matches the driver signature; forwarded unchanged.
func (a *AzureAI) CreateDatastore(ctx context.Context, cfg driver.DatastoreConfig) (*driver.Datastore, error) {
	return cast[*driver.Datastore](a.do(ctx, "CreateDatastore", cfg, func() (any, error) { return a.drv.CreateDatastore(ctx, cfg) }))
}

func (a *AzureAI) GetDatastore(ctx context.Context, rg, ws, name string) (*driver.Datastore, error) {
	return cast[*driver.Datastore](a.do(ctx, "GetDatastore", name, func() (any, error) {
		return a.drv.GetDatastore(ctx, rg, ws, name)
	}))
}

func (a *AzureAI) DeleteDatastore(ctx context.Context, rg, ws, name string) error {
	return a.act(ctx, "DeleteDatastore", name, func() error { return a.drv.DeleteDatastore(ctx, rg, ws, name) })
}

func (a *AzureAI) ListDatastores(ctx context.Context, rg, ws string) ([]driver.Datastore, error) {
	return cast[[]driver.Datastore](a.do(ctx, "ListDatastores", ws, func() (any, error) { return a.drv.ListDatastores(ctx, rg, ws) }))
}

//nolint:gocritic // cfg matches the driver signature; forwarded unchanged.
func (a *AzureAI) CreateConnection(ctx context.Context, cfg driver.ConnectionConfig) (*driver.Connection, error) {
	return cast[*driver.Connection](a.do(ctx, "CreateConnection", cfg, func() (any, error) { return a.drv.CreateConnection(ctx, cfg) }))
}

func (a *AzureAI) GetConnection(ctx context.Context, rg, ws, name string) (*driver.Connection, error) {
	return cast[*driver.Connection](a.do(ctx, "GetConnection", name, func() (any, error) {
		return a.drv.GetConnection(ctx, rg, ws, name)
	}))
}

func (a *AzureAI) DeleteConnection(ctx context.Context, rg, ws, name string) error {
	return a.act(ctx, "DeleteConnection", name, func() error { return a.drv.DeleteConnection(ctx, rg, ws, name) })
}

func (a *AzureAI) ListConnections(ctx context.Context, rg, ws string) ([]driver.Connection, error) {
	return cast[[]driver.Connection](a.do(ctx, "ListConnections", ws, func() (any, error) {
		return a.drv.ListConnections(ctx, rg, ws)
	}))
}

//nolint:gocritic // cfg matches the driver signature; forwarded unchanged.
func (a *AzureAI) CreateMLSchedule(ctx context.Context, cfg driver.MLScheduleConfig) (*driver.MLSchedule, error) {
	return cast[*driver.MLSchedule](a.do(ctx, "CreateMLSchedule", cfg, func() (any, error) { return a.drv.CreateMLSchedule(ctx, cfg) }))
}

func (a *AzureAI) GetMLSchedule(ctx context.Context, rg, ws, name string) (*driver.MLSchedule, error) {
	return cast[*driver.MLSchedule](a.do(ctx, "GetMLSchedule", name, func() (any, error) {
		return a.drv.GetMLSchedule(ctx, rg, ws, name)
	}))
}

func (a *AzureAI) DeleteMLSchedule(ctx context.Context, rg, ws, name string) error {
	return a.act(ctx, "DeleteMLSchedule", name, func() error { return a.drv.DeleteMLSchedule(ctx, rg, ws, name) })
}

func (a *AzureAI) ListMLSchedules(ctx context.Context, rg, ws string) ([]driver.MLSchedule, error) {
	return cast[[]driver.MLSchedule](a.do(ctx, "ListMLSchedules", ws, func() (any, error) {
		return a.drv.ListMLSchedules(ctx, rg, ws)
	}))
}

func (a *AzureAI) CreateRegistry(ctx context.Context, cfg driver.RegistryConfig) (*driver.Registry, error) {
	return cast[*driver.Registry](a.do(ctx, "CreateRegistry", cfg, func() (any, error) { return a.drv.CreateRegistry(ctx, cfg) }))
}

func (a *AzureAI) GetRegistry(ctx context.Context, rg, name string) (*driver.Registry, error) {
	return cast[*driver.Registry](a.do(ctx, "GetRegistry", name, func() (any, error) { return a.drv.GetRegistry(ctx, rg, name) }))
}

func (a *AzureAI) DeleteRegistry(ctx context.Context, rg, name string) error {
	return a.act(ctx, "DeleteRegistry", name, func() error { return a.drv.DeleteRegistry(ctx, rg, name) })
}

func (a *AzureAI) ListRegistries(ctx context.Context, rg string) ([]driver.Registry, error) {
	return cast[[]driver.Registry](a.do(ctx, "ListRegistries", rg, func() (any, error) { return a.drv.ListRegistries(ctx, rg) }))
}
