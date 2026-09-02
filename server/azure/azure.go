// Package azure assembles CloudEmu's Azure-compatible HTTP server.
//
// New takes a Drivers bundle and returns a *server.Server preloaded with the
// handler for each non-nil driver. Consumers that want a single service can
// skip this package and register the handler directly on their own
// server.Server.
package azure

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/server"
	"github.com/stackshy/cloudemu/v2/server/azure/acr"
	azureaiserver "github.com/stackshy/cloudemu/v2/server/azure/ai"
	aksserver "github.com/stackshy/cloudemu/v2/server/azure/aks"
	"github.com/stackshy/cloudemu/v2/server/azure/blobstorage"
	cachesrv "github.com/stackshy/cloudemu/v2/server/azure/cache"
	containerappssrv "github.com/stackshy/cloudemu/v2/server/azure/containerapps"
	containerinstancessrv "github.com/stackshy/cloudemu/v2/server/azure/containerinstances"
	"github.com/stackshy/cloudemu/v2/server/azure/cosmosaccount"
	"github.com/stackshy/cloudemu/v2/server/azure/cosmosdb"
	"github.com/stackshy/cloudemu/v2/server/azure/cosmospostgresql"
	"github.com/stackshy/cloudemu/v2/server/azure/costmanagement"
	"github.com/stackshy/cloudemu/v2/server/azure/databricks"
	"github.com/stackshy/cloudemu/v2/server/azure/databricks/dbfs"
	"github.com/stackshy/cloudemu/v2/server/azure/databricks/gitcredentials"
	"github.com/stackshy/cloudemu/v2/server/azure/databricks/hostmeta"
	"github.com/stackshy/cloudemu/v2/server/azure/databricks/pipelines"
	"github.com/stackshy/cloudemu/v2/server/azure/databricks/queryhistory"
	"github.com/stackshy/cloudemu/v2/server/azure/databricks/repos"
	"github.com/stackshy/cloudemu/v2/server/azure/databricks/scim"
	"github.com/stackshy/cloudemu/v2/server/azure/databricks/secrets"
	"github.com/stackshy/cloudemu/v2/server/azure/databricks/serving"
	"github.com/stackshy/cloudemu/v2/server/azure/databricks/sqlwarehouses"
	"github.com/stackshy/cloudemu/v2/server/azure/databricks/token"
	"github.com/stackshy/cloudemu/v2/server/azure/databricks/ucstorage"
	"github.com/stackshy/cloudemu/v2/server/azure/databricks/unitycatalog"
	"github.com/stackshy/cloudemu/v2/server/azure/databricks/wsfs"
	"github.com/stackshy/cloudemu/v2/server/azure/disks"
	dnssrv "github.com/stackshy/cloudemu/v2/server/azure/dns"
	eventgridsrv "github.com/stackshy/cloudemu/v2/server/azure/eventgrid"
	eventhubsrv "github.com/stackshy/cloudemu/v2/server/azure/eventhub"
	"github.com/stackshy/cloudemu/v2/server/azure/functions"
	"github.com/stackshy/cloudemu/v2/server/azure/iam"
	"github.com/stackshy/cloudemu/v2/server/azure/images"
	keyvaultsrv "github.com/stackshy/cloudemu/v2/server/azure/keyvault"
	kustosrv "github.com/stackshy/cloudemu/v2/server/azure/kusto"
	lbsrv "github.com/stackshy/cloudemu/v2/server/azure/loadbalancer"
	"github.com/stackshy/cloudemu/v2/server/azure/locks"
	loganalyticssrv "github.com/stackshy/cloudemu/v2/server/azure/loganalytics"
	"github.com/stackshy/cloudemu/v2/server/azure/managedcassandra"
	managedidentitysrv "github.com/stackshy/cloudemu/v2/server/azure/managedidentity"
	"github.com/stackshy/cloudemu/v2/server/azure/monitor"
	"github.com/stackshy/cloudemu/v2/server/azure/mysqlflex"
	notificationhubssrv "github.com/stackshy/cloudemu/v2/server/azure/notificationhubs"
	"github.com/stackshy/cloudemu/v2/server/azure/postgresflex"
	providerssrv "github.com/stackshy/cloudemu/v2/server/azure/providers"
	"github.com/stackshy/cloudemu/v2/server/azure/queue"
	"github.com/stackshy/cloudemu/v2/server/azure/resourcegraph"
	"github.com/stackshy/cloudemu/v2/server/azure/resourcegroups"
	azuresearchserver "github.com/stackshy/cloudemu/v2/server/azure/search"
	"github.com/stackshy/cloudemu/v2/server/azure/servicebus"
	"github.com/stackshy/cloudemu/v2/server/azure/snapshots"
	"github.com/stackshy/cloudemu/v2/server/azure/sql"
	"github.com/stackshy/cloudemu/v2/server/azure/sshpublickeys"
	storageaccountsrv "github.com/stackshy/cloudemu/v2/server/azure/storageaccount"
	"github.com/stackshy/cloudemu/v2/server/azure/subscriptions"
	synapsesrv "github.com/stackshy/cloudemu/v2/server/azure/synapse"
	tablesrv "github.com/stackshy/cloudemu/v2/server/azure/tablestorage"
	tagssrv "github.com/stackshy/cloudemu/v2/server/azure/tags"
	"github.com/stackshy/cloudemu/v2/server/azure/tenants"
	"github.com/stackshy/cloudemu/v2/server/azure/virtualmachines"
	"github.com/stackshy/cloudemu/v2/server/azure/vnet"
	azureaidriver "github.com/stackshy/cloudemu/v2/services/azureai/driver"
	azuresearchdriver "github.com/stackshy/cloudemu/v2/services/azuresearch/driver"
	cachedriver "github.com/stackshy/cloudemu/v2/services/cache/driver"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
	acidriver "github.com/stackshy/cloudemu/v2/services/containerinstances/driver"
	crdriver "github.com/stackshy/cloudemu/v2/services/containerregistry/driver"
	cpgdriver "github.com/stackshy/cloudemu/v2/services/cosmospostgresql/driver"
	dbdriver "github.com/stackshy/cloudemu/v2/services/database/driver"
	dbxdriver "github.com/stackshy/cloudemu/v2/services/databricks/driver"
	dnsdriver "github.com/stackshy/cloudemu/v2/services/dns/driver"
	ebdriver "github.com/stackshy/cloudemu/v2/services/eventbus/driver"
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
	"github.com/stackshy/cloudemu/v2/services/kubernetes"
	lbdriver "github.com/stackshy/cloudemu/v2/services/loadbalancer/driver"
	logdriver "github.com/stackshy/cloudemu/v2/services/logging/driver"
	mcdriver "github.com/stackshy/cloudemu/v2/services/managedcassandra/driver"
	mqdriver "github.com/stackshy/cloudemu/v2/services/messagequeue/driver"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
	notifdriver "github.com/stackshy/cloudemu/v2/services/notification/driver"
	rdbdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
	secretsdriver "github.com/stackshy/cloudemu/v2/services/secrets/driver"
	sdrv "github.com/stackshy/cloudemu/v2/services/serverless/driver"
	storagedriver "github.com/stackshy/cloudemu/v2/services/storage/driver"
	tabledriver "github.com/stackshy/cloudemu/v2/services/tablestorage/driver"
)

// Drivers bundles the driver interfaces the Azure server can expose. Leave a
// field nil to omit that service; the server returns 501 Not Implemented for
// any request that no registered handler matches.
//
// VirtualMachines / Disks / Snapshots / Images all delegate to the same
// compute driver — the driver's Volume*/Snapshot*/Image* methods back the
// corresponding resources.
type Drivers struct {
	VirtualMachines computedriver.Compute
	Disks           computedriver.Compute
	Snapshots       computedriver.Compute
	Images          computedriver.Compute
	SSHPublicKeys   computedriver.Compute
	BlobStorage     storagedriver.Bucket
	// QueueStorage serves the Azure Queue Storage data-plane REST API against
	// the messagequeue driver.
	QueueStorage mqdriver.MessageQueue
	// TableStorage serves the Azure Table Storage data-plane REST API against
	// the tablestorage driver.
	TableStorage tabledriver.TableStorage
	CosmosDB     dbdriver.Database
	// ManagedCassandra serves Microsoft.DocumentDB/cassandraClusters (Azure
	// Managed Instance for Apache Cassandra) via the ARM protocol.
	ManagedCassandra mcdriver.ManagedCassandra
	// CosmosPostgreSQL serves Microsoft.DBforPostgreSQL/serverGroupsv2 (Azure
	// Cosmos DB for PostgreSQL, Citus) via the ARM protocol.
	CosmosPostgreSQL cpgdriver.CosmosPostgreSQL
	Network          netdriver.Networking
	Monitor          mondriver.Monitoring
	Functions        sdrv.Serverless
	ServiceBus       mqdriver.MessageQueue
	SQL              rdbdriver.RelationalDB
	PostgresFlex     rdbdriver.RelationalDB
	MySQLFlex        rdbdriver.RelationalDB
	AKS              aksserver.Backend
	// ManagedIdentity serves Microsoft.ManagedIdentity/userAssignedIdentities.
	ManagedIdentity managedidentitysrv.Store
	// ContainerApps serves Microsoft.App managedEnvironments and containerApps.
	ContainerApps containerappssrv.Store
	IAM           iamdriver.IAM
	ACR           crdriver.ContainerRegistry
	// ContainerInstances serves the Azure Container Instances
	// (Microsoft.ContainerInstance/containerGroups) ARM API against the
	// containerinstances driver.
	ContainerInstances acidriver.ContainerInstances
	// KeyVault serves the Key Vault secrets data-plane API (/secrets/…)
	// against the secrets driver.
	KeyVault secretsdriver.Secrets
	// DNS serves the Azure DNS (Microsoft.Network/dnsZones) ARM API against the
	// dns driver.
	DNS dnsdriver.DNS
	// LB serves the Azure Load Balancer (Microsoft.Network/loadBalancers) ARM
	// API against the loadbalancer driver.
	LB lbdriver.LoadBalancer
	// EventGrid serves the Azure Event Grid (Microsoft.EventGrid/topics) ARM API
	// against the eventbus driver, mapping topics to event buses.
	EventGrid ebdriver.EventBus
	// LogAnalytics serves the Log Analytics
	// (Microsoft.OperationalInsights/workspaces) ARM API against the logging
	// driver. The workspace lifecycle maps onto the driver's log-group
	// lifecycle; the data-plane log-query API is out of scope.
	LogAnalytics logdriver.Logging
	// Cache serves the Azure Cache for Redis (Microsoft.Cache/redis) ARM API
	// against the cache driver's cluster control plane.
	Cache cachedriver.Cache
	// NotificationHubs serves the Microsoft.NotificationHubs ARM API against the
	// notification driver.
	NotificationHubs    notifdriver.Notification
	Databricks          dbxdriver.Databricks
	DatabricksDataPlane dbxdriver.DataPlane
	CognitiveServices   azureaidriver.CognitiveServices
	MachineLearning     azureaidriver.MachineLearning
	AzureAIDataPlane    azureaidriver.DataPlane
	SearchControl       azuresearchdriver.SearchControl
	SearchDataPlane     azuresearchdriver.SearchDataPlane
	// K8sAPI is the shared in-memory Kubernetes data-plane API server. It is
	// shared with awsserver.Drivers.K8sAPI and gcpserver.Drivers.K8sAPI so a
	// kubeconfig issued by any provider's control plane (EKS/AKS/GKE) reaches
	// the same backend. Leave nil to disable Kubernetes data-plane support.
	K8sAPI *kubernetes.APIServer
	// ResourceDiscovery is the cross-service inventory engine. Required to
	// serve Azure Resource Graph (armresourcegraph) requests. Leave nil to
	// omit the handler. SubscriptionID is needed for the subscription-scoping
	// check on incoming queries.
	ResourceDiscovery *resourcediscovery.Engine
	SubscriptionID    string
	// TenantID is the Azure AD tenant reported by the subscriptions Get/List and
	// the global tenants list. Empty falls back to defaultTenantID.
	TenantID string
	// EnforceAuth turns on Azure claims-based bearer authentication: each
	// incoming request must carry an "Authorization: Bearer <jwt>" whose claims
	// validate (accepted Azure audience, un-expired "exp", a principal claim),
	// and the resolved principal is attached to the request context. Missing,
	// malformed, expired or wrong-audience tokens are rejected with 401
	// InvalidAuthenticationToken. Off by default, which accepts any credentials
	// exactly as before.
	//
	// Because cloudemu does not hold Azure AD's signing key, the token SIGNATURE
	// is NOT verified — only its structure and claims are. This is a documented
	// limitation, distinct from AWS SigV4 where the shared secret lets the
	// emulator verify signatures. This gate is authentication only; RBAC
	// authorization is a follow-up.
	EnforceAuth bool
}

// defaultTenantID is reported when Drivers.TenantID is unset, so a caller
// verifying an account always sees a well-formed tenant GUID.
const defaultTenantID = "11111111-1111-1111-1111-111111111111"

// New returns a server that speaks the Azure ARM JSON wire protocol for every
// non-nil driver in d. Routing is path-based on
//
//	/subscriptions/{sub}/resourceGroups/{rg}/providers/{provider}/{type}/...
//
// so handlers can register independently — virtualMachines doesn't conflict
// with future blob storage or networking handlers.
//
// New returns an http.Handler speaking the Azure ARM JSON wire protocol for
// every non-nil driver in d. The assembled server is wrapped so unmodeled
// request properties survive into responses (see echoUnmodeledProperties)
// rather than being silently dropped.
//
//nolint:gocritic,gocyclo,gocognit,funlen // Drivers is all interface fields; one if-per-driver is the simplest expression
func New(d Drivers) http.Handler {
	srv := server.New()

	tenantID := d.TenantID
	if tenantID == "" {
		tenantID = defaultTenantID
	}

	// The subscriptions endpoints (list / get / locations) have no driver: the
	// emulator serves a single estate under one subscription and echoes it back.
	srv.Register(subscriptions.New(d.SubscriptionID, tenantID))

	// The global tenants list is a non-/subscriptions ARM path; register it
	// before the permissive blob fallback so it isn't swallowed as a blob call.
	srv.Register(tenants.New(tenantID))

	// Resource-provider registration (Microsoft.Resources "providers" surface):
	// the bare /subscriptions/{sub}/providers list, a single-namespace get, and
	// the register / unregister actions CLIs and IaC tools call at startup. It
	// has no driver — the emulator holds registration state in memory. Registered
	// early so its strict path match wins; its Matches never claims the deeper
	// /providers/{namespace}/{resourceType}/... paths owned by the service
	// handlers, so it does not shadow them.
	srv.Register(providerssrv.New())

	// Management locks (Microsoft.Authorization/locks) attach at any scope,
	// including a nested .../providers/{ns}/{type}/{name}/providers/Microsoft.
	// Authorization/locks/{lock} on an individual resource. A resource-scope lock
	// URL's leading /providers/{ns}/{type} pair is claimed by that resource type's
	// own handler (whose azurearm.ParsePath match ignores the trailing locks
	// segment), so the locks handler must register BEFORE the per-resource-type
	// handlers to win first-match dispatch. Its Matches is a scope-agnostic
	// substring test on /providers/microsoft.authorization/locks — a path no
	// resource handler produces — so registering it early shadows nothing. Locks
	// are a pure management-plane concept with no backing driver, so the handler
	// is always registered (like subscriptions/tenants). CRUD + round-trip only;
	// enforcement (blocking deletes/writes on locked resources) is out of scope.
	srv.Register(locks.New())

	// Build the per-service handlers that own resource-group-scoped resources up
	// front so they can be handed to the resource-group cascade below and then
	// registered at their normal positions. A resource group is a pure
	// container, so deleting it must delete the resources created under it;
	// each of these handlers implements ResourceGroupPurger to tear its own
	// resources down. Other resource types are not cascaded yet.
	var (
		vnetHandler    *vnet.Handler
		vmHandler      *virtualmachines.Handler
		storageHandler *storageaccountsrv.Handler
		lbHandler      *lbsrv.Handler
		rgPurgers      []resourcegroups.ResourceGroupPurger
	)

	// Virtual machines are purged before the networking resources they consume
	// (NICs, subnets): tearing a VM down first clears its NICs' virtualMachine
	// back-reference, so the vnet purger's NIC delete is not blocked by the
	// attached-NIC guard.
	if d.VirtualMachines != nil {
		vmHandler = virtualmachines.New(d.VirtualMachines, d.Network)
		rgPurgers = append(rgPurgers, vmHandler)
	}

	if d.Network != nil {
		vnetHandler = vnet.New(d.Network)
		rgPurgers = append(rgPurgers, vnetHandler)
	}

	if d.LB != nil {
		lbHandler = lbsrv.New(d.LB)

		// Project a backend address pool's read-only backendIPConfigurations from
		// the NIC side of the association, so NIC↔LB pool membership reflects on
		// both sides. Only wired when the networking driver exposes NICs.
		if nics, ok := d.Network.(netdriver.AzureNetworkInterfaces); ok {
			lbHandler.SetNICResolver(nics)
		}

		rgPurgers = append(rgPurgers, lbHandler)
	}

	if d.BlobStorage != nil {
		storageHandler = storageaccountsrv.New(d.BlobStorage)
		rgPurgers = append(rgPurgers, storageHandler)
	}

	// User-assigned managed identities: a resource-group-scoped resource, so its
	// handler joins the purge cascade. Registered further below.
	var managedIdentityHandler *managedidentitysrv.Handler
	if d.ManagedIdentity != nil {
		managedIdentityHandler = managedidentitysrv.New(d.ManagedIdentity)
		rgPurgers = append(rgPurgers, managedIdentityHandler)
	}

	// Container Apps (managed environments + container apps): resource-group-scoped
	// resources, so their handler joins the purge cascade. Registered further below.
	var containerAppsHandler *containerappssrv.Handler
	if d.ContainerApps != nil {
		containerAppsHandler = containerappssrv.New(d.ContainerApps)
		rgPurgers = append(rgPurgers, containerAppsHandler)
	}

	// Synapse workspaces are resource-group-scoped, so the (always-on,
	// driverless) Synapse handler joins the purge cascade. Registered further below.
	synapseHandler := synapsesrv.New()
	rgPurgers = append(rgPurgers, synapseHandler)

	// Resource groups have no driver of their own: they are containers, and the
	// emulator tracks membership by the ids resources already carry. The
	// discovery engine (nil-safe) lets exportTemplate enumerate that membership;
	// the purgers cascade a group delete into its resources.
	srv.Register(resourcegroups.New(d.ResourceDiscovery, rgPurgers...))

	// Tags resource provider (Microsoft.Resources/tags/default). Self-contained
	// (no driver): it owns the per-scope tag sets an armresources TagsClient
	// manages at subscription or resource scope. Its path suffix
	// /providers/Microsoft.Resources/tags/default is disjoint from the
	// resource-group paths above and the Microsoft.ResourceGraph/generic-resources
	// listings, so registration order is unconstrained.
	srv.Register(tagssrv.New())

	// microsoft.insights extension resources (metrics, metricDefinitions,
	// diagnosticSettings) hang off an arbitrary resource URI, so they must claim
	// those paths before the underlying resource's own handler. Registered first.
	if d.Monitor != nil {
		srv.Register(monitor.NewMetricsHandler(d.Monitor))
		srv.Register(monitor.NewDiagnosticSettingsHandler())
		// Activity Log read API — registered only when the monitoring backend
		// supports the recorder capability, so its suffix match wins over any
		// resource handler for .../eventtypes/management/values.
		if alh := monitor.NewActivityLogHandler(d.Monitor); alh != nil {
			srv.Register(alh)
		}
	}

	// Register more-specific compute resource handlers first so their
	// resourceType match wins over virtualMachines (which also accepts the
	// locations sub-path used for async-operation polling).
	if d.Disks != nil {
		srv.Register(disks.New(d.Disks))
	}

	if d.Snapshots != nil {
		srv.Register(snapshots.New(d.Snapshots))
	}

	if d.Images != nil {
		srv.Register(images.New(d.Images))
	}

	if d.SSHPublicKeys != nil {
		srv.Register(sshpublickeys.New(d.SSHPublicKeys))
	}

	// Cosmos DB matches on /dbs/* paths — register before the catch-all
	// blob handler.
	if d.CosmosDB != nil {
		cosmosDataPlane := cosmosdb.New(d.CosmosDB)
		srv.Register(cosmosDataPlane)
		// Cosmos-account ARM control plane (Microsoft.DocumentDB/databaseAccounts).
		// Claims only the /providers/Microsoft.DocumentDB/databaseAccounts/
		// management path — disjoint from the /dbs data plane above and from
		// managedcassandra (cassandraClusters), so order is unconstrained. Account
		// DELETE is delegated to the data-plane handler so the account is torn
		// down fully (tables, attributes and data-plane bookkeeping).
		srv.Register(cosmosaccount.New(d.CosmosDB, cosmosDataPlane))
	}

	// Managed Cassandra matches ARM Microsoft.DocumentDB/cassandraClusters paths
	// (disjoint from every other Azure handler).
	if d.ManagedCassandra != nil {
		srv.Register(managedcassandra.New(d.ManagedCassandra))
	}

	if d.CosmosPostgreSQL != nil {
		srv.Register(cosmospostgresql.New(d.CosmosPostgreSQL))
	}

	if vnetHandler != nil {
		srv.Register(vnetHandler)
	}

	// Azure DNS shares the Microsoft.Network ARM provider with the network
	// handler above, but claims a disjoint resource type (dnsZones vs
	// virtualNetworks / networkSecurityGroups / locations), so registration
	// order relative to it is unconstrained. Registered before the BlobStorage
	// fallback.
	if d.DNS != nil {
		srv.Register(dnssrv.New(d.DNS))
	}

	// Azure Load Balancer shares the Microsoft.Network ARM provider with the
	// network handler above and the DNS handler, but claims a disjoint resource
	// type (loadBalancers vs virtualNetworks / networkSecurityGroups /
	// locations / dnsZones), so registration order relative to them is
	// unconstrained. Registered before the BlobStorage fallback.
	if lbHandler != nil {
		srv.Register(lbHandler)
	}

	// Event Grid claims Microsoft.EventGrid/topics — a distinct ARM provider
	// name from every other Azure handler, so registration order is
	// unconstrained. Registered before the BlobStorage fallback.
	if d.EventGrid != nil {
		srv.Register(eventgridsrv.New(d.EventGrid))
		// Data-plane publish endpoint (POST /api/events, topic taken from the
		// request Host). Registered before the BlobStorage fallback.
		srv.Register(eventgridsrv.NewPublishHandler(d.EventGrid))
	}

	// Log Analytics matches on Microsoft.OperationalInsights/workspaces — a
	// distinct ARM provider name from every other Azure handler, so registration
	// order is unconstrained. Registered before the BlobStorage fallback.
	if d.LogAnalytics != nil {
		srv.Register(loganalyticssrv.New(d.LogAnalytics))
	}

	// Azure Cache for Redis matches on the Microsoft.Cache ARM provider — a
	// unique provider name among Azure handlers, so registration order is
	// unconstrained. Registered before the BlobStorage fallback.
	if d.Cache != nil {
		srv.Register(cachesrv.New(d.Cache))
	}

	// Notification Hubs matches on the Microsoft.NotificationHubs provider — a
	// distinct ARM provider name from every other Azure handler, so
	// registration order is unconstrained. Registered before the BlobStorage
	// fallback.
	if d.NotificationHubs != nil {
		srv.Register(notificationhubssrv.New(d.NotificationHubs))
		// Data-plane device registration API on
		// {namespace}.servicebus.windows.net. Registered before the BlobStorage
		// fallback.
		srv.Register(notificationhubssrv.NewRegistrationHandler(d.NotificationHubs))
	}

	if d.Monitor != nil {
		srv.Register(monitor.New(d.Monitor))
	}

	if d.Functions != nil {
		srv.Register(functions.New(d.Functions))
	}

	if d.ServiceBus != nil {
		srv.Register(servicebus.New(d.ServiceBus))
	}

	// Event Hubs claims Microsoft.EventHub/namespaces (and their eventhubs,
	// consumergroups and authorizationRules) — a distinct ARM provider name from
	// every other Azure handler, so registration order is unconstrained. It is a
	// self-contained control-plane handler with no backing driver (its state is
	// namespace-scoped ARM containers, like locks/tags), so it is always
	// registered. The Event Hubs data plane is AMQP/Kafka only and out of scope.
	srv.Register(eventhubsrv.New())

	// Kusto (Azure Data Explorer) claims Microsoft.Kusto/clusters (and their
	// databases) — a distinct ARM provider name from every other Azure handler,
	// so registration order is unconstrained. It is a self-contained control-plane
	// handler with no backing driver (its state is cluster-scoped ARM containers,
	// like Event Hubs), so it is always registered.
	srv.Register(kustosrv.New())

	// Kusto query data plane serves the globally-unique /v1|v2/rest/{mgmt,query}
	// paths clients POST to the cluster host. The paths collide with no other
	// handler, so registration order is unconstrained; like the control plane it
	// is driverless, holding the ingested tables in memory. This increment serves
	// the control commands (.create/.show/.drop table); the KQL query evaluator
	// arrives in a later increment.
	srv.Register(kustosrv.NewDataPlane())

	// Synapse claims Microsoft.Synapse/workspaces (and their sqlPools,
	// bigDataPools and integrationRuntimes) — a distinct ARM provider name from
	// every other Azure handler, so registration order is unconstrained. Like
	// Event Hubs it is a self-contained control-plane handler with no backing
	// driver (its state is workspace-scoped ARM containers), so it is always
	// registered. Created above and joined to the resource-group purge cascade.
	srv.Register(synapseHandler)

	// Microsoft.Sql provider — distinct ARM provider name from compute and
	// network so registration order is unconstrained.
	if d.SQL != nil {
		srv.Register(sql.New(d.SQL))
	}

	// Postgres Flex matches on a distinct provider name
	// (Microsoft.DBforPostgreSQL) so registration order is unconstrained.
	if d.PostgresFlex != nil {
		srv.Register(postgresflex.New(d.PostgresFlex))
	}

	// MySQL Flex matches on Microsoft.DBforMySQL provider — distinct from
	// Postgres Flex and SQL, so registration order is unconstrained.
	if d.MySQLFlex != nil {
		srv.Register(mysqlflex.New(d.MySQLFlex))
	}

	// AKS matches on Microsoft.ContainerService provider — distinct ARM
	// provider name from compute / network / database, so registration order
	// is unconstrained.
	if d.AKS != nil {
		srv.Register(aksserver.New(d.AKS))
	}

	// Databricks matches on Microsoft.Databricks/workspaces — a distinct ARM
	// provider name, so registration order is unconstrained.
	if d.Databricks != nil {
		srv.Register(databricks.New(d.Databricks))
	}

	registerDatabricksDataPlane(srv, &d)

	// Cognitive Services matches on Microsoft.CognitiveServices/accounts — a
	// distinct ARM provider name, so registration order is unconstrained.
	if d.CognitiveServices != nil {
		srv.Register(azureaiserver.NewCognitiveServices(d.CognitiveServices))
	}

	// Azure ML matches on Microsoft.MachineLearningServices — a distinct ARM
	// provider name, so registration order is unconstrained.
	if d.MachineLearning != nil {
		srv.Register(azureaiserver.NewMachineLearning(d.MachineLearning))
	}

	// Azure AI data plane (Azure OpenAI inference + Assistants, AML scoring).
	// Matches on /openai/ and /score — disjoint from the ARM /subscriptions/
	// prefix, so registration order is unconstrained.
	if d.AzureAIDataPlane != nil {
		srv.Register(azureaiserver.NewDataPlane(d.AzureAIDataPlane))
	}

	// Azure AI Search — ARM control plane on Microsoft.Search, plus the
	// host/path-routed search data plane (/indexes, /indexers, …).
	if d.SearchControl != nil {
		srv.Register(azuresearchserver.NewControl(d.SearchControl))
	}

	if d.SearchDataPlane != nil {
		srv.Register(azuresearchserver.NewDataPlane(d.SearchDataPlane))
	}

	if vmHandler != nil {
		srv.Register(vmHandler)
	}

	// Kubernetes data-plane API. Matches /k8s/{uid}/... — disjoint from every
	// other Azure path. Registered before the BlobStorage fallback.
	if d.K8sAPI != nil {
		srv.Register(d.K8sAPI)
	}

	// Resource Graph matches /providers/Microsoft.ResourceGraph/... —
	// distinct from any service-scoped ARM URL, so registration order is
	// unconstrained relative to the resource handlers above.
	if d.ResourceDiscovery != nil {
		srv.Register(resourcegraph.New(d.ResourceDiscovery, d.SubscriptionID))
		// Generic Microsoft.Resources listing (az resource list) at subscription
		// and resource-group scope, backed by the same discovery engine.
		srv.Register(resourcegraph.NewResources(d.ResourceDiscovery, d.SubscriptionID))
		// Cost Management query matches any scope ending in
		// /providers/Microsoft.CostManagement/query — a distinct ARM provider
		// name from every other handler, so registration order is unconstrained.
		// Backed by the same discovery engine, priced through services/cost.
		srv.Register(costmanagement.New(d.ResourceDiscovery))
	}

	// Managed identities claim Microsoft.ManagedIdentity/userAssignedIdentities —
	// a distinct ARM provider name from every other Azure handler, so
	// registration order is unconstrained. Registered before the BlobStorage
	// fallback.
	if managedIdentityHandler != nil {
		srv.Register(managedIdentityHandler)
	}

	// Container Apps claim Microsoft.App/{managedEnvironments,containerApps} — a
	// distinct ARM provider name from every other Azure handler, so registration
	// order is unconstrained. Registered before the BlobStorage fallback.
	if containerAppsHandler != nil {
		srv.Register(containerAppsHandler)
	}

	// IAM matches /providers/Microsoft.Authorization/role{Definitions,Assignments}
	// at any scope — distinct from every other ARM provider name, so
	// registration order is unconstrained.
	if d.IAM != nil {
		srv.Register(iam.New(d.IAM))
	}

	// ACR data-plane catalog API matches /acr/v1/… — disjoint from ARM and
	// must register before the permissive BlobStorage fallback below.
	if d.ACR != nil {
		srv.Register(acr.New(d.ACR))

		// ACR ARM management plane (Microsoft.ContainerRegistry/registries) is
		// an Azure-specific optional capability; register it when the driver
		// implements the manager surface.
		if mgr, ok := d.ACR.(crdriver.AzureRegistryManager); ok {
			srv.Register(acr.NewARM(mgr))
		}
	}

	// Azure Container Instances claims a unique ARM provider
	// (Microsoft.ContainerInstance), so registration order is unconstrained;
	// registered before the BlobStorage fallback.
	if d.ContainerInstances != nil {
		srv.Register(containerinstancessrv.New(d.ContainerInstances))
	}

	// Key Vault secrets data-plane API matches /secrets/… — disjoint from ARM
	// and from the Databricks secrets API (/api/{ver}/secrets), and must
	// register before the permissive BlobStorage fallback below.
	if d.KeyVault != nil {
		srv.Register(keyvaultsrv.New(d.KeyVault))
		// Key Vault ARM control plane (Microsoft.KeyVault/vaults). A unique ARM
		// provider name among Azure handlers, so registration order is
		// unconstrained; registered only when the backend implements the
		// KeyVaultVaults surface. Disjoint from the /secrets, /keys and
		// /certificates data-plane handlers below.
		if vaults, ok := d.KeyVault.(secretsdriver.KeyVaultVaults); ok {
			srv.Register(keyvaultsrv.NewVaultARM(vaults))
		}
		// Keys data-plane matches /keys and /deletedkeys — disjoint from the
		// /secrets surface above and from ARM. Registered only when the backend
		// implements the KeyVaultKeys surface.
		if _, ok := d.KeyVault.(secretsdriver.KeyVaultKeys); ok {
			srv.Register(keyvaultsrv.NewKeys(d.KeyVault))
		}
		// Certificates data-plane matches /certificates and /deletedcertificates.
		// Registering here — before the permissive Table/Blob storage fallbacks
		// below — is the routing fix: without it certificate requests fall
		// through to storage and return an odata error instead of a Key Vault
		// response. Registered only when the backend implements the surface.
		if _, ok := d.KeyVault.(secretsdriver.KeyVaultCertificates); ok {
			srv.Register(keyvaultsrv.NewCerts(d.KeyVault))
		}
	}

	// Table Storage matches the OData table surface (/Tables, /Tables('name'),
	// /{table}(…) entity predicates, and POST /{table} inserts) — path shapes
	// that contain parentheses or a bare JSON POST, disjoint from Blob's
	// container/blob paths and Queue's /messages surface. Registered before the
	// permissive Blob fallback.
	if d.TableStorage != nil {
		srv.Register(tablesrv.New(d.TableStorage))
	}

	// Queue Storage matches the queue data-plane surface (/{queue}/messages,
	// bare PUT/DELETE /{queue} without restype=container). These shapes are
	// disjoint from Blob (which carries restype=container) and Table (which
	// carries OData parentheses). Registered before the permissive Blob
	// fallback.
	if d.QueueStorage != nil {
		srv.Register(queue.New(d.QueueStorage))
	}

	// Storage-account ARM control plane (Microsoft.Storage/storageAccounts).
	// Claims only the /providers/Microsoft.Storage/storageAccounts/ management
	// path (which starts with /subscriptions/), disjoint from the blob
	// data-plane fallback below, so it must register before that fallback.
	if storageHandler != nil {
		srv.Register(storageHandler)
	}

	// BlobStorage handler is the data-plane fallback for non-ARM URLs. It
	// must register last so its permissive Matches() doesn't shadow the
	// ARM-specific resource handlers.
	if d.BlobStorage != nil {
		srv.Register(blobstorage.New(d.BlobStorage))
	}

	// Opt-in claims-based bearer authentication. Installed only when enabled, so
	// the default request path is byte-for-byte unchanged. Registered on the raw
	// server so the pre-dispatch gate runs before handler matching (and before
	// the unmodeled-property overlay below wraps the response).
	if d.EnforceAuth {
		srv.SetPreDispatch(newAuthGate(config.RealClock{}))
	}

	// When the monitoring backend can record Activity Log events, observe every
	// served ARM request and log a management event so the Activity Log API
	// reflects real API activity. This is the Azure analog of the AWS
	// CloudTrail observer.
	if rec, ok := d.Monitor.(mondriver.ActivityLogRecorder); ok {
		srv.SetObserver(func(r *http.Request) { recordActivityLogEvent(rec, r) })
	}

	return echoUnmodeledProperties(srv, newPropertyOverlay())
}

// registerDatabricksDataPlane registers the Databricks workspace data-plane
// handlers when the data plane is enabled. The core handler is driver-backed;
// the rest are self-contained handlers that own their in-memory state and
// claim disjoint /api path prefixes (so registration order is unconstrained).
// They sit before the blob fallback so their REST URLs aren't swallowed.
func registerDatabricksDataPlane(srv *server.Server, d *Drivers) {
	if d.DatabricksDataPlane == nil {
		return
	}

	srv.Register(databricks.NewDataPlane(d.DatabricksDataPlane))
	srv.Register(hostmeta.New())
	srv.Register(secrets.New())
	srv.Register(token.New())
	srv.Register(gitcredentials.New())
	srv.Register(repos.New())
	srv.Register(dbfs.New())
	srv.Register(wsfs.New())
	srv.Register(sqlwarehouses.New())
	srv.Register(queryhistory.New())
	srv.Register(pipelines.New())
	srv.Register(serving.New())
	srv.Register(unitycatalog.New())
	srv.Register(ucstorage.New())
	srv.Register(scim.New())
}
