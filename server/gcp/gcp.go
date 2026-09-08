// Package gcp assembles CloudEmu's GCP-compatible HTTP server.
//
// New takes a Drivers bundle and returns a *server.Server preloaded with the
// handler for each non-nil driver. Consumers that want a single service can
// skip this package and register the handler directly on their own
// server.Server.
package gcp

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/config"
	gkeprov "github.com/stackshy/cloudemu/v2/providers/gcp/gke"
	gcpmon "github.com/stackshy/cloudemu/v2/providers/gcp/monitoring"
	"github.com/stackshy/cloudemu/v2/server"
	alloydbsrv "github.com/stackshy/cloudemu/v2/server/gcp/alloydb"
	apigatewaysrv "github.com/stackshy/cloudemu/v2/server/gcp/apigateway"
	"github.com/stackshy/cloudemu/v2/server/gcp/artifactregistry"
	bigqueryserver "github.com/stackshy/cloudemu/v2/server/gcp/bigquery"
	bigtableserver "github.com/stackshy/cloudemu/v2/server/gcp/bigtable"
	binauthzsrv "github.com/stackshy/cloudemu/v2/server/gcp/binaryauthorization"
	certmanagersrv "github.com/stackshy/cloudemu/v2/server/gcp/certificatemanager"
	"github.com/stackshy/cloudemu/v2/server/gcp/cloudasset"
	"github.com/stackshy/cloudemu/v2/server/gcp/cloudbilling"
	clouddeploysrv "github.com/stackshy/cloudemu/v2/server/gcp/clouddeploy"
	"github.com/stackshy/cloudemu/v2/server/gcp/clouddns"
	"github.com/stackshy/cloudemu/v2/server/gcp/cloudfunctions"
	cloudidssrv "github.com/stackshy/cloudemu/v2/server/gcp/cloudids"
	cloudloggingsrv "github.com/stackshy/cloudemu/v2/server/gcp/cloudlogging"
	cloudrunsrv "github.com/stackshy/cloudemu/v2/server/gcp/cloudrun"
	"github.com/stackshy/cloudemu/v2/server/gcp/cloudsql"
	cloudtaskssrv "github.com/stackshy/cloudemu/v2/server/gcp/cloudtasks"
	composersrv "github.com/stackshy/cloudemu/v2/server/gcp/composer"
	"github.com/stackshy/cloudemu/v2/server/gcp/compute"
	datacatalogsrv "github.com/stackshy/cloudemu/v2/server/gcp/datacatalog"
	dataformsrv "github.com/stackshy/cloudemu/v2/server/gcp/dataform"
	datafusionsrv "github.com/stackshy/cloudemu/v2/server/gcp/datafusion"
	dataplexsrv "github.com/stackshy/cloudemu/v2/server/gcp/dataplex"
	dataprocsrv "github.com/stackshy/cloudemu/v2/server/gcp/dataproc"
	datastreamsrv "github.com/stackshy/cloudemu/v2/server/gcp/datastream"
	"github.com/stackshy/cloudemu/v2/server/gcp/eventarc"
	fcmsrv "github.com/stackshy/cloudemu/v2/server/gcp/fcm"
	filestoresrv "github.com/stackshy/cloudemu/v2/server/gcp/filestore"
	"github.com/stackshy/cloudemu/v2/server/gcp/firestore"
	"github.com/stackshy/cloudemu/v2/server/gcp/gcs"
	"github.com/stackshy/cloudemu/v2/server/gcp/gke"
	gkebackupsrv "github.com/stackshy/cloudemu/v2/server/gcp/gkebackup"
	gkehubsrv "github.com/stackshy/cloudemu/v2/server/gcp/gkehub"
	"github.com/stackshy/cloudemu/v2/server/gcp/iam"
	kmssrv "github.com/stackshy/cloudemu/v2/server/gcp/kms"
	lbsrv "github.com/stackshy/cloudemu/v2/server/gcp/loadbalancer"
	"github.com/stackshy/cloudemu/v2/server/gcp/lro"
	memorystoresrv "github.com/stackshy/cloudemu/v2/server/gcp/memorystore"
	metastoresrv "github.com/stackshy/cloudemu/v2/server/gcp/metastore"
	"github.com/stackshy/cloudemu/v2/server/gcp/monitoring"
	networkconnectivitysrv "github.com/stackshy/cloudemu/v2/server/gcp/networkconnectivity"
	privatecasrv "github.com/stackshy/cloudemu/v2/server/gcp/privateca"
	"github.com/stackshy/cloudemu/v2/server/gcp/pubsub"
	"github.com/stackshy/cloudemu/v2/server/gcp/resourcemanager"
	schedulersrv "github.com/stackshy/cloudemu/v2/server/gcp/scheduler"
	secretmanagersrv "github.com/stackshy/cloudemu/v2/server/gcp/secretmanager"
	securesourcemanagersrv "github.com/stackshy/cloudemu/v2/server/gcp/securesourcemanager"
	servicedirectorysrv "github.com/stackshy/cloudemu/v2/server/gcp/servicedirectory"
	"github.com/stackshy/cloudemu/v2/server/gcp/servicenetworking"
	spannersrv "github.com/stackshy/cloudemu/v2/server/gcp/spanner"
	vertexaisrv "github.com/stackshy/cloudemu/v2/server/gcp/vertexai"
	"github.com/stackshy/cloudemu/v2/server/gcp/vpc"
	vpcaccesssrv "github.com/stackshy/cloudemu/v2/server/gcp/vpcaccess"
	workflowssrv "github.com/stackshy/cloudemu/v2/server/gcp/workflows"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	agdriver "github.com/stackshy/cloudemu/v2/services/apigatewaygcp/driver"
	bqdriver "github.com/stackshy/cloudemu/v2/services/bigquery/driver"
	btdriver "github.com/stackshy/cloudemu/v2/services/bigtable/driver"
	badriver "github.com/stackshy/cloudemu/v2/services/binaryauthorization/driver"
	cachedriver "github.com/stackshy/cloudemu/v2/services/cache/driver"
	certmanagerdriver "github.com/stackshy/cloudemu/v2/services/certificatemanager/driver"
	clouddeploydriver "github.com/stackshy/cloudemu/v2/services/clouddeploy/driver"
	cloudidsdriver "github.com/stackshy/cloudemu/v2/services/cloudids/driver"
	cloudrundriver "github.com/stackshy/cloudemu/v2/services/cloudrun/driver"
	ctdriver "github.com/stackshy/cloudemu/v2/services/cloudtasks/driver"
	composerdriver "github.com/stackshy/cloudemu/v2/services/composer/driver"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
	crdriver "github.com/stackshy/cloudemu/v2/services/containerregistry/driver"
	dbdriver "github.com/stackshy/cloudemu/v2/services/database/driver"
	dcdriver "github.com/stackshy/cloudemu/v2/services/datacatalog/driver"
	dataformdriver "github.com/stackshy/cloudemu/v2/services/dataform/driver"
	datafusiondriver "github.com/stackshy/cloudemu/v2/services/datafusion/driver"
	dataplexdriver "github.com/stackshy/cloudemu/v2/services/dataplex/driver"
	dataprocdriver "github.com/stackshy/cloudemu/v2/services/dataproc/driver"
	datastreamdriver "github.com/stackshy/cloudemu/v2/services/datastream/driver"
	dnsdriver "github.com/stackshy/cloudemu/v2/services/dns/driver"
	ebdriver "github.com/stackshy/cloudemu/v2/services/eventbus/driver"
	gkebackupdriver "github.com/stackshy/cloudemu/v2/services/gkebackup/driver"
	gkehubdriver "github.com/stackshy/cloudemu/v2/services/gkehub/driver"
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
	"github.com/stackshy/cloudemu/v2/services/kubernetes"
	lbdriver "github.com/stackshy/cloudemu/v2/services/loadbalancer/driver"
	logdriver "github.com/stackshy/cloudemu/v2/services/logging/driver"
	mqdriver "github.com/stackshy/cloudemu/v2/services/messagequeue/driver"
	metastoredriver "github.com/stackshy/cloudemu/v2/services/metastore/driver"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	nccdriver "github.com/stackshy/cloudemu/v2/services/networkconnectivity/driver"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
	notifdriver "github.com/stackshy/cloudemu/v2/services/notification/driver"
	privatecadriver "github.com/stackshy/cloudemu/v2/services/privateca/driver"
	rdbdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
	scheddriver "github.com/stackshy/cloudemu/v2/services/scheduler/driver"
	secretsdriver "github.com/stackshy/cloudemu/v2/services/secrets/driver"
	securesourcemanagerdriver "github.com/stackshy/cloudemu/v2/services/securesourcemanager/driver"
	sdrv "github.com/stackshy/cloudemu/v2/services/serverless/driver"
	sddriver "github.com/stackshy/cloudemu/v2/services/servicedirectory/driver"
	spannerdriver "github.com/stackshy/cloudemu/v2/services/spanner/driver"
	storagedriver "github.com/stackshy/cloudemu/v2/services/storage/driver"
	vertexaidriver "github.com/stackshy/cloudemu/v2/services/vertexai/driver"
	vpcaccessdriver "github.com/stackshy/cloudemu/v2/services/vpcaccess/driver"
	workflowsdriver "github.com/stackshy/cloudemu/v2/services/workflows/driver"
)

// Drivers bundles the driver interfaces the GCP server can expose.
type Drivers struct {
	Compute        computedriver.Compute
	Storage        storagedriver.Bucket
	Firestore      dbdriver.Database
	Networking     netdriver.Networking
	Monitoring     mondriver.Monitoring
	CloudFunctions sdrv.Serverless
	// CloudRun serves the Cloud Run Admin API v2 Jobs surface
	// (/v2/projects/{p}/locations/{l}/jobs…) against the cloudrun driver.
	CloudRun cloudrundriver.CloudRun
	PubSub   mqdriver.MessageQueue
	Bigtable btdriver.Admin
	// BigQuery serves the bigquery.googleapis.com v2 dataset + table metadata
	// control plane against the bigquery driver.
	BigQuery bqdriver.BigQuery
	CloudSQL rdbdriver.RelationalDB
	GKE      *gkeprov.Mock
	// AlloyDB serves the alloydb.googleapis.com v1 REST API against a
	// relationaldb driver that also implements the AlloyDB capability. Its
	// paths (/v1/projects/{p}/locations/{l}/clusters…) are identical to GKE's,
	// so the two cannot be multiplexed on one server; AlloyDB is left nil in
	// DriversFrom and injected by callers that want it instead of GKE.
	AlloyDB rdbdriver.RelationalDB
	// Spanner serves the spanner.googleapis.com v1 admin REST API (instance +
	// database control plane) against the spanner driver. It shares the
	// /v1/projects/{p}/instances URL space with Cloud SQL; the Spanner handler's
	// Matches disambiguates by content and instance ownership, and it registers
	// ahead of Cloud SQL (see New).
	Spanner spannerdriver.Spanner
	// Dataproc serves the dataproc.googleapis.com v1 cluster control plane against
	// the dataproc driver. Its paths live under /v1/projects/{p}/regions/{r}/
	// {clusters|operations}; the handler's Matches narrows on the regions keyword
	// and the resource segment so it is disjoint from every other /v1/projects/
	// handler, and Compute's regional paths are under the /compute/v1/ prefix.
	Dataproc dataprocdriver.Dataproc
	// Datastream serves the datastream.googleapis.com v1 connection-profile and
	// stream control plane against the datastream driver. Its paths live under
	// /v1/projects/{p}/locations/{l}/{connectionProfiles|streams}[/…]; the
	// handler's Matches narrows on those resource segments, so it is disjoint from
	// every other /v1/projects/ handler, and its location-scoped operation polls
	// are owned by the shared LRO poller.
	Datastream datastreamdriver.Datastream
	// CertificateManager serves the certificatemanager.googleapis.com v1
	// certificate, certificate-map, and DNS-authorization control plane against
	// the certificatemanager driver. Its paths live under /v1/projects/{p}/
	// locations/{l}/{certificates|certificateMaps|dnsAuthorizations}[/…]; the
	// handler's Matches narrows on those resource segments, so it is disjoint from
	// every other /v1/projects/ handler, and its location-scoped operation polls
	// are owned by the shared LRO poller.
	CertificateManager certmanagerdriver.CertificateManager
	// PrivateCA serves the privateca.googleapis.com v1 Certificate Authority
	// Service control plane (CA pools, certificate authorities, certificate
	// templates, certificates) against the privateca driver. Its paths live under
	// /v1/projects/{p}/locations/{l}/{caPools|certificateTemplates}[/…]; the
	// handler's Matches narrows on those resource segments, so it is disjoint from
	// every other /v1/projects/ handler (its certificates are nested under a caPool
	// and never collide with certificatemanager's location-level certificates), and
	// its location-scoped operation polls are owned by the shared LRO poller.
	PrivateCA privatecadriver.PrivateCA
	// GKEBackup serves the gkebackup.googleapis.com v1 backup-plan and
	// restore-plan control plane against the gkebackup driver. Its paths live
	// under /v1/projects/{p}/locations/{l}/{backupPlans|restorePlans}[/…]; the
	// handler's Matches narrows on those resource segments, so it is disjoint from
	// every other /v1/projects/ handler, and its location-scoped operation polls
	// are owned by the shared LRO poller.
	GKEBackup gkebackupdriver.GKEBackup
	// GKEHub serves the gkehub.googleapis.com v1 membership, feature, and fleet
	// control plane against the gkehub driver. Its paths live under
	// /v1/projects/{p}/locations/{l}/{memberships|features|fleets}[/…]; the
	// handler's Matches narrows on those resource segments, so it is disjoint from
	// every other /v1/projects/ handler, and its location-scoped operation polls
	// are owned by the shared LRO poller.
	GKEHub gkehubdriver.GKEHub
	// DataFusion serves the datafusion.googleapis.com v1 instance control plane
	// against the datafusion driver. Its paths live under /v1/projects/{p}/
	// locations/{l}/instances[/{i}[:restart]] — the same grammar Memorystore and
	// Filestore share — so the handler's Matches claims only genuinely-Data-Fusion
	// traffic (a create body with a `type`, the :restart verb, or an item/list it
	// owns), letting Redis/Filestore requests fall through. Its location-scoped
	// operation polls are owned by the shared LRO poller.
	DataFusion datafusiondriver.DataFusion
	// Dataplex serves the dataplex.googleapis.com v1 lake → zone → asset control
	// plane against the dataplex driver. Its paths live under /v1/projects/{p}/
	// locations/{l}/lakes[/{lake}/zones[/{zone}/assets[/…]]]; the handler's Matches
	// narrows on the lakes resource segment, so it is disjoint from every other
	// /v1/projects/ handler, and its location-scoped operation polls are owned by
	// the shared LRO poller.
	Dataplex dataplexdriver.Dataplex
	// Metastore serves the metastore.googleapis.com v1 Dataproc Metastore service
	// control plane against the metastore driver. Its paths live under
	// /v1/projects/{p}/locations/{l}/services[/…]; the handler's Matches narrows on
	// the services resource segment, so it is disjoint from every other
	// /v1/projects/ handler, and its location-scoped operation polls are owned by
	// the shared LRO poller.
	Metastore metastoredriver.Metastore
	// VPCAccess serves the vpcaccess.googleapis.com v1 Serverless VPC Access
	// connector control plane against the vpcaccess driver. Its paths live under
	// /v1/projects/{p}/locations/{l}/connectors[/…]; the handler's Matches narrows
	// on the connectors resource segment, so it is disjoint from every other
	// /v1/projects/ handler, and its location-scoped operation polls are owned by
	// the shared LRO poller.
	VPCAccess vpcaccessdriver.VPCAccess
	// CloudIDS serves the ids.googleapis.com v1 Cloud IDS endpoint control plane
	// against the cloudids driver. Its paths live under
	// /v1/projects/{p}/locations/{l}/endpoints[/…]; the handler's Matches narrows
	// on the endpoints resource segment, so it is disjoint from every other
	// /v1/projects/ handler, and its location-scoped operation polls are owned by
	// the shared LRO poller.
	CloudIDS cloudidsdriver.CloudIDs
	// SecureSourceManager serves the securesourcemanager.googleapis.com v1
	// instance + repository control plane against the securesourcemanager driver.
	// Its paths live under /v1/projects/{p}/locations/{l}/{instances|repositories}
	// [/…]. Both collections collide on the identical path with greedy
	// fall-through services (instances with Filestore/Memorystore, repositories
	// with Artifact Registry), so its Matches claims each selectively by content
	// (create) and ownership (item/list). Its location-scoped operation polls are
	// owned by the shared LRO poller.
	SecureSourceManager securesourcemanagerdriver.SecureSourceManager
	// NetworkConnectivity serves the networkconnectivity.googleapis.com v1 hub +
	// spoke control plane against the networkconnectivity driver. Its paths live
	// under /v1/projects/{p}/locations/{l}/{hubs|spokes}[/…] (hubs are global,
	// spokes regional); the handler's Matches narrows on those resource segments,
	// so it is disjoint from every other /v1/projects/ handler, and its
	// location-scoped operation polls are owned by the shared LRO poller.
	NetworkConnectivity nccdriver.NetworkConnectivity
	// Composer serves the composer.googleapis.com v1 environment control plane
	// against the composer driver. Its paths live under /v1/projects/{p}/
	// locations/{l}/environments[/…]; the handler's Matches narrows on the
	// environments resource segment, so it is disjoint from every other
	// /v1/projects/ handler, and its location-scoped operation polls are owned by
	// the shared LRO poller.
	Composer composerdriver.Composer
	// CloudDeploy serves the clouddeploy.googleapis.com v1 delivery-pipeline and
	// target control plane against the clouddeploy driver. Its paths live under
	// /v1/projects/{p}/locations/{l}/{deliveryPipelines|targets}[/…]; the
	// handler's Matches narrows on those resource segments, so it is disjoint from
	// every other /v1/projects/ handler, and its location-scoped operation polls
	// are owned by the shared LRO poller.
	CloudDeploy clouddeploydriver.CloudDeploy
	// Workflows serves the workflows.googleapis.com v1 control plane against the
	// workflows driver. Its paths live under
	// /v1/projects/{p}/locations/{l}/workflows[/…]; the handler's Matches narrows
	// on that resource segment, so it is disjoint from every other /v1/projects/
	// handler, and its location-scoped operation polls are owned by the shared
	// LRO poller.
	Workflows workflowsdriver.Workflows
	// ServiceDirectory serves the servicedirectory.googleapis.com v1 REST API
	// (namespaces → services → endpoints) against the servicedirectory driver.
	// Its paths live under /v1/projects/{p}/locations/{l}/namespaces[/…]; the
	// handler's Matches narrows on the namespaces resource segment, so it is
	// disjoint from every other /v1/projects/ handler. CRUD is synchronous REST
	// (no long-running operations).
	ServiceDirectory sddriver.ServiceDirectory
	// DataCatalog serves the datacatalog.googleapis.com v1 REST API (entry groups
	// → entries → tags, and tag templates → fields) against the datacatalog
	// driver. Its paths live under /v1/projects/{p}/locations/{l}/{entryGroups|
	// tagTemplates}[/…]; the handler's Matches narrows on those resource segments,
	// so it is disjoint from every other /v1/projects/ handler. CRUD is
	// synchronous REST (no long-running operations).
	DataCatalog dcdriver.DataCatalog
	// Dataform serves the dataform.googleapis.com/v1beta1 REST API (region-scoped
	// repositories) against the dataform driver. Dataform ships a v1beta1 API only,
	// so the google-beta google_dataform_repository resource and SDK clients target
	// /v1beta1/. The handler's Matches narrows on the repositories resource segment,
	// so it is disjoint from every other /v1beta1/projects/ handler. CRUD is
	// synchronous REST (no long-running operations).
	Dataform dataformdriver.Dataform
	// APIGateway serves the apigateway.googleapis.com control plane (apis, their
	// api configs, and gateways) against the apigateway driver. Its resources
	// exist only in the terraform-provider-google-beta provider, whose default
	// base path is /v1beta/; the handler serves both /v1beta/ and /v1/. Its paths
	// live under /{v}/projects/{p}/locations/{l}/{apis|gateways}[/…]; the handler's
	// Matches narrows on those resource segments, so it is disjoint from every
	// other /v1/projects/ handler. The google-beta provider polls operations at
	// its /v1beta/ base path — a space the shared LRO poller does not own — so the
	// handler serves its own /v1beta/ operation polls and yields the /v1/ ones to
	// the shared poller.
	APIGateway       agdriver.APIGateway
	VertexAI         vertexaidriver.VertexAI
	IAM              iamdriver.IAM
	ArtifactRegistry crdriver.ContainerRegistry
	// CloudDNS serves the dns.googleapis.com v1 REST API against the dns
	// driver.
	CloudDNS dnsdriver.DNS
	// LB serves the Cloud Load Balancing REST API (backendServices +
	// forwardingRules on the compute API) against the loadbalancer driver.
	LB lbdriver.LoadBalancer
	// CloudLogging serves the logging.googleapis.com v2 REST API
	// (entries:write/list, logs list/delete) against the logging driver.
	CloudLogging logdriver.Logging
	// SecretManager serves the secretmanager.googleapis.com v1 REST API
	// against the secrets driver.
	SecretManager secretsdriver.Secrets
	// Eventarc serves the eventarc.googleapis.com v1 REST API against the
	// eventbus driver, mapping triggers to rules under a per-location bus.
	Eventarc ebdriver.EventBus
	// Memorystore serves the redis.googleapis.com v1 REST API against the cache
	// driver's instance control plane.
	Memorystore cachedriver.Cache
	// Scheduler serves the cloudscheduler.googleapis.com v1 REST API (job
	// control plane) against the scheduler driver.
	Scheduler scheddriver.Scheduler
	// CloudTasks serves the cloudtasks.googleapis.com v2 REST API (queue control
	// plane) against the cloudtasks driver.
	CloudTasks ctdriver.Queues
	// BinaryAuthorization serves the binaryauthorization.googleapis.com v1 REST
	// API (the per-project policy singleton + attestor control plane) against the
	// binaryauthorization driver.
	BinaryAuthorization badriver.BinaryAuthorization
	// FCM serves the fcm.googleapis.com v1 messages:send API against the
	// notification driver (Publish only; FCM has no topic/subscription CRUD).
	FCM notifdriver.Notification
	// K8sAPI is the shared in-memory Kubernetes data-plane API server. It is
	// shared with awsserver.Drivers.K8sAPI and azureserver.Drivers.K8sAPI so a
	// kubeconfig issued by any provider's control plane (EKS/AKS/GKE) reaches
	// the same backend. Leave nil to disable Kubernetes data-plane support.
	K8sAPI *kubernetes.APIServer
	// ResourceDiscovery is the cross-service inventory engine. Required to
	// serve Cloud Asset Inventory (cloudasset/v1) requests. Leave nil to
	// omit the handler. ProjectID is used for feed-name validation; if
	// empty the engine's own AccountID (GCP project ID for GCP engines)
	// is used as the fallback.
	ResourceDiscovery *resourcediscovery.Engine
	ProjectID         string
	// Clock drives time-stamped request observers (the Cloud Audit Log
	// recorder). Leave nil to use the real clock; set it to the provider's clock
	// so a FakeClock makes audit timestamps deterministic in tests.
	Clock config.Clock
}

// New returns a server that speaks GCP's REST JSON wire protocol for every
// non-nil driver in d.
//
// GCS's Matches() also accepts /{bucket}/{object} for direct-media downloads,
// which is broad enough to swallow Firestore and Cloud Monitoring traffic if
// it registers first. Register more-specific handlers (compute, networks,
// firestore, monitoring) ahead of GCS so first-match-wins keeps each on the
// correct package.
//
//nolint:gocritic,gocyclo,gocognit,funlen // Drivers is all interface fields; one if-per-driver, grows with the bundle.
func New(d Drivers) *server.Server {
	// AlloyDB and GKE claim the same /v1/projects/{p}/locations/{l}/clusters
	// paths, so enabling both would silently shadow one. Fail fast rather than
	// route ambiguously — use DriversFromWithAlloyDB to enable AlloyDB in place
	// of GKE.
	if d.AlloyDB != nil && d.GKE != nil {
		panic("gcp server: AlloyDB and GKE share REST paths and cannot both be enabled; " +
			"use DriversFromWithAlloyDB to enable AlloyDB in place of GKE")
	}

	srv := server.New()

	// GKE registers ahead of the shared LRO poller because it answers a richer
	// operation shape (operationType/targetLink/selfLink/zone/timestamps) for
	// its OWN operations. Its Matches claims a named operation poll only when
	// the op was recorded by the GKE mock, so foreign location operations still
	// fall through to lro below — no shadowing.
	if d.GKE != nil {
		gkeH := gke.New(d.GKE)
		// Wire the compute-side MIG registrar (the GCE Mock) so node-pool
		// lifecycle keeps a backing instanceGroupManager's targetSize == node
		// count, letting the Terraform provider resolve node_count and stop the
		// 0→N drift. Nil compute driver leaves node pools without MIG URLs.
		if reg, ok := d.Compute.(gke.InstanceGroupManagerRegistrar); ok {
			gkeH.SetInstanceGroupManagers(reg)
		}

		srv.Register(gkeH)
	}

	// Shared location-operations poller. Registered ahead of the remaining
	// service handlers so it owns every GET /v1/projects/{p}/locations/{l}/
	// operations/{op} uniformly, instead of alloydb greedily claiming (and
	// 404ing) operations created by artifactregistry, eventarc, memorystore,
	// etc. Each service registers the operations it creates into opsReg so the
	// poller replays their typed response and 404s an operation name that was
	// never created (as real GCP does).
	opsReg := lro.NewRegistry()
	srv.Register(lro.New(opsReg))

	// Shared compute-operation registry. The compute handler's /operations route
	// serves every compute#operation poll (its own, plus the networks and load-
	// balancing handlers', which mint compute operations but have no operations
	// route of their own). Sharing one registry lets an Insert/Delete poll resolve
	// a real operation and 404 a name that was never issued, uniformly across the
	// three handlers.
	computeOps := gcprest.NewOperationRegistry()

	if d.Compute != nil {
		// d.Networking (may be nil) lets insert allocate the instance's private
		// networkIP from the referenced subnetwork's CIDR.
		computeH := compute.New(d.Compute, d.Networking)
		computeH.SetOperationRegistry(computeOps)
		srv.Register(computeH)
	}

	if d.Networking != nil {
		// d.Compute (may be nil) lets the subnetwork delete guard reject removing a
		// subnet that still has instances.
		netH := vpc.New(d.Networking, d.Compute)
		netH.SetOperationRegistry(computeOps)
		srv.Register(netH)
	}

	// Service Networking has no driver: a private-services connection is a
	// record, and nothing in the emulator routes the peering it stands for.
	srv.Register(servicenetworking.New())

	// Cloud Load Balancing shares the /compute/v1/projects/… URL space with the
	// compute and networks handlers above but claims a disjoint set of resource
	// types — backendServices / forwardingRules — whereas compute claims
	// instances / operations / disks / snapshots / images and networks claims
	// networks / subnetworks / firewalls. gcprest.ParsePath keys dispatch on the
	// resource-type segment, so first-match-wins routing is unambiguous and
	// registration order relative to those two is unconstrained. Mutating LB ops
	// return operation envelopes the SDK polls via the compute handler's
	// /global/operations route.
	if d.LB != nil {
		lbH := lbsrv.New(d.LB)
		lbH.SetOperationRegistry(computeOps)
		srv.Register(lbH)
	}

	// Compute-space catch-all. Registered AFTER the compute, networks and load-
	// balancing handlers so first-match-wins keeps every implemented /compute/v1
	// path on its real handler; this only claims the leftovers, answering with a
	// GCP JSON error envelope instead of the dispatcher's bare-text 501.
	if d.Compute != nil || d.Networking != nil || d.LB != nil {
		srv.Register(compute.NewFallback())
	}

	// CloudFunctions matches /v1/projects/{p}/locations/{l}/functions paths
	// before Firestore so the locations+functions guard wins over Firestore's
	// /v1/projects/ prefix match.
	var cfHandler *cloudfunctions.Handler

	if d.CloudFunctions != nil {
		var cfOpts []cloudfunctions.Option
		if d.Storage != nil {
			// Let create() fetch a sourceArchiveUrl (gs://...) deployment package
			// from the in-process GCS backend so real code runs instead of the
			// echo stub.
			cfOpts = append(cfOpts, cloudfunctions.WithObjectStore(d.Storage))
		}

		cfHandler = cloudfunctions.New(d.CloudFunctions, cfOpts...)
		srv.Register(cfHandler)
	}

	// Cloud Run matches /v2/projects/{p}/locations/{l}/{jobs|operations}[/…].
	// Its locations+jobs guard keeps it disjoint from Cloud Logging's
	// /v2/projects/{p}/logs paths, so registration order between the two is
	// unconstrained; registered here alongside the other /v2 handlers.
	if d.CloudRun != nil {
		srv.Register(cloudrunsrv.New(d.CloudRun))
	}

	// PubSub matches /v1/projects/{p}/{topics|subscriptions}/...; register
	// before Firestore so its more-specific resource-type guard wins over
	// Firestore's permissive /v1/projects/ prefix.
	var pubsubHandler *pubsub.Handler

	if d.PubSub != nil {
		pubsubHandler = pubsub.New(d.PubSub)
		// PubSub -> Cloud Functions: a publish invokes every function whose
		// eventTrigger targets the topic (gen1 resource / gen2 pubsubTopic),
		// mirroring the AWS S3/DynamoDB -> Lambda event-delivery wiring. Push
		// subscription HTTP delivery is self-contained in the PubSub handler.
		if cfHandler != nil {
			pubsubHandler.SetFunctionInvoker(cfHandler)
		}

		// Monitoring -> PubSub: an alert-policy breach publishes the incident to
		// each pubsub notification channel's topic. Topic fanout is wire-only, so
		// the publisher is wired here (not providers/gcp/gcp.go) as an adapter over
		// the PubSub handler — the same layer #803 wired the function-invoker at.
		if setter, ok := d.Monitoring.(interface {
			SetPubSubPublisher(gcpmon.PubSubPublisher)
		}); ok {
			setter.SetPubSubPublisher(monitoringPubSubAdapter{h: pubsubHandler})
		}

		srv.Register(pubsubHandler)
	}

	// Cloud SQL matches /v1/projects/{p}/{instances|operations}/...; same
	// /v1/projects/ space as Firestore, so register first.
	if d.Bigtable != nil {
		srv.Register(bigtableserver.New(d.Bigtable))
	}

	// BigQuery matches /bigquery/v2/projects/{p}/datasets[...] — its own
	// /bigquery/v2/ URL space, disjoint from the /v1/projects/ family,
	// /compute/v1/, and /dns/v1/, so registration order relative to every other
	// handler is unconstrained.
	if d.BigQuery != nil {
		srv.Register(bigqueryserver.New(d.BigQuery))
	}

	// Spanner shares the /v1/projects/{p}/instances URL space with Cloud SQL, so
	// it must register BEFORE Cloud SQL: its Matches claims only genuinely-Spanner
	// traffic (create-by-body, item/sub-resource-by-instance-ownership, and the
	// instance list), letting every other /v1/projects/{p}/instances request fall
	// through to Cloud SQL below.
	if d.Spanner != nil {
		srv.Register(spannersrv.New(d.Spanner))
	}

	if d.CloudSQL != nil {
		srv.Register(cloudsql.New(d.CloudSQL))
	}

	// Dataproc matches /v1/projects/{p}/regions/{r}/{clusters|operations}[/…]. Its
	// Matches narrows on the "regions" keyword and the clusters/operations
	// resource segment, so it is disjoint from every other /v1/projects/ handler
	// and must simply register before Firestore's permissive /v1/projects/ prefix.
	// It serves its own region-scoped operation polls (the shared LRO poller owns
	// only the /locations/ space), and Compute's regional paths are under the
	// separate /compute/v1/ prefix, so the two never collide.
	if d.Dataproc != nil {
		srv.Register(dataprocsrv.New(d.Dataproc))
	}

	// Composer matches /v1/projects/{p}/locations/{l}/environments[/…]. Its
	// environments resource-type guard is disjoint from every other /v1/projects/
	// handler (Memorystore/Filestore's instances, GKE's clusters, Cloud
	// Functions' functions, Eventarc's triggers, …), so registration order among
	// them is unconstrained; registered before Firestore's permissive prefix.
	// Its location-scoped operation polls are owned by the shared LRO poller
	// (registered above), which the handler's Matches yields to.
	if d.Composer != nil {
		composerH := composersrv.New(d.Composer)
		composerH.SetOperationRegistry(opsReg)
		srv.Register(composerH)
	}

	// Cloud Deploy matches /v1/projects/{p}/locations/{l}/{deliveryPipelines|
	// targets}[/…]. Its resource-segment guard is disjoint from every other
	// /v1/projects/ handler (Composer's environments, Memorystore/Filestore's
	// instances, GKE's clusters, Scheduler's jobs, …), so registration order among
	// them is unconstrained; registered before Firestore's permissive prefix. Its
	// location-scoped operation polls are owned by the shared LRO poller, which the
	// handler's Matches yields to.
	if d.CloudDeploy != nil {
		clouddeployH := clouddeploysrv.New(d.CloudDeploy)
		clouddeployH.SetOperationRegistry(opsReg)
		srv.Register(clouddeployH)
	}

	// Datastream matches /v1/projects/{p}/locations/{l}/{connectionProfiles|
	// streams}[/…]. Its resource-segment guard is disjoint from every other
	// /v1/projects/ handler (Composer's environments, Cloud Deploy's pipelines/
	// targets, Scheduler's jobs, …), so registration order among them is
	// unconstrained; registered before Firestore's permissive prefix. Its
	// location-scoped operation polls are owned by the shared LRO poller, which the
	// handler's Matches yields to.
	if d.Datastream != nil {
		datastreamH := datastreamsrv.New(d.Datastream)
		datastreamH.SetOperationRegistry(opsReg)
		srv.Register(datastreamH)
	}

	// CertificateManager matches /v1/projects/{p}/locations/{l}/{certificates|
	// certificateMaps|dnsAuthorizations}[/…]. Its resource-segment guard is
	// disjoint from every other /v1/projects/ handler (Composer's environments,
	// Cloud Deploy's pipelines/targets, Datastream's connectionProfiles/streams,
	// …), so registration order among them is unconstrained; registered before
	// Firestore's permissive prefix. Its location-scoped operation polls are owned
	// by the shared LRO poller, which the handler's Matches yields to.
	if d.CertificateManager != nil {
		certmanagerH := certmanagersrv.New(d.CertificateManager)
		certmanagerH.SetOperationRegistry(opsReg)
		srv.Register(certmanagerH)
	}

	// PrivateCA matches /v1/projects/{p}/locations/{l}/{caPools|certificateTemplates|
	// operations}[/…], including the certificateAuthorities and certificates
	// collections nested under a caPool. Its resource-segment guard is disjoint from
	// every other /v1/projects/ handler — notably certificatemanager, whose
	// certificates live at the location level, not nested under a caPool — so
	// registration order among them is unconstrained; registered before Firestore's
	// permissive prefix. Its location-scoped operation polls are owned by the shared
	// LRO poller, which the handler's Matches yields to.
	if d.PrivateCA != nil {
		privatecaH := privatecasrv.New(d.PrivateCA)
		privatecaH.SetOperationRegistry(opsReg)
		srv.Register(privatecaH)
	}

	// GKEBackup matches /v1/projects/{p}/locations/{l}/{backupPlans|restorePlans}
	// [/…]. Its resource-segment guard is disjoint from every other /v1/projects/
	// handler (Composer's environments, Cloud Deploy's pipelines/targets,
	// Certificate Manager's certificates, Datastream's streams, …), so
	// registration order among them is unconstrained; registered before
	// Firestore's permissive prefix. Its location-scoped operation polls are owned
	// by the shared LRO poller, which the handler's Matches yields to.
	if d.GKEBackup != nil {
		gkebackupH := gkebackupsrv.New(d.GKEBackup)
		gkebackupH.SetOperationRegistry(opsReg)
		srv.Register(gkebackupH)
	}

	// GKEHub matches /v1/projects/{p}/locations/{l}/{memberships|features|fleets}
	// [/…]. Its resource-segment guard is disjoint from every other /v1/projects/
	// handler, so registration order among them is unconstrained; registered
	// before Firestore's permissive prefix. Its location-scoped operation polls
	// are owned by the shared LRO poller, which the handler's Matches yields to.
	if d.GKEHub != nil {
		gkehubH := gkehubsrv.New(d.GKEHub)
		gkehubH.SetOperationRegistry(opsReg)
		srv.Register(gkehubH)
	}

	// Dataplex matches /v1/projects/{p}/locations/{l}/lakes[/{lake}/zones[/{zone}/
	// assets[/…]]]. Its lakes resource-segment guard is disjoint from every other
	// /v1/projects/ handler (Composer's environments, Cloud Deploy's pipelines,
	// Datastream's streams, Certificate Manager's certificates, Metastore's
	// services, …), so registration order among them is unconstrained; registered
	// before Firestore's permissive prefix. Its location-scoped operation polls are
	// owned by the shared LRO poller, which the handler's Matches yields to.
	if d.Dataplex != nil {
		dataplexH := dataplexsrv.New(d.Dataplex)
		dataplexH.SetOperationRegistry(opsReg)
		srv.Register(dataplexH)
	}

	// Metastore matches /v1/projects/{p}/locations/{l}/services[/…]. Its services
	// resource-segment guard is disjoint from every other /v1/projects/ handler
	// (Composer's environments, Cloud Deploy's pipelines/targets, Datastream's
	// connectionProfiles/streams, Certificate Manager's certificates, VPC Access's
	// connectors, …), so registration order among them is unconstrained; registered
	// before Firestore's permissive prefix. Its location-scoped operation polls are
	// owned by the shared LRO poller, which the handler's Matches yields to.
	if d.Metastore != nil {
		metastoreH := metastoresrv.New(d.Metastore)
		metastoreH.SetOperationRegistry(opsReg)
		srv.Register(metastoreH)
	}

	// VPCAccess matches /v1/projects/{p}/locations/{l}/connectors[/…]. Its
	// connectors resource-segment guard is disjoint from every other
	// /v1/projects/ handler (Composer's environments, Cloud Deploy's pipelines/
	// targets, Datastream's connectionProfiles/streams, Certificate Manager's
	// certificates, …), so registration order among them is unconstrained;
	// registered before Firestore's permissive prefix. Its location-scoped
	// operation polls are owned by the shared LRO poller, which the handler's
	// Matches yields to.
	if d.VPCAccess != nil {
		vpcaccessH := vpcaccesssrv.New(d.VPCAccess)
		vpcaccessH.SetOperationRegistry(opsReg)
		srv.Register(vpcaccessH)
	}

	// CloudIDS matches /v1/projects/{p}/locations/{l}/endpoints[/…]. Its endpoints
	// resource-segment guard is disjoint from every other /v1/projects/ handler
	// (Composer's environments, Cloud Deploy's pipelines/targets, Datastream's
	// connectionProfiles/streams, Certificate Manager's certificates, VPC Access's
	// connectors, …), so registration order among them is unconstrained; registered
	// before Firestore's permissive prefix. Its location-scoped operation polls are
	// owned by the shared LRO poller, which the handler's Matches yields to.
	if d.CloudIDS != nil {
		cloudidsH := cloudidssrv.New(d.CloudIDS)
		cloudidsH.SetOperationRegistry(opsReg)
		srv.Register(cloudidsH)
	}

	// Data Fusion (datafusion.googleapis.com) shares the EXACT same instances path
	// grammar as Secure Source Manager (below), Memorystore, and Filestore. It
	// registers BEFORE all of them so its narrow Matches wins: it claims only
	// genuinely-Data-Fusion traffic — a create body carrying a `type`
	// (BASIC/ENTERPRISE/DEVELOPER, which the sibling instance bodies lack), the
	// Data-Fusion-only :restart verb, or an item/list it owns — letting Secure
	// Source Manager / Redis / Filestore requests fall through. Its location-
	// scoped operation polls are owned by the shared LRO poller, which the
	// handler's Matches yields to.
	if d.DataFusion != nil {
		datafusionH := datafusionsrv.New(d.DataFusion)
		datafusionH.SetOperationRegistry(opsReg)
		srv.Register(datafusionH)
	}

	// SecureSourceManager matches /v1/projects/{p}/locations/{l}/{instances|
	// repositories}[/…]. Both collections collide on the identical path with
	// greedy fall-through services registered AFTER it (instances with Filestore/
	// Memorystore, repositories with Artifact Registry), so its Matches claims
	// each only for genuinely-Secure-Source-Manager traffic — a create body with
	// no Filestore/Redis signal / carrying the required repository `instance`
	// reference, and item/list only for resources it owns — letting the sibling
	// services' requests fall through. This is the same content/ownership pattern
	// Filestore uses; being registered first is therefore safe. Its
	// location-scoped operation polls are owned by the shared LRO poller, which
	// the handler's Matches yields to.
	if d.SecureSourceManager != nil {
		ssmH := securesourcemanagersrv.New(d.SecureSourceManager)
		ssmH.SetOperationRegistry(opsReg)
		srv.Register(ssmH)
	}

	// NetworkConnectivity matches /v1/projects/{p}/locations/{l}/{hubs|spokes}
	// [/…] (hubs global, spokes regional). Its resource-segment guard is disjoint
	// from every other /v1/projects/ handler (Composer's environments, Cloud
	// Deploy's pipelines/targets, Datastream's connectionProfiles/streams, …), so
	// registration order among them is unconstrained; registered before
	// Firestore's permissive prefix. Its location-scoped operation polls are owned
	// by the shared LRO poller, which the handler's Matches yields to.
	if d.NetworkConnectivity != nil {
		nccH := networkconnectivitysrv.New(d.NetworkConnectivity)
		nccH.SetOperationRegistry(opsReg)
		srv.Register(nccH)
	}

	// Workflows matches /v1/projects/{p}/locations/{l}/workflows[/…]. Its
	// workflows resource-segment guard is disjoint from every other /v1/projects/
	// handler (Composer's environments, Cloud Deploy's pipelines/targets,
	// Scheduler's jobs, …), so registration order among them is unconstrained;
	// registered before Firestore's permissive prefix. Its location-scoped
	// operation polls are owned by the shared LRO poller, which the handler's
	// Matches yields to.
	if d.Workflows != nil {
		workflowsH := workflowssrv.New(d.Workflows)
		workflowsH.SetOperationRegistry(opsReg)
		srv.Register(workflowsH)
	}

	// ServiceDirectory matches /v1/projects/{p}/locations/{l}/namespaces[/…]. Its
	// namespaces resource-segment guard is disjoint from every other
	// /v1/projects/ handler, so registration order among them is unconstrained;
	// registered before Firestore's permissive prefix. CRUD is synchronous REST —
	// no operation registry is wired.
	if d.ServiceDirectory != nil {
		srv.Register(servicedirectorysrv.New(d.ServiceDirectory))
	}

	// DataCatalog matches /v1/projects/{p}/locations/{l}/{entryGroups|tagTemplates}
	// [/…]. Its entryGroups/tagTemplates resource-segment guard is disjoint from
	// every other /v1/projects/ handler, so registration order among them is
	// unconstrained; registered before Firestore's permissive prefix. CRUD is
	// synchronous REST — no operation registry is wired.
	if d.DataCatalog != nil {
		srv.Register(datacatalogsrv.New(d.DataCatalog))
	}

	// Dataform matches /v1beta1/projects/{p}/locations/{l}/repositories[/…]. Its
	// repositories resource-segment guard is disjoint from every other
	// /v1beta1/projects/ handler, so registration order among them is
	// unconstrained; registered before Firestore's permissive prefix. CRUD is
	// synchronous REST — no operation registry is wired.
	if d.Dataform != nil {
		srv.Register(dataformsrv.New(d.Dataform))
	}

	// API Gateway matches /{v1beta,v1}/projects/{p}/locations/{l}/{apis|gateways}
	// [/…]. Its apis/gateways resource-segment guard is disjoint from every other
	// /v1/projects/ handler, so registration order among them is unconstrained;
	// registered before Firestore's permissive prefix. It serves its own /v1beta/
	// operation polls (the shared LRO poller, registered above, owns only the /v1/
	// operations space, which the handler yields to it); the handler still
	// registers created operations into the shared poller so a /v1/ SDK poll
	// resolves their typed response.
	if d.APIGateway != nil {
		apigatewayH := apigatewaysrv.New(d.APIGateway)
		apigatewayH.SetOperationRegistry(opsReg)
		srv.Register(apigatewayH)
	}

	// AlloyDB matches /v1/projects/{p}/locations/{l}/{clusters|backups|
	// operations}/... — the cluster/operations paths are identical to GKE's, so
	// the two are mutually exclusive on one server. Registered before GKE so an
	// AlloyDB-configured server (GKE nil) works; DriversFrom leaves AlloyDB nil.
	if d.AlloyDB != nil {
		alloyH := alloydbsrv.New(d.AlloyDB)
		alloyH.SetOperationRegistry(opsReg)
		srv.Register(alloyH)
	}

	// GKE is registered ahead of the LRO poller above (see the top of New) so
	// its richer operation shape wins for its own operations.

	// Vertex AI matches /v1/projects/{p}/locations/{l}/{models|endpoints|datasets|
	// customJobs|batchPredictionJobs}/... and /v1/publishers/...:generateContent.
	// Disjoint from GKE/functions/instances; registered before Firestore's
	// permissive /v1/projects/ prefix.
	if d.VertexAI != nil {
		srv.Register(vertexaisrv.New(d.VertexAI))
	}

	// Cloud Asset Inventory matches /v1/{scope}:method and /v1/{parent}/
	// {assets,feeds} paths. Register before Firestore: Firestore's Matches
	// is /v1/projects/ broadly, which would otherwise swallow the colon-
	// suffix custom methods that share the same prefix.
	if d.ResourceDiscovery != nil {
		srv.Register(cloudasset.New(d.ResourceDiscovery, d.ProjectID))
	}

	// IAM matches /v1/projects/{p}/{serviceAccounts|roles}[/…] — its
	// resource-type guard is disjoint from Firestore (which serves
	// /v1/projects/{p}/databases/…) and from CloudFunctions / PubSub /
	// CloudSQL / GKE / CloudAsset, so registration order is unconstrained
	// among the /v1/projects/ family. Registered before Firestore for
	// consistency with the pattern above.
	if d.IAM != nil {
		srv.Register(iam.New(d.IAM))
	}

	// Artifact Registry matches /v1/projects/{p}/locations/{l}/repositories[/…]
	// — disjoint from IAM (serviceAccounts|roles) and Cloud Asset. Registered
	// among the /v1/projects/ family, before Firestore's catch-all.
	if d.ArtifactRegistry != nil {
		arH := artifactregistry.New(d.ArtifactRegistry)
		arH.SetOperationRegistry(opsReg)
		srv.Register(arH)
	}

	// Secret Manager matches /v1/projects/{p}/secrets[/…] — disjoint from IAM
	// (serviceAccounts|roles), Artifact Registry (locations/…), and the rest
	// of the /v1/projects/ family. Registered before Firestore's catch-all.
	if d.SecretManager != nil {
		srv.Register(secretmanagersrv.New(d.SecretManager))
	}

	// Eventarc matches /v1/projects/{p}/locations/{l}/triggers[/…] — a
	// resource-type guard disjoint from IAM, Artifact Registry, Secret Manager,
	// GKE, and the rest of the /v1/projects/ family. Registered before
	// Firestore's catch-all.
	if d.Eventarc != nil {
		eaH := eventarc.New(d.Eventarc)
		eaH.SetOperationRegistry(opsReg)

		// Destination validation: a trigger create/patch is rejected when it
		// names a Cloud Function / Cloud Run service that doesn't exist,
		// mirroring real Eventarc's admission check. Only wired when the peer
		// service is present, so a server that omits one keeps accepting that
		// destination kind unchecked.
		if d.CloudFunctions != nil {
			eaH.SetFunctionResolver(d.CloudFunctions)
		}

		if d.CloudRun != nil {
			eaH.SetCloudRunResolver(d.CloudRun)
		}

		srv.Register(eaH)
	}

	// Cloud DNS matches /dns/v1/projects/{p}/managedZones[...] — a distinct
	// URL space from the /v1/projects/ family, so registration order is
	// unconstrained relative to Firestore and the rest. Registered before the
	// GCS fallback for consistency with the other handlers.
	if d.CloudDNS != nil {
		srv.Register(clouddns.New(d.CloudDNS))
	}

	// Cloud Logging matches /v2/entries:{write,list} and /v2/projects/{p}/logs
	// — the logging.googleapis.com v2 URL space, disjoint from the /v1/projects/
	// family, /compute/v1/, and /dns/v1/, so registration order relative to them
	// is unconstrained. Registered before the GCS fallback for consistency.
	if d.CloudLogging != nil {
		srv.Register(cloudloggingsrv.New(d.CloudLogging))
	}

	// Filestore (file.googleapis.com) shares the EXACT same path grammar as
	// Memorystore — /v1/projects/{p}/locations/{l}/instances[/{i}] — on a
	// different real host, and a custom-endpoint client sends the emulator's own
	// Host, so the two cannot be told apart by URL or Host. Filestore registers
	// BEFORE Memorystore and its Matches claims only genuinely-Filestore traffic
	// (a create body carrying fileShares/networks, or an item/list this store
	// owns), letting every Memorystore request fall through — the Spanner/Cloud
	// SQL content+ownership pattern. It has no portable driver (the emulator
	// models no NFS data plane); like Cloud KMS the handler owns its own store,
	// so it is always registered. d.Clock (may be nil) makes createTime
	// deterministic under a FakeClock. Registered before Firestore's permissive
	// /v1/projects/ prefix.
	filestoreH := filestoresrv.New(d.Clock)
	filestoreH.SetOperationRegistry(opsReg)
	srv.Register(filestoreH)

	// Memorystore matches /v1/projects/{p}/locations/{l}/{instances|operations}
	// — its resource-type guard is disjoint from GKE (clusters), Cloud Functions
	// (functions), Vertex AI, and the rest of the /v1/projects/ family, so
	// registration order among them is unconstrained. Registered before
	// Firestore's permissive /v1/projects/ prefix so its paths aren't swallowed.
	// Filestore (above) shares this exact instances path and is registered ahead
	// of it; its selective Matches lets Memorystore traffic fall through here.
	if d.Memorystore != nil {
		msH := memorystoresrv.New(d.Memorystore)
		msH.SetOperationRegistry(opsReg)
		srv.Register(msH)
	}

	// Cloud Scheduler matches /v1/projects/{p}/locations/{l}/jobs[/…] — its jobs
	// resource-type guard is disjoint from Memorystore (instances|operations),
	// Eventarc (triggers), GKE (clusters), and the rest of the /v1/projects/
	// family; Cloud Run's jobs are under the /v2/ prefix. All eight methods are
	// synchronous (no LRO). Registered before Firestore's permissive prefix.
	if d.Scheduler != nil {
		srv.Register(schedulersrv.New(d.Scheduler))
	}

	// Cloud Tasks matches /v2/projects/{p}/locations/{l}/queues[/…] — its queues
	// resource-type guard on the /v2/ prefix keeps it disjoint from Cloud Run
	// (jobs|services, also /v2/) and from the entire /v1/projects/ family
	// (including Firestore's permissive prefix), so registration order is
	// unconstrained. All eleven methods are synchronous (no LRO).
	if d.CloudTasks != nil {
		srv.Register(cloudtaskssrv.New(d.CloudTasks))
	}

	// Binary Authorization matches /v1/projects/{p}/policy and
	// /v1/projects/{p}/attestors[/…] — its policy|attestors resource-type guard
	// keeps it disjoint from every other /v1/projects/ handler, and it never
	// claims operations. All ten methods are synchronous (no LRO). Registered
	// before Firestore's permissive /v1/projects/ prefix.
	if d.BinaryAuthorization != nil {
		srv.Register(binauthzsrv.New(d.BinaryAuthorization))
	}

	// FCM matches /v1/projects/{p}/messages:send — disjoint from every other
	// /v1/projects/ handler (none use the messages:send suffix). Registered
	// before Firestore's permissive /v1/projects/ prefix match.
	if d.FCM != nil {
		srv.Register(fcmsrv.New(d.FCM))
	}

	// Cloud Billing (cloudbilling.googleapis.com) + Budget API
	// (billingbudgets.googleapis.com) share the /v1/billingAccounts URL space and
	// have no driver — the control plane is a self-contained store seeded with a
	// default account and catalog, so the handler is always registered (like
	// servicenetworking). Its /v1/projects/{p}/billingInfo route overlaps the
	// /v1/projects/ family, so it registers before Firestore; the billingInfo-
	// suffix guard keeps it disjoint from Firestore's /v1/projects/{p}/databases.
	srv.Register(cloudbilling.New())

	// Project-level IAM policy (cloudresourcemanager.googleapis.com):
	// POST /v1/projects/{p}:{get,set}IamPolicy / :testIamPermissions. This is
	// the surface google_project_iam_member/_binding/_policy/_audit_config drive
	// via etag read-modify-write. It has no driver (the project policy is a
	// wire-only store) so it is always registered, like cloudbilling above; the
	// colon-verb single-segment guard keeps it disjoint from every other
	// /v1/projects/ handler, but it must precede Firestore's permissive prefix.
	srv.Register(resourcemanager.New())

	// Cloud KMS (cloudkms.googleapis.com) matches /v1/projects/{p}/locations/{l}/
	// keyRings[/…]. Its keyRings resource-type guard is disjoint from every other
	// /v1/projects/ handler (Memorystore's instances, GKE's clusters, Cloud
	// Functions' functions, Eventarc's triggers, Scheduler's jobs, Vertex AI,
	// Artifact Registry's repositories), so registration order among them is
	// unconstrained. It has no portable driver — the control-plane state is a
	// self-contained store (like Cloud Billing and project IAM above) so the
	// handler is always registered; it must precede Firestore's permissive
	// /v1/projects/ prefix. d.Clock (may be nil) makes create/destroy timestamps
	// deterministic under a FakeClock.
	srv.Register(kmssrv.New(d.Clock))

	if d.Firestore != nil {
		// The Firestore Admin API (projects.databases[.collectionGroups.indexes])
		// registers BEFORE the document data-plane handler: the data-plane's
		// Matches greedily claims every /v1/projects/ path, while the admin
		// handler's Matches claims only the databases/indexes/operations shapes
		// and defers .../documents to the data plane. First-match-wins keeps the
		// two disjoint on one server (google_firestore_database provisions via the
		// admin handler; app document reads/writes hit the data plane).
		srv.Register(firestore.NewAdmin())
		srv.Register(firestore.New(d.Firestore))
	}

	if d.Monitoring != nil {
		srv.Register(monitoring.New(d.Monitoring))
	}

	// Kubernetes data-plane API. Matches /k8s/{uid}/... — disjoint from every
	// other GCP path. Registered before the GCS fallback.
	if d.K8sAPI != nil {
		srv.Register(d.K8sAPI)
	}

	if d.Storage != nil {
		gcsHandler := gcs.New(d.Storage)
		// GCS -> Pub/Sub: an object finalize/delete emits an event to each
		// matching bucket notificationConfig's topic, completing the
		// GCS -> Pub/Sub -> Cloud Functions chain.
		if pubsubHandler != nil {
			gcsHandler.SetPublisher(pubsubHandler)
		}

		// GCS -> Cloud Functions: an object finalize/delete also invokes any gen2
		// function whose storage eventTrigger is bound directly to the bucket —
		// the Eventarc-backed delivery a real gen2 storage trigger uses, separate
		// from (and requiring no) notificationConfig/topic.
		if cfHandler != nil {
			gcsHandler.SetFunctionInvoker(cfHandler)
		}

		srv.Register(gcsHandler)
	}

	// When Cloud Logging is present, observe every served request and write a
	// Cloud Audit Log Admin Activity entry for mutating operations, so the audit
	// trail reflects real API activity. This is the GCP analog of the AWS
	// CloudTrail observer.
	installAuditObserver(srv, d.CloudLogging, d.Clock)

	return srv
}

// installAuditObserver wires the Cloud Audit Log observer when Cloud Logging is
// present. A nil sink leaves the server's request path unchanged; a nil clock
// falls back to the real clock.
func installAuditObserver(srv *server.Server, logs logdriver.Logging, clock config.Clock) {
	if logs == nil {
		return
	}

	if clock == nil {
		clock = config.RealClock{}
	}

	srv.SetObserver(func(r *http.Request) { recordAuditLogEvent(logs, r, clock) })
}
