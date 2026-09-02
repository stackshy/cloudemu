package azure

import (
	"context"

	"github.com/stackshy/cloudemu/v2/providers/azure/sqlvirtualmachine"
	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
)

// sqlVirtualMachineDiscovery projects CRUD-created SQL virtual machines
// (Microsoft.SqlVirtualMachine/sqlVirtualMachines) into the cross-service
// inventory so they surface in Resource Graph / `az resource list`. It rides the
// generic GenericResources projection (like managedIdentityDiscovery) rather
// than a shared walker, since SQL virtual machines are Azure-only with no shared
// cross-cloud driver.
//
// A row is emitted under ServiceCompute with the portable TypeSQLVirtualMachine,
// the same pair the compute walker's cloudemu:sqlvm tag overlay emits, so both a
// real CRUD resource and a tag-opted-in VM map to the identical Resource Graph
// type (microsoft.sqlvirtualmachine/sqlvirtualmachines). The two paths use
// disjoint id namespaces — the overlay is keyed by the paired VM's name, this by
// the caller-chosen SQL-virtual-machine name — so a real resource and a tag
// overlay never collide unless a caller deliberately opts a VM into both.
type sqlVirtualMachineDiscovery struct{ m *sqlvirtualmachine.Mock }

func (d sqlVirtualMachineDiscovery) DiscoverResources(
	ctx context.Context,
) ([]resourcediscovery.DiscoveredResource, error) {
	recs, err := d.m.DiscoverSQLVirtualMachines(ctx)
	if err != nil {
		return nil, err
	}

	return projectDiscovery(recs, func(r *sqlvirtualmachine.Record) resourcediscovery.DiscoveredResource {
		return resourcediscovery.DiscoveredResource{
			Service: resourcediscovery.ServiceCompute,
			Type:    resourcediscovery.TypeSQLVirtualMachine,
			ID:      r.Name,
			ARN:     r.ARMID(),
			Region:  r.Location,
			Tags:    r.Tags,
			Attrs:   resourcediscovery.Attributes{SKU: r.Properties.SQLImageSku, Properties: sqlVMProps(r)},
		}
	}), nil
}

// sqlVMProps projects the identifying slice of a SQL virtual machine into its
// inventory row's properties bag: the linked compute VM id and the SQL image
// offer/edition/license a discoverer reads to describe or price the resource.
// Returns nil when none are set, so an empty properties block is omitted.
func sqlVMProps(r *sqlvirtualmachine.Record) map[string]any {
	props := map[string]any{}

	if v := r.Properties.VirtualMachineResourceID; v != "" {
		props["virtualMachineResourceId"] = v
	}

	if v := r.Properties.SQLImageOffer; v != "" {
		props["sqlImageOffer"] = v
	}

	if v := r.Properties.SQLServerLicenseType; v != "" {
		props["sqlServerLicenseType"] = v
	}

	if len(props) == 0 {
		return nil
	}

	return props
}
