package oci

import (
	ociprovider "github.com/stackshy/cloudemu/v2/providers/oci"
)

// DriversFrom maps a fully-constructed OCI provider onto a Drivers bundle,
// wiring every service handler the standalone server can expose. It lets a
// standalone binary go from a provider to a running server without
// hand-mapping each field.
func DriversFrom(p *ociprovider.Provider) Drivers {
	return Drivers{
		ObjectStorage:     p.ObjectStorage,
		Compute:           p.Compute,
		NoSQL:             p.NoSQL,
		Functions:         p.Functions,
		VCN:               p.VCN,
		Monitoring:        p.Monitoring,
		Identity:          p.Identity,
		DNS:               p.DNS,
		LoadBalancer:      p.LoadBalancer,
		Queue:             p.Queue,
		Cache:             p.Cache,
		Vault:             p.Vault,
		Logging:           p.Logging,
		Notifications:     p.Notifications,
		ContainerRegistry: p.ContainerRegistry,
		Events:            p.Events,
		Database:          p.Database,
		// K8sAPI is left nil; injected by the caller when a shared cluster is desired.
		K8sAPI:            nil,
		ResourceDiscovery: p.ResourceDiscovery,
		TenancyOCID:       p.TenancyOCID,
		CompartmentID:     p.CompartmentID,
		Realm:             p.Realm,
		Region:            p.Region,
	}
}
