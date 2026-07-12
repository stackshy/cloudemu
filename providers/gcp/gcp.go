// Package gcp provides GCP mock provider factories.
package gcp

import (
	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/gcp/artifactregistry"
	"github.com/stackshy/cloudemu/v2/providers/gcp/clouddns"
	"github.com/stackshy/cloudemu/v2/providers/gcp/cloudfunctions"
	"github.com/stackshy/cloudemu/v2/providers/gcp/cloudlogging"
	"github.com/stackshy/cloudemu/v2/providers/gcp/cloudmonitoring"
	"github.com/stackshy/cloudemu/v2/providers/gcp/cloudsql"
	"github.com/stackshy/cloudemu/v2/providers/gcp/eventarc"
	"github.com/stackshy/cloudemu/v2/providers/gcp/fcm"
	"github.com/stackshy/cloudemu/v2/providers/gcp/firestore"
	"github.com/stackshy/cloudemu/v2/providers/gcp/gce"
	"github.com/stackshy/cloudemu/v2/providers/gcp/gcpiam"
	"github.com/stackshy/cloudemu/v2/providers/gcp/gcplb"
	"github.com/stackshy/cloudemu/v2/providers/gcp/gcpvpc"
	"github.com/stackshy/cloudemu/v2/providers/gcp/gcs"
	"github.com/stackshy/cloudemu/v2/providers/gcp/gke"
	"github.com/stackshy/cloudemu/v2/providers/gcp/memorystore"
	"github.com/stackshy/cloudemu/v2/providers/gcp/pubsub"
	"github.com/stackshy/cloudemu/v2/providers/gcp/secretmanager"
	"github.com/stackshy/cloudemu/v2/providers/gcp/vertexai"
	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
)

// Provider holds all GCP mock services.
type Provider struct {
	GCS              *gcs.Mock
	GCE              *gce.Mock
	Firestore        *firestore.Mock
	CloudFunctions   *cloudfunctions.Mock
	VPC              *gcpvpc.Mock
	CloudMonitoring  *cloudmonitoring.Mock
	IAM              *gcpiam.Mock
	CloudDNS         *clouddns.Mock
	LB               *gcplb.Mock
	PubSub           *pubsub.Mock
	Memorystore      *memorystore.Mock
	SecretManager    *secretmanager.Mock
	CloudLogging     *cloudlogging.Mock
	FCM              *fcm.Mock
	ArtifactRegistry *artifactregistry.Mock
	Eventarc         *eventarc.Mock
	CloudSQL         *cloudsql.Mock
	GKE              *gke.Mock
	VertexAI         *vertexai.Mock

	ResourceDiscovery *resourcediscovery.Engine
}

// New creates a new GCP provider with all mock services.
func New(opts ...config.Option) *Provider {
	o := config.NewOptions(opts...)
	p := &Provider{
		GCS:              gcs.New(o),
		GCE:              gce.New(o),
		Firestore:        firestore.New(o),
		CloudFunctions:   cloudfunctions.New(o),
		VPC:              gcpvpc.New(o),
		CloudMonitoring:  cloudmonitoring.New(o),
		IAM:              gcpiam.New(o),
		CloudDNS:         clouddns.New(o),
		LB:               gcplb.New(o),
		PubSub:           pubsub.New(o),
		Memorystore:      memorystore.New(o),
		SecretManager:    secretmanager.New(o),
		CloudLogging:     cloudlogging.New(o),
		FCM:              fcm.New(o),
		ArtifactRegistry: artifactregistry.New(o),
		Eventarc:         eventarc.New(o),
		CloudSQL:         cloudsql.New(o),
		GKE:              gke.New(o),
		VertexAI:         vertexai.New(o),
	}
	p.GCE.SetMonitoring(p.CloudMonitoring)
	p.GCS.SetMonitoring(p.CloudMonitoring)
	p.Firestore.SetMonitoring(p.CloudMonitoring)
	p.CloudFunctions.SetMonitoring(p.CloudMonitoring)
	p.PubSub.SetMonitoring(p.CloudMonitoring)
	p.Memorystore.SetMonitoring(p.CloudMonitoring)
	p.CloudLogging.SetMonitoring(p.CloudMonitoring)
	p.FCM.SetMonitoring(p.CloudMonitoring)
	p.ArtifactRegistry.SetMonitoring(p.CloudMonitoring)
	p.Eventarc.SetMonitoring(p.CloudMonitoring)
	p.CloudSQL.SetMonitoring(p.CloudMonitoring)
	p.GKE.SetMonitoring(p.CloudMonitoring)
	p.VertexAI.SetMonitoring(p.CloudMonitoring)

	p.ResourceDiscovery = resourcediscovery.New(
		resourcediscovery.ProviderGCP, o.ProjectID, o.Region,
		&resourcediscovery.Drivers{
			Compute:    p.GCE,
			Networking: p.VPC,
			Storage:    p.GCS,
			Database:   p.Firestore,
			Serverless: p.CloudFunctions,
		},
	)

	return p
}
