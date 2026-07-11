package azuresearch

import (
	"context"

	"github.com/stackshy/cloudemu/azuresearch/driver"
)

//nolint:gocritic // cfg matches the driver signature; forwarded unchanged.
func (a *AzureSearch) CreateService(ctx context.Context, cfg driver.ServiceConfig) (*driver.Service, error) {
	return cast[*driver.Service](a.do(ctx, "CreateService", cfg, func() (any, error) { return a.drv.CreateService(ctx, cfg) }))
}

func (a *AzureSearch) GetService(ctx context.Context, rg, name string) (*driver.Service, error) {
	return cast[*driver.Service](a.do(ctx, "GetService", name, func() (any, error) { return a.drv.GetService(ctx, rg, name) }))
}

func (a *AzureSearch) DeleteService(ctx context.Context, rg, name string) error {
	return a.act(ctx, "DeleteService", name, func() error { return a.drv.DeleteService(ctx, rg, name) })
}

func (a *AzureSearch) UpdateService(
	ctx context.Context, rg, name string, replicas, partitions int, tags map[string]string,
) (*driver.Service, error) {
	return cast[*driver.Service](a.do(ctx, "UpdateService", name, func() (any, error) {
		return a.drv.UpdateService(ctx, rg, name, replicas, partitions, tags)
	}))
}

func (a *AzureSearch) ListServicesByResourceGroup(ctx context.Context, rg string) ([]driver.Service, error) {
	return cast[[]driver.Service](a.do(ctx, "ListServicesByResourceGroup", rg, func() (any, error) {
		return a.drv.ListServicesByResourceGroup(ctx, rg)
	}))
}

func (a *AzureSearch) ListServices(ctx context.Context) ([]driver.Service, error) {
	return cast[[]driver.Service](a.do(ctx, "ListServices", nil, func() (any, error) { return a.drv.ListServices(ctx) }))
}

func (a *AzureSearch) ListAdminKeys(ctx context.Context, rg, name string) (*driver.AdminKeys, error) {
	return cast[*driver.AdminKeys](a.do(ctx, "ListAdminKeys", name, func() (any, error) { return a.drv.ListAdminKeys(ctx, rg, name) }))
}

func (a *AzureSearch) RegenerateAdminKey(ctx context.Context, rg, name, which string) (*driver.AdminKeys, error) {
	return cast[*driver.AdminKeys](a.do(ctx, "RegenerateAdminKey", name, func() (any, error) {
		return a.drv.RegenerateAdminKey(ctx, rg, name, which)
	}))
}

func (a *AzureSearch) ListQueryKeys(ctx context.Context, rg, name string) ([]driver.QueryKey, error) {
	return cast[[]driver.QueryKey](a.do(ctx, "ListQueryKeys", name, func() (any, error) { return a.drv.ListQueryKeys(ctx, rg, name) }))
}

func (a *AzureSearch) CreateQueryKey(ctx context.Context, rg, name, keyName string) (*driver.QueryKey, error) {
	return cast[*driver.QueryKey](a.do(ctx, "CreateQueryKey", name, func() (any, error) {
		return a.drv.CreateQueryKey(ctx, rg, name, keyName)
	}))
}

func (a *AzureSearch) DeleteQueryKey(ctx context.Context, rg, name, key string) error {
	return a.act(ctx, "DeleteQueryKey", name, func() error { return a.drv.DeleteQueryKey(ctx, rg, name, key) })
}

func (a *AzureSearch) PutSharedPrivateLink(
	ctx context.Context, rg, name, linkName, groupID, privateLinkID string,
) (*driver.SharedPrivateLink, error) {
	return cast[*driver.SharedPrivateLink](a.do(ctx, "PutSharedPrivateLink", linkName, func() (any, error) {
		return a.drv.PutSharedPrivateLink(ctx, rg, name, linkName, groupID, privateLinkID)
	}))
}

func (a *AzureSearch) GetSharedPrivateLink(ctx context.Context, rg, name, linkName string) (*driver.SharedPrivateLink, error) {
	return cast[*driver.SharedPrivateLink](a.do(ctx, "GetSharedPrivateLink", linkName, func() (any, error) {
		return a.drv.GetSharedPrivateLink(ctx, rg, name, linkName)
	}))
}

func (a *AzureSearch) DeleteSharedPrivateLink(ctx context.Context, rg, name, linkName string) error {
	return a.act(ctx, "DeleteSharedPrivateLink", linkName, func() error {
		return a.drv.DeleteSharedPrivateLink(ctx, rg, name, linkName)
	})
}

func (a *AzureSearch) ListSharedPrivateLinks(ctx context.Context, rg, name string) ([]driver.SharedPrivateLink, error) {
	return cast[[]driver.SharedPrivateLink](a.do(ctx, "ListSharedPrivateLinks", name, func() (any, error) {
		return a.drv.ListSharedPrivateLinks(ctx, rg, name)
	}))
}

func (a *AzureSearch) PutPrivateEndpointConnection(
	ctx context.Context, rg, name, connName, status string,
) (*driver.PrivateEndpointConnection, error) {
	return cast[*driver.PrivateEndpointConnection](a.do(ctx, "PutPrivateEndpointConnection", connName, func() (any, error) {
		return a.drv.PutPrivateEndpointConnection(ctx, rg, name, connName, status)
	}))
}

func (a *AzureSearch) GetPrivateEndpointConnection(
	ctx context.Context, rg, name, connName string,
) (*driver.PrivateEndpointConnection, error) {
	return cast[*driver.PrivateEndpointConnection](a.do(ctx, "GetPrivateEndpointConnection", connName, func() (any, error) {
		return a.drv.GetPrivateEndpointConnection(ctx, rg, name, connName)
	}))
}

func (a *AzureSearch) DeletePrivateEndpointConnection(ctx context.Context, rg, name, connName string) error {
	return a.act(ctx, "DeletePrivateEndpointConnection", connName, func() error {
		return a.drv.DeletePrivateEndpointConnection(ctx, rg, name, connName)
	})
}

func (a *AzureSearch) ListPrivateEndpointConnections(
	ctx context.Context, rg, name string,
) ([]driver.PrivateEndpointConnection, error) {
	return cast[[]driver.PrivateEndpointConnection](a.do(ctx, "ListPrivateEndpointConnections", name, func() (any, error) {
		return a.drv.ListPrivateEndpointConnections(ctx, rg, name)
	}))
}
