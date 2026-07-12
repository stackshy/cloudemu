// Package azure assembles CloudEmu's Azure-compatible HTTP server.
//
// New takes a Drivers bundle and returns a *server.Server preloaded with the
// handler for each non-nil driver. Consumers that want a single service can
// skip this package and register the handler directly on their own
// server.Server.
package azure

import (
	"github.com/stackshy/cloudemu/v2/server"
	"github.com/stackshy/cloudemu/v2/server/azure/acr"
	aksserver "github.com/stackshy/cloudemu/v2/server/azure/aks"
	azureaiserver "github.com/stackshy/cloudemu/v2/server/azure/azureai"
	azuresearchserver "github.com/stackshy/cloudemu/v2/server/azure/azuresearch"
	"github.com/stackshy/cloudemu/v2/server/azure/azuresql"
	"github.com/stackshy/cloudemu/v2/server/azure/blob"
	cachesrv "github.com/stackshy/cloudemu/v2/server/azure/cache"
	"github.com/stackshy/cloudemu/v2/server/azure/cosmos"
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
	"github.com/stackshy/cloudemu/v2/server/azure/functions"
	"github.com/stackshy/cloudemu/v2/server/azure/iam"
	"github.com/stackshy/cloudemu/v2/server/azure/images"
	keyvaultsrv "github.com/stackshy/cloudemu/v2/server/azure/keyvault"
	lbsrv "github.com/stackshy/cloudemu/v2/server/azure/loadbalancer"
	loganalyticssrv "github.com/stackshy/cloudemu/v2/server/azure/loganalytics"
	"github.com/stackshy/cloudemu/v2/server/azure/monitor"
	"github.com/stackshy/cloudemu/v2/server/azure/mysqlflex"
	"github.com/stackshy/cloudemu/v2/server/azure/network"
	notificationhubssrv "github.com/stackshy/cloudemu/v2/server/azure/notificationhubs"
	"github.com/stackshy/cloudemu/v2/server/azure/postgresflex"
	"github.com/stackshy/cloudemu/v2/server/azure/queue"
	"github.com/stackshy/cloudemu/v2/server/azure/resourcegraph"
	"github.com/stackshy/cloudemu/v2/server/azure/servicebus"
	"github.com/stackshy/cloudemu/v2/server/azure/snapshots"
	"github.com/stackshy/cloudemu/v2/server/azure/sshpublickeys"
	tablesrv "github.com/stackshy/cloudemu/v2/server/azure/table"
	"github.com/stackshy/cloudemu/v2/server/azure/virtualmachines"
	azureaidriver "github.com/stackshy/cloudemu/v2/services/azureai/driver"
	azuresearchdriver "github.com/stackshy/cloudemu/v2/services/azuresearch/driver"
	cachedriver "github.com/stackshy/cloudemu/v2/services/cache/driver"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
	crdriver "github.com/stackshy/cloudemu/v2/services/containerregistry/driver"
	dbdriver "github.com/stackshy/cloudemu/v2/services/database/driver"
	dbxdriver "github.com/stackshy/cloudemu/v2/services/databricks/driver"
	dnsdriver "github.com/stackshy/cloudemu/v2/services/dns/driver"
	ebdriver "github.com/stackshy/cloudemu/v2/services/eventbus/driver"
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
	"github.com/stackshy/cloudemu/v2/services/kubernetes"
	lbdriver "github.com/stackshy/cloudemu/v2/services/loadbalancer/driver"
	logdriver "github.com/stackshy/cloudemu/v2/services/logging/driver"
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
	Network      netdriver.Networking
	Monitor      mondriver.Monitoring
	Functions    sdrv.Serverless
	ServiceBus   mqdriver.MessageQueue
	SQL          rdbdriver.RelationalDB
	PostgresFlex rdbdriver.RelationalDB
	MySQLFlex    rdbdriver.RelationalDB
	AKS          aksserver.Backend
	IAM          iamdriver.IAM
	ACR          crdriver.ContainerRegistry
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
}

// New returns a server that speaks the Azure ARM JSON wire protocol for every
// non-nil driver in d. Routing is path-based on
//
//	/subscriptions/{sub}/resourceGroups/{rg}/providers/{provider}/{type}/...
//
// so handlers can register independently — virtualMachines doesn't conflict
// with future blob storage or networking handlers.
//
//nolint:gocritic,gocyclo // Drivers is all interface fields; one if-per-driver is the simplest expression
func New(d Drivers) *server.Server {
	srv := server.New()

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
		srv.Register(cosmos.New(d.CosmosDB))
	}

	if d.Network != nil {
		srv.Register(network.New(d.Network))
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
	if d.LB != nil {
		srv.Register(lbsrv.New(d.LB))
	}

	// Event Grid claims Microsoft.EventGrid/topics — a distinct ARM provider
	// name from every other Azure handler, so registration order is
	// unconstrained. Registered before the BlobStorage fallback.
	if d.EventGrid != nil {
		srv.Register(eventgridsrv.New(d.EventGrid))
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

	// Microsoft.Sql provider — distinct ARM provider name from compute and
	// network so registration order is unconstrained.
	if d.SQL != nil {
		srv.Register(azuresql.New(d.SQL))
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

	if d.VirtualMachines != nil {
		srv.Register(virtualmachines.New(d.VirtualMachines))
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
	}

	// Key Vault secrets data-plane API matches /secrets/… — disjoint from ARM
	// and from the Databricks secrets API (/api/{ver}/secrets), and must
	// register before the permissive BlobStorage fallback below.
	if d.KeyVault != nil {
		srv.Register(keyvaultsrv.New(d.KeyVault))
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

	// BlobStorage handler is the data-plane fallback for non-ARM URLs. It
	// must register last so its permissive Matches() doesn't shadow the
	// ARM-specific resource handlers.
	if d.BlobStorage != nil {
		srv.Register(blob.New(d.BlobStorage))
	}

	return srv
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
