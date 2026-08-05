package databricks

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/databricks/driver"
)

// This file wraps the extended Microsoft.Databricks ARM surface (issue #209)
// with the same cross-cutting pipeline (do) as the workspace operations.

// --- Access connectors ---

// CreateOrUpdateAccessConnector creates or updates an access connector.
func (b *Databricks) CreateOrUpdateAccessConnector(
	ctx context.Context, cfg driver.AccessConnectorConfig,
) (*driver.AccessConnector, error) {
	out, err := b.do(ctx, "CreateOrUpdateAccessConnector", cfg, func() (any, error) {
		return b.driver.CreateOrUpdateAccessConnector(ctx, cfg)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.AccessConnector), nil
}

// GetAccessConnector retrieves an access connector.
func (b *Databricks) GetAccessConnector(ctx context.Context, resourceGroup, name string) (*driver.AccessConnector, error) {
	out, err := b.do(ctx, "GetAccessConnector", name, func() (any, error) {
		return b.driver.GetAccessConnector(ctx, resourceGroup, name)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.AccessConnector), nil
}

// UpdateAccessConnector applies a PATCH to an access connector.
func (b *Databricks) UpdateAccessConnector(
	ctx context.Context, resourceGroup, name string, tags map[string]string, identity *driver.ManagedIdentity,
) (*driver.AccessConnector, error) {
	out, err := b.do(ctx, "UpdateAccessConnector", name, func() (any, error) {
		return b.driver.UpdateAccessConnector(ctx, resourceGroup, name, tags, identity)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.AccessConnector), nil
}

// DeleteAccessConnector deletes an access connector.
func (b *Databricks) DeleteAccessConnector(ctx context.Context, resourceGroup, name string) error {
	_, err := b.do(ctx, "DeleteAccessConnector", name, func() (any, error) {
		return nil, b.driver.DeleteAccessConnector(ctx, resourceGroup, name)
	})

	return err
}

// ListAccessConnectorsByResourceGroup lists access connectors in a resource group.
func (b *Databricks) ListAccessConnectorsByResourceGroup(
	ctx context.Context, resourceGroup string,
) ([]driver.AccessConnector, error) {
	out, err := b.do(ctx, "ListAccessConnectorsByResourceGroup", resourceGroup, func() (any, error) {
		return b.driver.ListAccessConnectorsByResourceGroup(ctx, resourceGroup)
	})
	if err != nil {
		return nil, err
	}

	return out.([]driver.AccessConnector), nil
}

// ListAccessConnectors lists all access connectors in the subscription.
func (b *Databricks) ListAccessConnectors(ctx context.Context) ([]driver.AccessConnector, error) {
	out, err := b.do(ctx, "ListAccessConnectors", nil, func() (any, error) {
		return b.driver.ListAccessConnectors(ctx)
	})
	if err != nil {
		return nil, err
	}

	return out.([]driver.AccessConnector), nil
}

// --- Private endpoint connections ---

// PutPrivateEndpointConnection creates or updates a workspace PEC.
func (b *Databricks) PutPrivateEndpointConnection(
	ctx context.Context, resourceGroup, workspace, name, status, description string,
) (*driver.PrivateEndpointConnection, error) {
	out, err := b.do(ctx, "PutPrivateEndpointConnection", name, func() (any, error) {
		return b.driver.PutPrivateEndpointConnection(ctx, resourceGroup, workspace, name, status, description)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.PrivateEndpointConnection), nil
}

// GetPrivateEndpointConnection retrieves a workspace PEC.
func (b *Databricks) GetPrivateEndpointConnection(
	ctx context.Context, resourceGroup, workspace, name string,
) (*driver.PrivateEndpointConnection, error) {
	out, err := b.do(ctx, "GetPrivateEndpointConnection", name, func() (any, error) {
		return b.driver.GetPrivateEndpointConnection(ctx, resourceGroup, workspace, name)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.PrivateEndpointConnection), nil
}

// DeletePrivateEndpointConnection deletes a workspace PEC.
func (b *Databricks) DeletePrivateEndpointConnection(ctx context.Context, resourceGroup, workspace, name string) error {
	_, err := b.do(ctx, "DeletePrivateEndpointConnection", name, func() (any, error) {
		return nil, b.driver.DeletePrivateEndpointConnection(ctx, resourceGroup, workspace, name)
	})

	return err
}

// ListPrivateEndpointConnections lists a workspace's PECs.
func (b *Databricks) ListPrivateEndpointConnections(
	ctx context.Context, resourceGroup, workspace string,
) ([]driver.PrivateEndpointConnection, error) {
	out, err := b.do(ctx, "ListPrivateEndpointConnections", workspace, func() (any, error) {
		return b.driver.ListPrivateEndpointConnections(ctx, resourceGroup, workspace)
	})
	if err != nil {
		return nil, err
	}

	return out.([]driver.PrivateEndpointConnection), nil
}

// --- Private link resources ---

// GetPrivateLinkResource retrieves a workspace private-link resource by group id.
func (b *Databricks) GetPrivateLinkResource(
	ctx context.Context, resourceGroup, workspace, groupID string,
) (*driver.GroupIDInformation, error) {
	out, err := b.do(ctx, "GetPrivateLinkResource", groupID, func() (any, error) {
		return b.driver.GetPrivateLinkResource(ctx, resourceGroup, workspace, groupID)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.GroupIDInformation), nil
}

// ListPrivateLinkResources lists a workspace's private-link resources.
func (b *Databricks) ListPrivateLinkResources(
	ctx context.Context, resourceGroup, workspace string,
) ([]driver.GroupIDInformation, error) {
	out, err := b.do(ctx, "ListPrivateLinkResources", workspace, func() (any, error) {
		return b.driver.ListPrivateLinkResources(ctx, resourceGroup, workspace)
	})
	if err != nil {
		return nil, err
	}

	return out.([]driver.GroupIDInformation), nil
}

// --- Virtual network peerings ---

// CreateOrUpdateVNetPeering creates or updates a workspace VNet peering.
func (b *Databricks) CreateOrUpdateVNetPeering(
	ctx context.Context, resourceGroup, workspace, name string, cfg driver.VirtualNetworkPeeringConfig,
) (*driver.VirtualNetworkPeering, error) {
	out, err := b.do(ctx, "CreateOrUpdateVNetPeering", name, func() (any, error) {
		return b.driver.CreateOrUpdateVNetPeering(ctx, resourceGroup, workspace, name, cfg)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.VirtualNetworkPeering), nil
}

// GetVNetPeering retrieves a workspace VNet peering.
func (b *Databricks) GetVNetPeering(
	ctx context.Context, resourceGroup, workspace, name string,
) (*driver.VirtualNetworkPeering, error) {
	out, err := b.do(ctx, "GetVNetPeering", name, func() (any, error) {
		return b.driver.GetVNetPeering(ctx, resourceGroup, workspace, name)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.VirtualNetworkPeering), nil
}

// DeleteVNetPeering deletes a workspace VNet peering.
func (b *Databricks) DeleteVNetPeering(ctx context.Context, resourceGroup, workspace, name string) error {
	_, err := b.do(ctx, "DeleteVNetPeering", name, func() (any, error) {
		return nil, b.driver.DeleteVNetPeering(ctx, resourceGroup, workspace, name)
	})

	return err
}

// ListVNetPeerings lists a workspace's VNet peerings.
func (b *Databricks) ListVNetPeerings(
	ctx context.Context, resourceGroup, workspace string,
) ([]driver.VirtualNetworkPeering, error) {
	out, err := b.do(ctx, "ListVNetPeerings", workspace, func() (any, error) {
		return b.driver.ListVNetPeerings(ctx, resourceGroup, workspace)
	})
	if err != nil {
		return nil, err
	}

	return out.([]driver.VirtualNetworkPeering), nil
}

// --- Outbound network dependencies & operations ---

// ListOutboundNetworkDependencies lists a workspace's outbound network dependencies.
func (b *Databricks) ListOutboundNetworkDependencies(
	ctx context.Context, resourceGroup, workspace string,
) ([]driver.OutboundEndpoint, error) {
	out, err := b.do(ctx, "ListOutboundNetworkDependencies", workspace, func() (any, error) {
		return b.driver.ListOutboundNetworkDependencies(ctx, resourceGroup, workspace)
	})
	if err != nil {
		return nil, err
	}

	return out.([]driver.OutboundEndpoint), nil
}

// ListOperations lists the Microsoft.Databricks provider operations.
func (b *Databricks) ListOperations(ctx context.Context) ([]driver.Operation, error) {
	out, err := b.do(ctx, "ListOperations", nil, func() (any, error) {
		return b.driver.ListOperations(ctx)
	})
	if err != nil {
		return nil, err
	}

	return out.([]driver.Operation), nil
}
