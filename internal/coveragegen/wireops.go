package main

import "sort"

// nativeWireOperations supplies the operation surface for provider-native
// services whose handler is wire-only — it has no backing provider mock package
// under providers/<prov>/<pkg>, so nativeOperations() (which reads a mock's
// method set) finds nothing and would otherwise leave the service at "0
// operations". These handlers dispatch requests directly on the wire shape
// (AWS query Action / X-Amz-Target, Azure ARM method+path, GCP/OCI REST
// routing) rather than through a Go interface, so there is no single method set
// to introspect uniformly across the four protocols.
//
// Each list mirrors exactly the operations the named handler serves in
// server/<prov>/<pkg>; keep it in sync when a handler gains or drops an
// operation. TestProviderNativeServicesHaveOperations guards that every
// wire-only native service is covered here (so a new one cannot silently ship
// as "0 operations"), and TestNativeWireOperationsAreRegistered guards that
// every key still names a registered handler (so a removed handler cannot leave
// a stale entry).
//
// Keyed by "<provider>/<handler-package>".
var nativeWireOperations = map[string][]string{ //nolint:gochecknoglobals // generator config, mirrors providerOrder
	// AWS — query / JSON-RPC handlers, dispatched on Action / X-Amz-Target.
	"aws/sts": {
		"AssumeRole", "AssumeRoleWithSAML", "AssumeRoleWithWebIdentity",
		"DecodeAuthorizationMessage", "GetAccessKeyInfo", "GetCallerIdentity",
		"GetFederationToken", "GetSessionToken",
	},
	"aws/resourcegroupstaggingapi": {
		"GetResources", "GetTagKeys", "GetTagValues", "TagResources", "UntagResources",
	},
	"aws/resourceexplorer2": {
		"CreateIndex", "CreateView", "DeleteView", "GetDefaultView", "GetIndex",
		"GetView", "ListIndexes", "ListResources", "ListViews", "Search",
	},

	// Azure — ARM handlers, routed on HTTP method + resource path shape.
	"azure/disks": {
		"CreateOrUpdate", "Delete", "Get", "GrantAccess", "List", "ListByResourceGroup", "RevokeAccess",
	},
	"azure/snapshots": {
		"CreateOrUpdate", "Delete", "Get", "List", "ListByResourceGroup",
	},
	"azure/images": {
		"CreateOrUpdate", "Delete", "Get", "List", "ListByResourceGroup",
	},
	"azure/storageaccount": {
		"Create", "Delete", "GetProperties", "GetServiceProperties", "List",
		"ListByResourceGroup", "ListKeys", "RegenerateKey", "SetServiceProperties", "Update",
	},
	"azure/cosmosaccount": {
		"CreateOrUpdate", "Delete", "FailoverPriorityChange", "Get", "List",
		"ListByResourceGroup", "ListConnectionStrings", "ListKeys", "ListReadOnlyKeys", "RegenerateKey",
	},
	"azure/sshpublickeys": {
		"Create", "Delete", "GenerateKeyPair", "Get", "List", "ListByResourceGroup", "Update",
	},
	"azure/resourcegroups": {
		"CheckExistence", "CreateOrUpdate", "Delete", "ExportTemplate", "Get", "List", "Update",
	},
	"azure/subscriptions": {
		"Get", "List", "ListLocations",
	},
	"azure/tenants": {
		"List",
	},
	"azure/resourcegraph": {
		"Operations", "Resources", "ResourcesHistory",
	},
	"azure/queue": {
		"Create", "Delete", "DeleteMessage", "DequeueMessages", "EnqueueMessage", "List",
	},

	// GCP — REST handlers, routed on method + resource path / custom verb.
	"gcp/cloudasset": {
		"BatchGetAssetsHistory", "CreateFeed", "DeleteFeed", "ExportAssets", "GetFeed",
		"GetOperation", "ListAssets", "ListFeeds", "SearchAllIamPolicies", "SearchAllResources", "UpdateFeed",
	},
	"gcp/lro": {
		"GetOperation",
	},
	"gcp/servicenetworking": {
		"CreateConnection", "DeleteConnection", "ListConnections",
	},

	// OCI — REST work-request envelope.
	"oci/workrequest": {
		"GetWorkRequest", "ListWorkRequestErrors", "ListWorkRequestLogs", "ListWorkRequests",
	},
}

// wireServedOperations returns the declared operations for the wire-only native
// handler server/<prov>/<pkg>, sorted, or nil when the handler is not
// supplemented here.
func wireServedOperations(prov, pkg string) []Operation {
	names, ok := nativeWireOperations[prov+"/"+pkg]
	if !ok {
		return nil
	}

	ops := make([]Operation, 0, len(names))
	for _, name := range names {
		ops = append(ops, Operation{Name: name})
	}

	sort.Slice(ops, func(i, j int) bool { return ops[i].Name < ops[j].Name })

	return ops
}
