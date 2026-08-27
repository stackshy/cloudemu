// Package gcp assembles CloudEmu's GCP-compatible HTTP server.
//
// New takes a Drivers bundle and returns a *server.Server preloaded with the
// handler for each non-nil driver. Consumers that want a single service can
// skip this package and register the handler directly on their own
// server.Server.
package gcp

import (
	gkeprov "github.com/stackshy/cloudemu/v2/providers/gcp/gke"
	gcpmon "github.com/stackshy/cloudemu/v2/providers/gcp/monitoring"
	"github.com/stackshy/cloudemu/v2/server"
	alloydbsrv "github.com/stackshy/cloudemu/v2/server/gcp/alloydb"
	"github.com/stackshy/cloudemu/v2/server/gcp/artifactregistry"
	bigtableserver "github.com/stackshy/cloudemu/v2/server/gcp/bigtable"
	"github.com/stackshy/cloudemu/v2/server/gcp/cloudasset"
	"github.com/stackshy/cloudemu/v2/server/gcp/clouddns"
	"github.com/stackshy/cloudemu/v2/server/gcp/cloudfunctions"
	cloudloggingsrv "github.com/stackshy/cloudemu/v2/server/gcp/cloudlogging"
	cloudrunsrv "github.com/stackshy/cloudemu/v2/server/gcp/cloudrun"
	"github.com/stackshy/cloudemu/v2/server/gcp/cloudsql"
	"github.com/stackshy/cloudemu/v2/server/gcp/compute"
	"github.com/stackshy/cloudemu/v2/server/gcp/eventarc"
	fcmsrv "github.com/stackshy/cloudemu/v2/server/gcp/fcm"
	"github.com/stackshy/cloudemu/v2/server/gcp/firestore"
	"github.com/stackshy/cloudemu/v2/server/gcp/gcs"
	"github.com/stackshy/cloudemu/v2/server/gcp/gke"
	"github.com/stackshy/cloudemu/v2/server/gcp/iam"
	lbsrv "github.com/stackshy/cloudemu/v2/server/gcp/loadbalancer"
	"github.com/stackshy/cloudemu/v2/server/gcp/lro"
	memorystoresrv "github.com/stackshy/cloudemu/v2/server/gcp/memorystore"
	"github.com/stackshy/cloudemu/v2/server/gcp/monitoring"
	"github.com/stackshy/cloudemu/v2/server/gcp/pubsub"
	secretmanagersrv "github.com/stackshy/cloudemu/v2/server/gcp/secretmanager"
	"github.com/stackshy/cloudemu/v2/server/gcp/servicenetworking"
	vertexaisrv "github.com/stackshy/cloudemu/v2/server/gcp/vertexai"
	"github.com/stackshy/cloudemu/v2/server/gcp/vpc"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	btdriver "github.com/stackshy/cloudemu/v2/services/bigtable/driver"
	cachedriver "github.com/stackshy/cloudemu/v2/services/cache/driver"
	cloudrundriver "github.com/stackshy/cloudemu/v2/services/cloudrun/driver"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
	crdriver "github.com/stackshy/cloudemu/v2/services/containerregistry/driver"
	dbdriver "github.com/stackshy/cloudemu/v2/services/database/driver"
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
	vertexaidriver "github.com/stackshy/cloudemu/v2/services/vertexai/driver"
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
	CloudSQL rdbdriver.RelationalDB
	GKE      *gkeprov.Mock
	// AlloyDB serves the alloydb.googleapis.com v1 REST API against a
	// relationaldb driver that also implements the AlloyDB capability. Its
	// paths (/v1/projects/{p}/locations/{l}/clusters…) are identical to GKE's,
	// so the two cannot be multiplexed on one server; AlloyDB is left nil in
	// DriversFrom and injected by callers that want it instead of GKE.
	AlloyDB          rdbdriver.RelationalDB
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
//nolint:gocritic,gocyclo,funlen // Drivers is all interface fields; one if-per-driver is the simplest expression and grows with the bundle.
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
		srv.Register(gke.New(d.GKE))
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

	if d.CloudSQL != nil {
		srv.Register(cloudsql.New(d.CloudSQL))
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

	// Memorystore matches /v1/projects/{p}/locations/{l}/{instances|operations}
	// — its resource-type guard is disjoint from GKE (clusters), Cloud Functions
	// (functions), Vertex AI, and the rest of the /v1/projects/ family, so
	// registration order among them is unconstrained. Registered before
	// Firestore's permissive /v1/projects/ prefix so its paths aren't swallowed.
	if d.Memorystore != nil {
		msH := memorystoresrv.New(d.Memorystore)
		msH.SetOperationRegistry(opsReg)
		srv.Register(msH)
	}

	// FCM matches /v1/projects/{p}/messages:send — disjoint from every other
	// /v1/projects/ handler (none use the messages:send suffix). Registered
	// before Firestore's permissive /v1/projects/ prefix match.
	if d.FCM != nil {
		srv.Register(fcmsrv.New(d.FCM))
	}

	if d.Firestore != nil {
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

		srv.Register(gcsHandler)
	}

	return srv
}
