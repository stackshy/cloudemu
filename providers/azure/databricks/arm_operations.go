package databricks

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/databricks/driver"
)

// ListOperations returns the Microsoft.Databricks provider operations list. It
// is a static catalog of the RBAC operations the provider exposes, mirroring
// what the real armdatabricks OperationsClient returns.
func (*Mock) ListOperations(_ context.Context) ([]driver.Operation, error) {
	return append([]driver.Operation(nil), databricksOperations...), nil
}

// databricksOperations is the provider operation catalog. Kept deliberately
// representative (the real list is longer); each entry round-trips the display
// metadata the SDK surfaces.
//
//nolint:gochecknoglobals // immutable static catalog
var databricksOperations = []driver.Operation{
	op("workspaces/read", "workspaces", "Read", "Get a workspace"),
	op("workspaces/write", "workspaces", "Write", "Create or update a workspace"),
	op("workspaces/delete", "workspaces", "Delete", "Delete a workspace"),
	op("accessConnectors/read", "accessConnectors", "Read", "Get an access connector"),
	op("accessConnectors/write", "accessConnectors", "Write", "Create or update an access connector"),
	op("accessConnectors/delete", "accessConnectors", "Delete", "Delete an access connector"),
	op("workspaces/privateEndpointConnections/read", "privateEndpointConnections", "Read", "Get a private endpoint connection"),
	op("workspaces/privateEndpointConnections/write", "privateEndpointConnections",
		"Write", "Approve or reject a private endpoint connection"),
	op("workspaces/privateEndpointConnections/delete", "privateEndpointConnections", "Delete", "Delete a private endpoint connection"),
	op("workspaces/privateLinkResources/read", "privateLinkResources", "Read", "Get workspace private link resources"),
	op("workspaces/virtualNetworkPeerings/read", "virtualNetworkPeerings", "Read", "Get a virtual network peering"),
	op("workspaces/virtualNetworkPeerings/write", "virtualNetworkPeerings", "Write", "Create or update a virtual network peering"),
	op("workspaces/virtualNetworkPeerings/delete", "virtualNetworkPeerings", "Delete", "Delete a virtual network peering"),
	op("workspaces/outboundNetworkDependenciesEndpoints/read", "outboundNetworkDependenciesEndpoints",
		"Read", "List workspace outbound network dependencies"),
	op("operations/read", "operations", "Read", "List Microsoft.Databricks operations"),
}

func op(name, resource, verb, description string) driver.Operation {
	return driver.Operation{
		Name:        providerNamespace + "/" + name,
		Provider:    providerNamespace,
		Resource:    resource,
		Operation:   verb,
		Description: description,
	}
}
