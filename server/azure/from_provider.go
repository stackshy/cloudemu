package azure

import (
	"net/http"

	azureprovider "github.com/stackshy/cloudemu/v2/providers/azure"
)

// DriversFrom builds a Drivers bundle from a fully-constructed Azure provider,
// wiring every server driver to the provider's corresponding mock. It lets a
// standalone binary go from a *azure.Provider to a running server without
// hand-mapping each field.
//
// The compute mock backs virtual machines, disks, snapshots, images and SSH
// public keys (the compute driver's Volume*/Snapshot*/Image* methods). The
// Databricks / Azure AI / Azure AI Search mocks each satisfy both the control
// and data-plane driver interfaces, so a single mock feeds every related field.
func DriversFrom(p *azureprovider.Provider) Drivers {
	return Drivers{
		// Compute mock backs all five compute-derived surfaces.
		VirtualMachines: p.VirtualMachines,
		Disks:           p.VirtualMachines,
		Snapshots:       p.VirtualMachines,
		Images:          p.VirtualMachines,
		SSHPublicKeys:   p.VirtualMachines,

		BlobStorage:        p.BlobStorage,
		QueueStorage:       p.QueueStorage,
		TableStorage:       p.TableStorage,
		CosmosDB:           p.CosmosDB,
		ManagedCassandra:   p.ManagedCassandra,
		CosmosPostgreSQL:   p.CosmosPostgreSQL,
		Network:            p.VNet,
		Monitor:            p.Monitor,
		Functions:          p.Functions,
		ServiceBus:         p.ServiceBus,
		SQL:                p.SQL,
		PostgresFlex:       p.PostgresFlex,
		MySQLFlex:          p.MySQLFlex,
		AKS:                p.AKS,
		ManagedIdentity:    p.ManagedIdentity,
		SQLVirtualMachine:  p.SQLVirtualMachine,
		ContainerApps:      p.ContainerApps,
		IAM:                p.IAM,
		ACR:                p.ACR,
		ContainerInstances: p.ContainerInstances,
		KeyVault:           p.KeyVault,
		DNS:                p.DNS,
		LB:                 p.LB,
		EventGrid:          p.EventGrid,
		LogAnalytics:       p.LogAnalytics,
		Cache:              p.Cache,

		NotificationHubs: p.NotificationHubs,

		// Databricks mock satisfies both the control and data-plane drivers.
		// DatabricksDataPlane must be set for registerDatabricksDataPlane to
		// register the workspace data-plane handlers.
		Databricks:          p.Databricks,
		DatabricksDataPlane: p.Databricks,

		// Azure AI mock satisfies CognitiveServices, MachineLearning and the
		// inference/Assistants data plane.
		CognitiveServices: p.AI,
		MachineLearning:   p.AI,
		AzureAIDataPlane:  p.AI,

		// Azure AI Search mock satisfies both the ARM control plane and the
		// search data plane.
		SearchControl:   p.Search,
		SearchDataPlane: p.Search,

		// K8sAPI is a shared handle with no provider source; the standalone
		// server has no shared cluster by default.
		K8sAPI: nil, // injected by the caller when a shared cluster is desired

		ResourceDiscovery: p.ResourceDiscovery,
		SubscriptionID:    p.SubscriptionID,
		EnforceAuth:       p.EnforceAuth,
	}
}

// NewFromProvider builds a ready-to-serve Azure server from a fully-constructed
// provider, wiring every driver via DriversFrom.
func NewFromProvider(p *azureprovider.Provider) http.Handler {
	return New(DriversFrom(p))
}
