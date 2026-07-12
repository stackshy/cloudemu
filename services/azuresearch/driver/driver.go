// Package driver defines the interface for Azure AI Search
// (Microsoft.Search/searchServices) — both the ARM control plane (service
// lifecycle, admin/query keys, private links) and the search data plane
// (indexes, documents, indexers, data sources, skillsets, synonym maps,
// aliases, service statistics).
//
// The interface uses plain Go types only (no cloud SDK dependencies). ARM
// resource IDs follow the standard
// /subscriptions/{s}/resourceGroups/{rg}/providers/Microsoft.Search/searchServices/{name}
// convention. The data plane is hosted at {service}.search.windows.net.
package driver

// Provisioning / status values for a search service.
const (
	StateSucceeded    = "succeeded"
	StateProvisioning = "provisioning"
	StateFailed       = "failed"

	StatusRunning  = "running"
	StatusDegraded = "degraded"
	StatusError    = "error"
)

// AzureSearch is the full Azure AI Search surface: the ARM control plane plus
// the search data plane.
type AzureSearch interface {
	SearchControl
	SearchDataPlane
}
