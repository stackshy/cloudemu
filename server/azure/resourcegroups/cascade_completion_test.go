// Deep-audit round-2 regression (R1): the resource-group cascade delete was
// incomplete. Deleting an RG tore down vnets, NSGs, VMs and storage accounts but
// orphaned the other Microsoft.Network children — public IPs, network
// interfaces, NAT gateways and load balancers — leaving them alive and globally
// addressable (ghosts that block name reuse and show in subscription-wide
// lists). Folded in with the #627 review nit: a cascade-deleted VM must also
// clear its attached data disks' managedBy (deleteOption=Detach), the same
// release the single-VM delete path does.
//
// Real Azure deletes every resource contained in a group. This test creates a
// group holding a public IP, a NIC, a NAT gateway, a load balancer and a
// VM-with-data-disk, deletes the group, and asserts every contained network
// resource is gone (404) while the detached data disk survives with its
// managedBy cleared — and that an identically-shaped resource in a DIFFERENT
// group is untouched.

package resourcegroups_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// cascadeClients bundles the ARM clients the completion test drives against one
// full Azure server.
type cascadeClients struct {
	rg   *armresources.ResourceGroupsClient
	pip  *armnetwork.PublicIPAddressesClient
	vnet *armnetwork.VirtualNetworksClient
	nic  *armnetwork.InterfacesClient
	nat  *armnetwork.NatGatewaysClient
	lb   *armnetwork.LoadBalancersClient
	disk *armcompute.DisksClient
	vm   *armcompute.VirtualMachinesClient
}

func newCascadeClients(t *testing.T) *cascadeClients {
	t.Helper()

	// The account id must equal the subscription the SDK clients address so the
	// VM's stamped resource id matches the NIC-attach path (see nic_crossref).
	cloudP := cloudemu.NewAzure(config.WithAccountID(subID))

	ts := httptest.NewTLSServer(azureserver.NewFromProvider(cloudP))
	t.Cleanup(ts.Close)

	opts := armOpts(ts)

	rgc, err := armresources.NewResourceGroupsClient(subID, fakeCred{}, opts)
	require.NoError(t, err)

	pipc, err := armnetwork.NewPublicIPAddressesClient(subID, fakeCred{}, opts)
	require.NoError(t, err)

	vnetc, err := armnetwork.NewVirtualNetworksClient(subID, fakeCred{}, opts)
	require.NoError(t, err)

	nicc, err := armnetwork.NewInterfacesClient(subID, fakeCred{}, opts)
	require.NoError(t, err)

	natc, err := armnetwork.NewNatGatewaysClient(subID, fakeCred{}, opts)
	require.NoError(t, err)

	lbc, err := armnetwork.NewLoadBalancersClient(subID, fakeCred{}, opts)
	require.NoError(t, err)

	diskc, err := armcompute.NewDisksClient(subID, fakeCred{}, opts)
	require.NoError(t, err)

	vmc, err := armcompute.NewVirtualMachinesClient(subID, fakeCred{}, opts)
	require.NoError(t, err)

	return &cascadeClients{rg: rgc, pip: pipc, vnet: vnetc, nic: nicc, nat: natc, lb: lbc, disk: diskc, vm: vmc}
}

// TestResourceGroupDeleteCascadesNetworkChildrenAndDetachesDisks is the
// load-bearing regression test for finding R1 plus the #627 cascade disk-detach
// nit.
func TestResourceGroupDeleteCascadesNetworkChildrenAndDetachesDisks(t *testing.T) {
	ctx := context.Background()
	c := newCascadeClients(t)

	const (
		rg      = "rg-full"
		otherRG = "rg-other"
	)

	require.NoError(t, createRG(ctx, c.rg, rg))
	require.NoError(t, createRG(ctx, c.rg, otherRG))

	// A public IP in a DIFFERENT resource group must survive the delete below.
	createSDKPublicIP(ctx, t, c.pip, otherRG, "pip-other")

	// Contained network resources.
	createSDKPublicIP(ctx, t, c.pip, rg, "pip-full")
	subnetID := createSDKVNetWithSubnet(ctx, t, c.vnet, rg, "vnet-full", "sub1")
	nicID := createSDKNICWithPublicIP(ctx, t, c.nic, rg, "nic-full", subnetID,
		publicIPID(rg, "pip-full"))
	createSDKNATGateway(ctx, t, c.nat, rg, "nat-full")
	createSDKLoadBalancer(ctx, t, c.lb, rg, "lb-full")

	// A VM with an attached data disk, whose NIC is the one created above (so the
	// cascade must tear the VM down before the NIC — and clear the disk).
	diskID := createSDKEmptyDisk(ctx, t, c.disk, rg, "disk-full")
	createSDKVMWithNICAndDisk(ctx, t, c.vm, rg, "vm-full", nicID, diskID)

	// Sanity: the disk is attached (managedBy set) before the delete.
	before, err := c.disk.Get(ctx, rg, "disk-full", nil)
	require.NoError(t, err)
	require.NotNil(t, before.ManagedBy)
	require.NotEmpty(t, *before.ManagedBy, "disk should be attached before RG delete")

	// Delete the resource group and wait for the cascade.
	delPoller, err := c.rg.BeginDelete(ctx, rg, nil)
	require.NoError(t, err)
	_, err = delPoller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond})
	require.NoError(t, err)

	// Every contained network resource is gone (404), not merely the group.
	assertGone(t, "public IP", func() error { _, e := c.pip.Get(ctx, rg, "pip-full", nil); return e })
	assertGone(t, "network interface", func() error { _, e := c.nic.Get(ctx, rg, "nic-full", nil); return e })
	assertGone(t, "NAT gateway", func() error { _, e := c.nat.Get(ctx, rg, "nat-full", nil); return e })
	assertGone(t, "load balancer", func() error { _, e := c.lb.Get(ctx, rg, "lb-full", nil); return e })
	assertGone(t, "virtual network", func() error { _, e := c.vnet.Get(ctx, rg, "vnet-full", nil); return e })

	// The data disk survives the cascade but with its managedBy cleared and its
	// diskState back to Unattached — the deleteOption=Detach release, now applied
	// on the cascade path too.
	after, err := c.disk.Get(ctx, rg, "disk-full", nil)
	require.NoError(t, err, "data disk should survive the cascade (deleteOption=Detach)")

	if after.ManagedBy != nil && *after.ManagedBy != "" {
		t.Errorf("disk-full managedBy=%q after cascade, want cleared", *after.ManagedBy)
	}

	if after.Properties == nil || after.Properties.DiskState == nil ||
		*after.Properties.DiskState != armcompute.DiskStateUnattached {
		t.Errorf("disk-full diskState not Unattached after cascade")
	}

	// The resource in the OTHER group is untouched.
	_, err = c.pip.Get(ctx, otherRG, "pip-other", nil)
	require.NoError(t, err, "public IP in a different resource group must not be affected by the delete")
}

func createRG(ctx context.Context, client *armresources.ResourceGroupsClient, name string) error {
	_, err := client.CreateOrUpdate(ctx, name, armresources.ResourceGroup{Location: to.Ptr("eastus")}, nil)

	return err
}

func createSDKPublicIP(ctx context.Context, t *testing.T, client *armnetwork.PublicIPAddressesClient, rg, name string) {
	t.Helper()

	poller, err := client.BeginCreateOrUpdate(ctx, rg, name, armnetwork.PublicIPAddress{
		Location: to.Ptr("eastus"),
		SKU:      &armnetwork.PublicIPAddressSKU{Name: to.Ptr(armnetwork.PublicIPAddressSKUNameStandard)},
		Properties: &armnetwork.PublicIPAddressPropertiesFormat{
			PublicIPAllocationMethod: to.Ptr(armnetwork.IPAllocationMethodStatic),
		},
	}, nil)
	require.NoError(t, err)
	_, err = poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond})
	require.NoError(t, err)
}

func createSDKVNetWithSubnet(
	ctx context.Context, t *testing.T, client *armnetwork.VirtualNetworksClient, rg, name, subnet string,
) string {
	t.Helper()

	poller, err := client.BeginCreateOrUpdate(ctx, rg, name, armnetwork.VirtualNetwork{
		Location: to.Ptr("eastus"),
		Properties: &armnetwork.VirtualNetworkPropertiesFormat{
			AddressSpace: &armnetwork.AddressSpace{AddressPrefixes: []*string{to.Ptr("10.0.0.0/16")}},
			Subnets: []*armnetwork.Subnet{{
				Name:       to.Ptr(subnet),
				Properties: &armnetwork.SubnetPropertiesFormat{AddressPrefix: to.Ptr("10.0.0.0/24")},
			}},
		},
	}, nil)
	require.NoError(t, err)
	_, err = poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond})
	require.NoError(t, err)

	return "/subscriptions/" + subID + "/resourceGroups/" + rg +
		"/providers/Microsoft.Network/virtualNetworks/" + name + "/subnets/" + subnet
}

func createSDKNICWithPublicIP(
	ctx context.Context, t *testing.T, client *armnetwork.InterfacesClient, rg, name, subnetID, pipID string,
) string {
	t.Helper()

	poller, err := client.BeginCreateOrUpdate(ctx, rg, name, armnetwork.Interface{
		Location: to.Ptr("eastus"),
		Properties: &armnetwork.InterfacePropertiesFormat{
			IPConfigurations: []*armnetwork.InterfaceIPConfiguration{{
				Name: to.Ptr("ipconfig1"),
				Properties: &armnetwork.InterfaceIPConfigurationPropertiesFormat{
					PrivateIPAddress:          to.Ptr("10.0.0.10"),
					PrivateIPAllocationMethod: to.Ptr(armnetwork.IPAllocationMethodStatic),
					Subnet:                    &armnetwork.Subnet{ID: to.Ptr(subnetID)},
					PublicIPAddress:           &armnetwork.PublicIPAddress{ID: to.Ptr(pipID)},
				},
			}},
		},
	}, nil)
	require.NoError(t, err)
	_, err = poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond})
	require.NoError(t, err)

	return "/subscriptions/" + subID + "/resourceGroups/" + rg +
		"/providers/Microsoft.Network/networkInterfaces/" + name
}

func createSDKNATGateway(ctx context.Context, t *testing.T, client *armnetwork.NatGatewaysClient, rg, name string) {
	t.Helper()

	poller, err := client.BeginCreateOrUpdate(ctx, rg, name, armnetwork.NatGateway{
		Location:   to.Ptr("eastus"),
		SKU:        &armnetwork.NatGatewaySKU{Name: to.Ptr(armnetwork.NatGatewaySKUNameStandard)},
		Properties: &armnetwork.NatGatewayPropertiesFormat{},
	}, nil)
	require.NoError(t, err)
	_, err = poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond})
	require.NoError(t, err)
}

func createSDKLoadBalancer(ctx context.Context, t *testing.T, client *armnetwork.LoadBalancersClient, rg, name string) {
	t.Helper()

	poller, err := client.BeginCreateOrUpdate(ctx, rg, name, armnetwork.LoadBalancer{
		Location: to.Ptr("eastus"),
		SKU:      &armnetwork.LoadBalancerSKU{Name: to.Ptr(armnetwork.LoadBalancerSKUNameStandard)},
	}, nil)
	require.NoError(t, err)
	_, err = poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond})
	require.NoError(t, err)
}

func createSDKEmptyDisk(ctx context.Context, t *testing.T, client *armcompute.DisksClient, rg, name string) string {
	t.Helper()

	poller, err := client.BeginCreateOrUpdate(ctx, rg, name, armcompute.Disk{
		Location: to.Ptr("eastus"),
		Properties: &armcompute.DiskProperties{
			CreationData: &armcompute.CreationData{CreateOption: to.Ptr(armcompute.DiskCreateOptionEmpty)},
			DiskSizeGB:   to.Ptr[int32](4),
		},
	}, nil)
	require.NoError(t, err)
	created, err := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond})
	require.NoError(t, err)
	require.NotNil(t, created.ID)

	return *created.ID
}

func createSDKVMWithNICAndDisk(
	ctx context.Context, t *testing.T, client *armcompute.VirtualMachinesClient, rg, name, nicID, diskID string,
) {
	t.Helper()

	poller, err := client.BeginCreateOrUpdate(ctx, rg, name, armcompute.VirtualMachine{
		Location: to.Ptr("eastus"),
		Properties: &armcompute.VirtualMachineProperties{
			HardwareProfile: &armcompute.HardwareProfile{
				VMSize: to.Ptr(armcompute.VirtualMachineSizeTypesStandardD2SV3),
			},
			StorageProfile: &armcompute.StorageProfile{
				OSDisk: &armcompute.OSDisk{OSType: to.Ptr(armcompute.OperatingSystemTypesLinux)},
				DataDisks: []*armcompute.DataDisk{{
					Lun:          to.Ptr[int32](0),
					CreateOption: to.Ptr(armcompute.DiskCreateOptionTypesAttach),
					ManagedDisk:  &armcompute.ManagedDiskParameters{ID: to.Ptr(diskID)},
				}},
			},
			NetworkProfile: &armcompute.NetworkProfile{
				NetworkInterfaces: []*armcompute.NetworkInterfaceReference{{ID: to.Ptr(nicID)}},
			},
		},
	}, nil)
	require.NoError(t, err)
	_, err = poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond})
	require.NoError(t, err)
}

func publicIPID(rg, name string) string {
	return "/subscriptions/" + subID + "/resourceGroups/" + rg +
		"/providers/Microsoft.Network/publicIPAddresses/" + name
}

// assertGone runs get and requires it to fail with a 404, proving the resource
// no longer exists after the cascade.
func assertGone(t *testing.T, kind string, get func() error) {
	t.Helper()

	err := get()
	if err == nil {
		t.Fatalf("%s survived resource-group delete (no cascade)", kind)
	}

	if code := statusCode(t, err); code != 404 {
		t.Fatalf("%s after RG delete: status %d, want 404", kind, code)
	}
}
