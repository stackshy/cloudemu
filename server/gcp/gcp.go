// Package gcp assembles CloudEmu's GCP-compatible HTTP server.
//
// New takes a Drivers bundle and returns a *server.Server preloaded with the
// handler for each non-nil driver. Consumers that want a single service can
// skip this package and register the handler directly on their own
// server.Server.
package gcp

import (
	computedriver "github.com/stackshy/cloudemu/compute/driver"
	crdriver "github.com/stackshy/cloudemu/containerregistry/driver"
	dbdriver "github.com/stackshy/cloudemu/database/driver"
	dnsdriver "github.com/stackshy/cloudemu/dns/driver"
	ebdriver "github.com/stackshy/cloudemu/eventbus/driver"
	iamdriver "github.com/stackshy/cloudemu/iam/driver"
	"github.com/stackshy/cloudemu/kubernetes"
	lbdriver "github.com/stackshy/cloudemu/loadbalancer/driver"
	mqdriver "github.com/stackshy/cloudemu/messagequeue/driver"
	mondriver "github.com/stackshy/cloudemu/monitoring/driver"
	netdriver "github.com/stackshy/cloudemu/networking/driver"
	gkeprov "github.com/stackshy/cloudemu/providers/gcp/gke"
	rdbdriver "github.com/stackshy/cloudemu/relationaldb/driver"
	"github.com/stackshy/cloudemu/resourcediscovery"
	secretsdriver "github.com/stackshy/cloudemu/secrets/driver"
	"github.com/stackshy/cloudemu/server"
	"github.com/stackshy/cloudemu/server/gcp/artifactregistry"
	"github.com/stackshy/cloudemu/server/gcp/cloudasset"
	"github.com/stackshy/cloudemu/server/gcp/clouddns"
	"github.com/stackshy/cloudemu/server/gcp/cloudfunctions"
	"github.com/stackshy/cloudemu/server/gcp/cloudsql"
	"github.com/stackshy/cloudemu/server/gcp/compute"
	"github.com/stackshy/cloudemu/server/gcp/eventarc"
	"github.com/stackshy/cloudemu/server/gcp/firestore"
	"github.com/stackshy/cloudemu/server/gcp/gcs"
	"github.com/stackshy/cloudemu/server/gcp/gke"
	"github.com/stackshy/cloudemu/server/gcp/iam"
	lbsrv "github.com/stackshy/cloudemu/server/gcp/loadbalancer"
	"github.com/stackshy/cloudemu/server/gcp/monitoring"
	"github.com/stackshy/cloudemu/server/gcp/networks"
	"github.com/stackshy/cloudemu/server/gcp/pubsub"
	secretmanagersrv "github.com/stackshy/cloudemu/server/gcp/secretmanager"
	vertexaisrv "github.com/stackshy/cloudemu/server/gcp/vertexai"
	sdrv "github.com/stackshy/cloudemu/serverless/driver"
	storagedriver "github.com/stackshy/cloudemu/storage/driver"
	vertexaidriver "github.com/stackshy/cloudemu/vertexai/driver"
)

// Drivers bundles the driver interfaces the GCP server can expose.
type Drivers struct {
	Compute          computedriver.Compute
	Storage          storagedriver.Bucket
	Firestore        dbdriver.Database
	Networking       netdriver.Networking
	Monitoring       mondriver.Monitoring
	CloudFunctions   sdrv.Serverless
	PubSub           mqdriver.MessageQueue
	CloudSQL         rdbdriver.RelationalDB
	GKE              *gkeprov.Mock
	VertexAI         vertexaidriver.VertexAI
	IAM              iamdriver.IAM
	ArtifactRegistry crdriver.ContainerRegistry
	// CloudDNS serves the dns.googleapis.com v1 REST API against the dns
	// driver.
	CloudDNS dnsdriver.DNS
	// LB serves the Cloud Load Balancing REST API (backendServices +
	// forwardingRules on the compute API) against the loadbalancer driver.
	LB lbdriver.LoadBalancer
	// SecretManager serves the secretmanager.googleapis.com v1 REST API
	// against the secrets driver.
	SecretManager secretsdriver.Secrets
	// Eventarc serves the eventarc.googleapis.com v1 REST API against the
	// eventbus driver, mapping triggers to rules under a per-location bus.
	Eventarc ebdriver.EventBus
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
//nolint:gocritic,gocyclo // Drivers is all interface fields; one if-per-driver is the simplest expression and grows with the bundle.
func New(d Drivers) *server.Server {
	srv := server.New()

	if d.Compute != nil {
		srv.Register(compute.New(d.Compute))
	}

	if d.Networking != nil {
		srv.Register(networks.New(d.Networking))
	}

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
		srv.Register(lbsrv.New(d.LB))
	}

	// CloudFunctions matches /v1/projects/{p}/locations/{l}/functions paths
	// before Firestore so the locations+functions guard wins over Firestore's
	// /v1/projects/ prefix match.
	if d.CloudFunctions != nil {
		srv.Register(cloudfunctions.New(d.CloudFunctions))
	}

	// PubSub matches /v1/projects/{p}/{topics|subscriptions}/...; register
	// before Firestore so its more-specific resource-type guard wins over
	// Firestore's permissive /v1/projects/ prefix.
	if d.PubSub != nil {
		srv.Register(pubsub.New(d.PubSub))
	}

	// Cloud SQL matches /v1/projects/{p}/{instances|operations}/...; same
	// /v1/projects/ space as Firestore, so register first.
	if d.CloudSQL != nil {
		srv.Register(cloudsql.New(d.CloudSQL))
	}

	// GKE matches /v1/projects/{p}/locations/{l}/{clusters|operations}/...;
	// same /v1/projects/ space as Firestore, so register first.
	if d.GKE != nil {
		srv.Register(gke.New(d.GKE))
	}

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
		srv.Register(artifactregistry.New(d.ArtifactRegistry))
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
		srv.Register(eventarc.New(d.Eventarc))
	}

	// Cloud DNS matches /dns/v1/projects/{p}/managedZones[...] — a distinct
	// URL space from the /v1/projects/ family, so registration order is
	// unconstrained relative to Firestore and the rest. Registered before the
	// GCS fallback for consistency with the other handlers.
	if d.CloudDNS != nil {
		srv.Register(clouddns.New(d.CloudDNS))
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
		srv.Register(gcs.New(d.Storage))
	}

	return srv
}
