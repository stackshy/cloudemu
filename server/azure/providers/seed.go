package providers

// defaultProviders returns the seed catalog of resource providers the
// emulator serves. The resourceTypes lists are representative (the types
// cloudemu actually backs for each namespace), not the full Azure surface.
// Initial registrationState mirrors the real defaults: the always-on control
// namespaces are Registered, the rest start NotRegistered so a Register call
// meaningfully flips them.
func defaultProviders() []providerEntry {
	regional := []string{"eastus", "westus", "westeurope"}
	global := []string{"global"}

	return []providerEntry{
		newProvider("Microsoft.Resources", stateRegistered, global,
			"resourceGroups", "subscriptions", "tenants", "providers"),
		newProvider("Microsoft.Authorization", stateRegistered, global,
			"roleDefinitions", "roleAssignments"),
		newProvider("Microsoft.Insights", stateRegistered, regional,
			"components", "metricAlerts", "diagnosticSettings"),
		newProvider("Microsoft.Compute", stateRegistered, regional,
			"virtualMachines", "disks", "snapshots", "images", "sshPublicKeys"),
		newProvider("Microsoft.Network", stateRegistered, regional,
			"virtualNetworks", "networkSecurityGroups", "networkInterfaces",
			"publicIPAddresses", "loadBalancers", "dnsZones", "applicationSecurityGroups"),
		newProvider("Microsoft.Storage", stateRegistered, regional, "storageAccounts"),
		newProvider("Microsoft.Sql", stateNotRegistered, regional, "servers", "servers/databases"),
		newProvider("Microsoft.KeyVault", stateNotRegistered, regional, "vaults"),
		newProvider("Microsoft.DocumentDB", stateNotRegistered, regional,
			"databaseAccounts", "cassandraClusters"),
		newProvider("Microsoft.ServiceBus", stateNotRegistered, regional, "namespaces"),
		newProvider("Microsoft.ManagedIdentity", stateNotRegistered, regional, "userAssignedIdentities"),
		newProvider("Microsoft.ContainerService", stateNotRegistered, regional, "managedClusters"),
		newProvider("Microsoft.ContainerRegistry", stateNotRegistered, regional, "registries"),
		newProvider("Microsoft.ContainerInstance", stateNotRegistered, regional, "containerGroups"),
		newProvider("Microsoft.Cache", stateNotRegistered, regional, "redis"),
		newProvider("Microsoft.EventGrid", stateNotRegistered, regional, "topics"),
		newProvider("Microsoft.OperationalInsights", stateNotRegistered, regional, "workspaces"),
		newProvider("Microsoft.Web", stateNotRegistered, regional, "sites"),
		newProvider("Microsoft.NotificationHubs", stateNotRegistered, regional, "namespaces"),
		newProvider("Microsoft.DBforPostgreSQL", stateNotRegistered, regional,
			"flexibleServers", "serverGroupsv2"),
		newProvider("Microsoft.DBforMySQL", stateNotRegistered, regional, "flexibleServers"),
		newProvider("Microsoft.Databricks", stateNotRegistered, regional, "workspaces"),
	}
}

// newProvider builds a providerEntry whose resource types all share locs.
func newProvider(namespace, state string, locs []string, types ...string) providerEntry {
	rts := make([]resourceType, 0, len(types))
	for _, t := range types {
		rts = append(rts, resourceType{name: t, locations: locs})
	}

	return providerEntry{namespace: namespace, resourceTypes: rts, state: state}
}
