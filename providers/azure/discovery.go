package azure

import "github.com/stackshy/cloudemu/v2/services/resourcediscovery"

// projectDiscovery maps a slice of service records onto cross-service inventory
// rows via project, sharing the make-and-append loop the per-service
// GenericResources adapters (managedIdentityDiscovery, sqlVirtualMachineDiscovery)
// would otherwise each repeat. Each element is passed by pointer so a wide record
// is projected without a per-row copy.
func projectDiscovery[T any](
	items []T, project func(*T) resourcediscovery.DiscoveredResource,
) []resourcediscovery.DiscoveredResource {
	out := make([]resourcediscovery.DiscoveredResource, 0, len(items))
	for i := range items {
		out = append(out, project(&items[i]))
	}

	return out
}
