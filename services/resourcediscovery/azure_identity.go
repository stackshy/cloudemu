package resourcediscovery

import (
	"context"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	crdriver "github.com/stackshy/cloudemu/v2/services/containerregistry/driver"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// Azure ARM-name tag keys. The Azure wire handlers create resources through the
// cross-cloud drivers, which mint their own ids (vm-00000033, vnet-00000022), and
// record the name the client chose under these tags. The inventory reports that
// name, as a GET on the resource does. Like the resource-group keys in arn.go
// they are stable wire-contract strings duplicated here so the services layer
// does not import the server layer.
const (
	azureVMNameTag         = "cloudemu:azureName"
	azureDiskNameTag       = "cloudemu:azureDiskName"
	azureSnapshotNameTag   = "cloudemu:azureSnapshotName"
	azureVNetNameTag       = "cloudemu:azureNetName"
	azureSubnetNameTag     = "cloudemu:azureSubnet"
	azureNSGNameTag        = "cloudemu:azureNSGName"
	azurePublicIPNameTag   = "cloudemu:azurePublicIP"
	azureNATGatewayNameTag = "cloudemu:azureNatGatewayName"
	azureRouteTableNameTag = "cloudemu:azureRouteTableName"
)

// azureName returns the ARM name recorded under key, or fallback when the
// provider is not Azure or no name was recorded (a portable-API creation). AWS
// and GCP always get fallback, so their ids are unchanged.
func (e *Engine) azureName(tags map[string]string, key, fallback string) string {
	if e.provider != ProviderAzure {
		return fallback
	}

	if v := tags[key]; v != "" {
		return v
	}

	return fallback
}

// azureNamedID builds the ARM id of a tag-named Azure resource from its recorded
// name and resource group. ok is false when the resource carries no ARM name, so
// the caller keeps its provider-neutral id.
func (e *Engine) azureNamedID(tags map[string]string, nameKey, provider, typ string) (name, id string, ok bool) {
	name = e.azureName(tags, nameKey, "")
	if name == "" {
		return "", "", false
	}

	rg := azureResourceGroupOrDefault(e.azureRGFromTags(tags))

	return name, idgen.AzureID(e.accountID, rg, provider, typ, name), true
}

// netIdentity returns a network row's id and ARN: on Azure the ARM name recorded
// under nameKey (when present), otherwise the driver id, as before.
func (e *Engine) netIdentity(kind, id string, tags map[string]string, nameKey string) (rowID, arn string) {
	rowID = e.azureName(tags, nameKey, id)

	return rowID, e.networkARN(kind, rowID, e.azureRGFromTags(tags))
}

// storageAccountARN is the ARM id of an Azure storage account. Only the Azure
// driver models accounts (storagedriver.AzureStorageAccounts), so there is no
// per-provider switch.
func (e *Engine) storageAccountARN(name, resourceGroup string) string {
	return idgen.AzureID(e.accountID, azureResourceGroupOrDefault(resourceGroup), "Microsoft.Storage", "storageAccounts", name)
}

// azureVNetRef is what a subnet row needs from its parent virtual network.
type azureVNetRef struct{ name, rg, location string }

// azureSubnetIdentity rewrites an Azure subnet row to its ARM child id
// (.../virtualNetworks/{vnet}/subnets/{name}) under the parent's resource group
// and location. Rows for other providers, or subnets without a recorded name or
// known parent, are left alone.
func (e *Engine) azureSubnetIdentity(r *Resource, vpcID string, vnets map[string]azureVNetRef) {
	name := e.azureName(r.Tags, azureSubnetNameTag, "")
	parent, ok := vnets[vpcID]

	if name == "" || !ok {
		return
	}

	r.ID = name
	r.ARN = idgen.AzureID(e.accountID, parent.rg, "Microsoft.Network",
		"virtualNetworks/"+parent.name+"/subnets", name)
	r.Region = parent.location
}

// walkAzureNetworkInterfaces lists Azure NICs, which the Azure driver keeps on
// its own (resourceGroup, name)-keyed surface rather than the cross-cloud one.
func (e *Engine) walkAzureNetworkInterfaces(ctx context.Context, svc netdriver.AzureNetworkInterfaces) ([]Resource, error) {
	nics, err := svc.ListNetworkInterfaces(ctx, "")
	if err != nil {
		return nil, err
	}

	out := make([]Resource, 0, len(nics))

	for i := range nics {
		n := &nics[i]
		out = append(out, Resource{
			Provider: e.provider, Service: ServiceNetworking, Type: TypeNetworkIface,
			ID:     n.Name,
			ARN:    e.networkARN(netKindNetworkIface, n.Name, n.ResourceGroup),
			Region: firstNonEmpty(n.Location, e.region), Tags: copyTags(n.Tags),
		})
	}

	return out, nil
}

// walkAzureRegistries emits one row per Azure container registry, the ARM
// resource (Microsoft.ContainerRegistry/registries). Repositories live in the
// registry's data plane and are not ARM resources, so they are not listed.
func (e *Engine) walkAzureRegistries(ctx context.Context, svc crdriver.AzureRegistryManager) ([]Resource, error) {
	regs, err := svc.ListRegistries(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("walkContainerRegistry registries: %w", err)
	}

	out := make([]Resource, 0, len(regs))

	for i := range regs {
		r := &regs[i]
		out = append(out, Resource{
			Provider: e.provider, Service: ServiceContainer, Type: TypeRegistry,
			ID: r.Name,
			ARN: idgen.AzureID(e.accountID, azureResourceGroupOrDefault(r.ResourceGroup),
				"Microsoft.ContainerRegistry", "registries", r.Name),
			Region:  firstNonEmpty(r.Location, e.region),
			Tags:    copyTags(r.Tags),
			SKU:     r.SKUName,
			SKUTier: r.SKUTier,
		})
	}

	return out, nil
}
