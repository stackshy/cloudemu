// Package gcp provides GCP mock provider factories.
package gcp

import (
	"context"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/gcp/alloydb"
	"github.com/stackshy/cloudemu/v2/providers/gcp/artifactregistry"
	"github.com/stackshy/cloudemu/v2/providers/gcp/bigtable"
	"github.com/stackshy/cloudemu/v2/providers/gcp/clouddns"
	"github.com/stackshy/cloudemu/v2/providers/gcp/cloudfunctions"
	"github.com/stackshy/cloudemu/v2/providers/gcp/cloudlogging"
	"github.com/stackshy/cloudemu/v2/providers/gcp/cloudmonitoring"
	"github.com/stackshy/cloudemu/v2/providers/gcp/cloudsql"
	"github.com/stackshy/cloudemu/v2/providers/gcp/eventarc"
	"github.com/stackshy/cloudemu/v2/providers/gcp/fcm"
	"github.com/stackshy/cloudemu/v2/providers/gcp/firestore"
	"github.com/stackshy/cloudemu/v2/providers/gcp/gce"
	"github.com/stackshy/cloudemu/v2/providers/gcp/gcs"
	"github.com/stackshy/cloudemu/v2/providers/gcp/gke"
	"github.com/stackshy/cloudemu/v2/providers/gcp/iam"
	"github.com/stackshy/cloudemu/v2/providers/gcp/loadbalancer"
	"github.com/stackshy/cloudemu/v2/providers/gcp/memorystore"
	"github.com/stackshy/cloudemu/v2/providers/gcp/pubsub"
	"github.com/stackshy/cloudemu/v2/providers/gcp/secretmanager"
	"github.com/stackshy/cloudemu/v2/providers/gcp/vertexai"
	"github.com/stackshy/cloudemu/v2/providers/gcp/vpc"
	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
)

// gkeDiscovery adapts the GKE mock to the resourcediscovery KubernetesClusters
// capability, so GKE clusters and node pools surface in Cloud Asset Inventory.
type gkeDiscovery struct{ m *gke.Mock }

func (a gkeDiscovery) DiscoverClusters(ctx context.Context) ([]resourcediscovery.DiscoveredCluster, error) {
	// Empty location lists clusters across all regions.
	clusters, err := a.m.ListClusters(ctx, "")
	if err != nil {
		return nil, err
	}

	out := make([]resourcediscovery.DiscoveredCluster, 0, len(clusters))

	for i := range clusters {
		c := clusters[i]
		out = append(out, resourcediscovery.DiscoveredCluster{
			Name:       c.Name,
			Region:     c.Location,
			Tags:       c.ResourceLabels,
			NodeGroups: resourcediscovery.NodeGroupsFromNames(c.NodePoolNames),
		})
	}

	return out, nil
}

// Provider holds all GCP mock services.
type Provider struct {
	GCS              *gcs.Mock
	GCE              *gce.Mock
	Firestore        *firestore.Mock
	CloudFunctions   *cloudfunctions.Mock
	VPC              *vpc.Mock
	CloudMonitoring  *cloudmonitoring.Mock
	IAM              *iam.Mock
	CloudDNS         *clouddns.Mock
	LB               *loadbalancer.Mock
	PubSub           *pubsub.Mock
	Memorystore      *memorystore.Mock
	SecretManager    *secretmanager.Mock
	CloudLogging     *cloudlogging.Mock
	FCM              *fcm.Mock
	ArtifactRegistry *artifactregistry.Mock
	Eventarc         *eventarc.Mock
	Bigtable         *bigtable.Mock
	CloudSQL         *cloudsql.Mock
	AlloyDB          *alloydb.Mock
	GKE              *gke.Mock
	VertexAI         *vertexai.Mock

	ResourceDiscovery *resourcediscovery.Engine

	// ProjectID and Region record the identity this provider was created with,
	// so callers (e.g. a standalone server) can wire them without re-reading
	// the options.
	ProjectID string
	Region    string
}

// New creates a new GCP provider with all mock services.
func New(opts ...config.Option) *Provider {
	o := config.NewOptions(opts...)
	p := &Provider{
		GCS:              gcs.New(o),
		GCE:              gce.New(o),
		Firestore:        firestore.New(o),
		CloudFunctions:   cloudfunctions.New(o),
		VPC:              vpc.New(o),
		CloudMonitoring:  cloudmonitoring.New(o),
		IAM:              iam.New(o),
		CloudDNS:         clouddns.New(o),
		LB:               loadbalancer.New(o),
		PubSub:           pubsub.New(o),
		Memorystore:      memorystore.New(o),
		SecretManager:    secretmanager.New(o),
		CloudLogging:     cloudlogging.New(o),
		FCM:              fcm.New(o),
		ArtifactRegistry: artifactregistry.New(o),
		Eventarc:         eventarc.New(o),
		Bigtable:         bigtable.New(o),
		CloudSQL:         cloudsql.New(o),
		AlloyDB:          alloydb.New(o),
		GKE:              gke.New(o),
		VertexAI:         vertexai.New(o),
		ProjectID:        o.ProjectID,
		Region:           o.Region,
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
	p.AlloyDB.SetMonitoring(p.CloudMonitoring)
	p.GKE.SetMonitoring(p.CloudMonitoring)
	p.VertexAI.SetMonitoring(p.CloudMonitoring)

	p.ResourceDiscovery = resourcediscovery.New(
		resourcediscovery.ProviderGCP, o.ProjectID, o.Region,
		&resourcediscovery.Drivers{
			Compute:      p.GCE,
			Networking:   p.VPC,
			Storage:      p.GCS,
			Database:     p.Firestore,
			Serverless:   p.CloudFunctions,
			Kubernetes:   gkeDiscovery{p.GKE},
			RelationalDB: gcpRelationalDiscovery{sql: p.CloudSQL, alloy: p.AlloyDB},
		},
	)

	return p
}

// gcpRelationalDiscovery fans GCP's relational mocks (Cloud SQL instances and
// AlloyDB clusters) into a single resourcediscovery.RelationalDatabases
// adapter, since the engine exposes one relational slot per provider.
type gcpRelationalDiscovery struct {
	sql   *cloudsql.Mock
	alloy *alloydb.Mock
}

func (d gcpRelationalDiscovery) DiscoverDatabases(
	ctx context.Context,
) ([]resourcediscovery.DiscoveredDatabase, error) {
	out, err := (cloudSQLDiscovery{d.sql}).DiscoverDatabases(ctx)
	if err != nil {
		return nil, err
	}

	clusters, err := d.alloy.DescribeClusters(ctx, nil)
	if err != nil {
		return nil, err
	}

	for i := range clusters {
		out = append(out, resourcediscovery.DiscoveredDatabase{
			Name:   clusters[i].ID,
			Type:   resourcediscovery.TypeAlloyDBCluster,
			Region: d.alloy.Region(),
			ARN:    clusters[i].ARN,
			Tags:   clusters[i].Tags,
		})
	}

	return out, nil
}

// cloudSQLDiscovery adapts the Cloud SQL mock to the resourcediscovery
// RelationalDatabases capability, so instances surface in Cloud Asset Inventory.
type cloudSQLDiscovery struct{ m *cloudsql.Mock }

func (d cloudSQLDiscovery) DiscoverDatabases(
	ctx context.Context,
) ([]resourcediscovery.DiscoveredDatabase, error) {
	insts, err := d.m.DescribeInstances(ctx, nil)
	if err != nil {
		return nil, err
	}

	out := make([]resourcediscovery.DiscoveredDatabase, 0, len(insts))

	for i := range insts {
		out = append(out, resourcediscovery.DiscoveredDatabase{
			Name: insts[i].ID, Type: resourcediscovery.TypeSQLInstance,
			Region: insts[i].AvailabilityZone, ARN: insts[i].ARN, Tags: insts[i].Tags,
		})
	}

	return out, nil
}
