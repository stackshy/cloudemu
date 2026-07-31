package gcp

import (
	gcpprovider "github.com/stackshy/cloudemu/v2/providers/gcp"
	"github.com/stackshy/cloudemu/v2/server"
)

// DriversFrom maps a fully-constructed GCP provider onto a Drivers bundle,
// wiring every service handler the standalone server can expose. It lets a
// standalone binary go from a provider to a running server without
// hand-mapping each field.
func DriversFrom(p *gcpprovider.Provider) Drivers {
	return Drivers{
		Compute:          p.GCE,
		Storage:          p.GCS,
		Firestore:        p.Firestore,
		Networking:       p.VPC,
		Monitoring:       p.CloudMonitoring,
		CloudFunctions:   p.CloudFunctions,
		PubSub:           p.PubSub,
		CloudSQL:         p.CloudSQL,
		GKE:              p.GKE,
		VertexAI:         p.VertexAI,
		IAM:              p.IAM,
		ArtifactRegistry: p.ArtifactRegistry,
		CloudDNS:         p.CloudDNS,
		LB:               p.LB,
		CloudLogging:     p.CloudLogging,
		SecretManager:    p.SecretManager,
		Eventarc:         p.Eventarc,
		Memorystore:      p.Memorystore,
		FCM:              p.FCM,
		// K8sAPI is left nil; injected by the caller when a shared cluster is desired.
		K8sAPI: nil,
		// AlloyDB is left nil: its REST paths collide with GKE's, so callers that
		// want AlloyDB inject p.AlloyDB explicitly (in place of GKE).
		AlloyDB:           nil,
		ResourceDiscovery: p.ResourceDiscovery,
		ProjectID:         p.ProjectID,
	}
}

// NewFromProvider builds a GCP server from a fully-constructed provider.
func NewFromProvider(p *gcpprovider.Provider) *server.Server {
	return New(DriversFrom(p))
}

// DriversFromWithAlloyDB is like DriversFrom but enables AlloyDB in place of
// GKE: the two serve identical REST paths and cannot coexist on one server, so
// this helper wires AlloyDB and nils GKE to avoid the ambiguity.
func DriversFromWithAlloyDB(p *gcpprovider.Provider) Drivers {
	d := DriversFrom(p)
	d.AlloyDB = p.AlloyDB
	d.GKE = nil

	return d
}
