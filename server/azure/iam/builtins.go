package iam

import "strings"

const (
	// builtInCreatedOn is a fixed timestamp for built-in roles. Real Azure
	// reports the (very old) date the role shipped; the exact value does not
	// matter to consumers, but it must be stable so GETs are deterministic.
	builtInCreatedOn = "2017-12-05T00:00:00.0000000Z"

	// builtInAssignableScope is the root scope built-in roles are assignable
	// at: real built-ins carry assignableScopes ["/"], meaning they can be
	// assigned at any scope in the hierarchy.
	builtInAssignableScope = "/"

	// Action prefixes shared by several built-in roles.
	actBlobContainers = "Microsoft.Storage/storageAccounts/blobServices/containers/"
	actBlobDelegation = "Microsoft.Storage/storageAccounts/blobServices/generateUserDelegationKey/action"
	actQueues         = "Microsoft.Storage/storageAccounts/queueServices/queues/"
	actKVKeys         = "Microsoft.KeyVault/vaults/keys/"
	actSupport        = "Microsoft.Support/*"
	actAuthRead       = "Microsoft.Authorization/*/read"
	actAlertRules     = "Microsoft.Insights/alertRules/*"
	actDeployments    = "Microsoft.Resources/deployments/*"
	actRGRead         = "Microsoft.Resources/subscriptions/resourceGroups/read"
	actHealthRead     = "Microsoft.ResourceHealth/availabilityStatuses/read"
	actSBAll          = "Microsoft.ServiceBus/*"
	actEHAll          = "Microsoft.EventHub/*"
	actSBReads        = "Microsoft.ServiceBus/*/queues/read|Microsoft.ServiceBus/*/topics/read|" +
		"Microsoft.ServiceBus/*/topics/subscriptions/read"
)

// builtInRole is one entry of the built-in role catalog. Action lists are
// written as "|"-joined strings to keep the table readable.
type builtInRole struct {
	id, name, description            string
	actions, notActions, dataActions string
}

// kvManagementActions are the control-plane actions the Key Vault data roles
// (Administrator, Secrets Officer) carry in real Azure.
const kvManagementActions = actAuthRead + "|" + actAlertRules + "|" + actDeployments + "|" + actRGRead + "|" +
	actSupport + "|Microsoft.KeyVault/checkNameAvailability/read|Microsoft.KeyVault/deletedVaults/read|" +
	"Microsoft.KeyVault/locations/*/read|Microsoft.KeyVault/vaults/*/read|Microsoft.KeyVault/operations/read"

// builtInRoleCatalog lists the Azure built-in roles cloudemu seeds, with the
// fixed role-definition GUIDs and permissions Microsoft publishes in
// "Azure built-in roles" (learn.microsoft.com/azure/role-based-access-control/built-in-roles).
// The GUIDs are identical in every tenant, so IaC references them directly.
func builtInRoleCatalog() []builtInRole {
	return []builtInRole{
		{"8e3af657-a8ff-443c-a75c-2fe8c4bcb635", "Owner",
			"Grants full access to manage all resources, including the ability to assign roles in Azure RBAC.",
			"*", "", ""},
		{"b24988ac-6180-42a0-ab88-20f7382dd24c", "Contributor",
			"Grants full access to manage all resources, but does not allow you to assign roles in Azure RBAC.",
			"*", "Microsoft.Authorization/*/Delete|Microsoft.Authorization/*/Write|" +
				"Microsoft.Authorization/elevateAccess/Action|Microsoft.Blueprint/blueprintAssignments/write|" +
				"Microsoft.Blueprint/blueprintAssignments/delete", ""},
		{"acdd72a7-3385-48ef-bd42-f606fba81ae7", "Reader",
			"View all resources, but does not allow you to make any changes.",
			"*/read", "", ""},
		{"18d7d88d-d35e-4fb5-a5c3-7773c20a72d9", "User Access Administrator",
			"Lets you manage user access to Azure resources.",
			"*/read|Microsoft.Authorization/*|" + actSupport, "", ""},
		{"b7e6dc6d-f1e8-4753-8033-0f276bb0955b", "Storage Blob Data Owner",
			"Provides full access to Azure Storage blob containers and data, including assigning POSIX access control.",
			actBlobContainers + "*|" + actBlobDelegation, "", actBlobContainers + "blobs/*"},
		{"ba92f5b4-2d11-453d-a403-e96b0029c9fe", "Storage Blob Data Contributor",
			"Allows for read, write and delete access to Azure Storage blob containers and data.",
			actBlobContainers + "delete|" + actBlobContainers + "read|" + actBlobContainers + "write|" + actBlobDelegation,
			"", actBlobContainers + "blobs/delete|" + actBlobContainers + "blobs/read|" + actBlobContainers +
				"blobs/write|" + actBlobContainers + "blobs/move/action|" + actBlobContainers + "blobs/add/action"},
		{"2a2b9908-6ea1-4ae2-8e65-a410df84e7d1", "Storage Blob Data Reader",
			"Allows for read access to Azure Storage blob containers and data.",
			actBlobContainers + "read|" + actBlobDelegation, "", actBlobContainers + "blobs/read"},
		{"974c5e8b-45b9-4653-ba55-5f855dd0fb88", "Storage Queue Data Contributor",
			"Allows for read, write, and delete access to Azure Storage queues and queue messages.",
			actQueues + "delete|" + actQueues + "read|" + actQueues + "write", "",
			actQueues + "messages/delete|" + actQueues + "messages/read|" + actQueues + "messages/write|" +
				actQueues + "messages/process/action"},
		{"00482a5a-887f-4fb3-b363-3b7fe8e74483", "Key Vault Administrator",
			"Perform all data plane operations on a key vault and all objects in it, including certificates, keys, and secrets.",
			kvManagementActions, "", "Microsoft.KeyVault/vaults/*"},
		{"4633458b-17de-408a-b874-0445c86b69e6", "Key Vault Secrets User",
			"Read secret contents. Only works for key vaults that use the 'Azure role-based access control' permission model.",
			"", "", "Microsoft.KeyVault/vaults/secrets/getSecret/action|Microsoft.KeyVault/vaults/secrets/readMetadata/action"},
		{"b86a8fe4-44ce-4948-aee5-eccb2c155cd7", "Key Vault Secrets Officer",
			"Perform any action on the secrets of a key vault, except manage permissions.",
			kvManagementActions, "", "Microsoft.KeyVault/vaults/secrets/*"},
		{"12338af0-0e69-4776-bea7-57ae8d297424", "Key Vault Crypto User",
			"Perform cryptographic operations using keys.",
			"", "", actKVKeys + "read|" + actKVKeys + "update/action|" + actKVKeys + "backup/action|" +
				actKVKeys + "encrypt/action|" + actKVKeys + "decrypt/action|" + actKVKeys + "wrap/action|" +
				actKVKeys + "unwrap/action|" + actKVKeys + "sign/action|" + actKVKeys + "verify/action"},
		{"7f951dda-4ed3-4680-a7ca-43fe172d538d", "AcrPull",
			"acr pull", "Microsoft.ContainerRegistry/registries/pull/read", "", ""},
		{"8311e382-0749-4cb8-b61a-304f252e45ec", "AcrPush",
			"acr push",
			"Microsoft.ContainerRegistry/registries/pull/read|Microsoft.ContainerRegistry/registries/push/write", "", ""},
		{"4abbcc35-e782-43d8-92c5-2d3f1bd2253f", "Azure Kubernetes Service Cluster User Role",
			"List cluster user credential action.",
			"Microsoft.ContainerService/managedClusters/listClusterUserCredential/action|" +
				"Microsoft.ContainerService/managedClusters/read", "", ""},
		{"4d97b98b-1d4f-4787-a291-c67834d212e7", "Network Contributor",
			"Lets you manage networks, but not access to them.",
			actAuthRead + "|" + actAlertRules + "|Microsoft.Network/*|" + actHealthRead + "|" + actDeployments + "|" +
				actRGRead + "|" + actSupport, "", ""},
		{"3913510d-42f4-4e42-8a64-420c390055eb", "Monitoring Metrics Publisher",
			"Enables publishing metrics against Azure resources.",
			"Microsoft.Insights/Register/Action|" + actSupport + "|" + actRGRead, "",
			"Microsoft.Insights/Metrics/Write|Microsoft.Insights/Telemetry/Write"},
		{"090c5cfd-751d-490a-894a-3ce6f1109419", "Azure Service Bus Data Owner",
			"Allows for full access to Azure Service Bus resources.",
			actSBAll, "", actSBAll},
		{"69a216fc-b8fb-44d8-bc22-1f3c2cd27a39", "Azure Service Bus Data Sender",
			"Allows for send access to Azure Service Bus resources.",
			actSBReads, "", "Microsoft.ServiceBus/*/send/action"},
		{"4f6d3b9b-027b-4f4c-9142-0e5a2a2247e0", "Azure Service Bus Data Receiver",
			"Allows for receive access to Azure Service Bus resources.",
			actSBReads, "", "Microsoft.ServiceBus/*/receive/action"},
		{"f526a384-b230-433a-b45c-95f59c4a2dec", "Azure Event Hubs Data Owner",
			"Allows for full access to Azure Event Hubs resources.",
			actEHAll, "", actEHAll},
		{"2b629674-e913-4c01-ae53-ef4638d8f975", "Azure Event Hubs Data Sender",
			"Allows send access to Azure Event Hubs resources.",
			"Microsoft.EventHub/*/eventhubs/read", "", "Microsoft.EventHub/*/send/action"},
		{"a638d3c7-ab3a-418d-83e6-5f17a39d4fde", "Azure Event Hubs Data Receiver",
			"Allows receive access to Azure Event Hubs resources.",
			"Microsoft.EventHub/*/eventhubs/consumergroups/read", "", "Microsoft.EventHub/*/receive/action"},
		{"a97b65f3-24c7-4388-baec-2e87135dc908", "Cognitive Services User",
			"Lets you read and list keys of Cognitive Services.",
			"Microsoft.CognitiveServices/*/read|Microsoft.CognitiveServices/accounts/listkeys/action|" +
				"Microsoft.Insights/alertRules/read|Microsoft.Insights/diagnosticSettings/read|" +
				"Microsoft.Insights/logDefinitions/read|Microsoft.Insights/metricdefinitions/read|" +
				"Microsoft.Insights/metrics/read|" + actHealthRead + "|Microsoft.Resources/deployments/operations/read|" +
				"Microsoft.Resources/subscriptions/operationresults/read|Microsoft.Resources/subscriptions/read|" +
				actRGRead + "|" + actSupport, "", "Microsoft.CognitiveServices/*"},
	}
}

// builtInRoleDefinitions returns the seeded built-in role definitions keyed by
// their fixed GUID. It is a constructor (not a package global) so it satisfies
// the no-globals lint rule and so each Handler owns an independent copy.
func builtInRoleDefinitions() map[string]roleDefinitionProperties {
	catalog := builtInRoleCatalog()
	out := make(map[string]roleDefinitionProperties, len(catalog))

	for i := range catalog {
		role := &catalog[i]
		out[role.id] = roleDefinitionProperties{
			RoleName:    role.name,
			Description: role.description,
			Type:        "BuiltInRole",
			Permissions: []permission{{
				Actions:     splitActions(role.actions),
				NotActions:  splitActions(role.notActions),
				DataActions: splitActions(role.dataActions),
			}},
			AssignableScopes: []string{builtInAssignableScope},
			CreatedOn:        builtInCreatedOn,
			UpdatedOn:        builtInCreatedOn,
		}
	}

	return out
}

// splitActions turns a "|"-joined action list into a slice; "" yields nil so
// the JSON field is omitted, as real Azure returns empty lists.
func splitActions(joined string) []string {
	if joined == "" {
		return nil
	}

	return strings.Split(joined, "|")
}
