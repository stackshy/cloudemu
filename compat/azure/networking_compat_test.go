package azure

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/internal/compat"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// TestAzureNetworkingCompat drives Azure VNet networking resources through the
// real azure-sdk-for-go armnetwork clients against the in-process wire server.
// Azure networking is an ARM control-plane API (Microsoft.Network/*), so the
// clients run over the harness's TLS server with a fake bearer credential,
// pointed at the emulator via a custom cloud.Configuration endpoint.
//
// The Microsoft.Network wire handler routes virtual networks, subnets, network
// security groups, and public IP addresses. Those map onto the portable
// "networking" driver, so operation names match VPC's / GCP VPC's in
// docs/coverage/coverage.json:
//
//	CreateVPC / DescribeVPCs / DeleteVPC          -> virtualNetworks
//	CreateSubnet / DescribeSubnets / DeleteSubnet -> virtualNetworks/subnets
//	CreateSecurityGroup / DescribeSecurityGroups /
//	    DeleteSecurityGroup                       -> networkSecurityGroups
//	AllocateAddress / DescribeAddresses           -> publicIPAddresses
//
// The remaining portable ops (route tables, internet/NAT gateways, network
// ACLs, VPC peering, flow logs, VPC endpoints, elastic-IP association, rule and
// tag mutations) have no Microsoft.Network resource wired into this handler, so
// they are coverage gaps and are not asserted here.
func TestAzureNetworkingCompat(t *testing.T) {
	provider := cloudemu.NewAzure()
	sess := compat.BootAzureTLS(t, azureserver.Drivers{Network: provider.VNet})

	const (
		testSub = "sub-1"
		testRG  = "rg-1"

		vnetName   = "vnet-1"
		subnetName = "subnet-1"
		nsgName    = "nsg-1"
		pipName    = "pip-1"

		vnetCIDR   = "10.0.0.0/16"
		subnetCIDR = "10.0.1.0/24"
	)

	myCloud := cloud.Configuration{
		ActiveDirectoryAuthorityHost: "https://login.microsoftonline.com/",
		Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
			cloud.ResourceManager: {
				Endpoint: sess.Endpoint(),
				Audience: "https://management.azure.com",
			},
		},
	}

	opts := &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Cloud:     myCloud,
			Transport: sess.Transport(),
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
	}

	vnets, err := armnetwork.NewVirtualNetworksClient(testSub, compat.FakeAzureCred(), opts)
	if err != nil {
		t.Fatalf("armnetwork.NewVirtualNetworksClient: %v", err)
	}

	subnets, err := armnetwork.NewSubnetsClient(testSub, compat.FakeAzureCred(), opts)
	if err != nil {
		t.Fatalf("armnetwork.NewSubnetsClient: %v", err)
	}

	nsgs, err := armnetwork.NewSecurityGroupsClient(testSub, compat.FakeAzureCred(), opts)
	if err != nil {
		t.Fatalf("armnetwork.NewSecurityGroupsClient: %v", err)
	}

	pips, err := armnetwork.NewPublicIPAddressesClient(testSub, compat.FakeAzureCred(), opts)
	if err != nil {
		t.Fatalf("armnetwork.NewPublicIPAddressesClient: %v", err)
	}

	ctx := context.Background()

	const svc = "networking"

	pollOpts := &runtime.PollUntilDoneOptions{Frequency: time.Millisecond}

	// virtualNetworks: create -> list -> delete.
	sess.Op(svc, "CreateVPC", func() error {
		poller, perr := vnets.BeginCreateOrUpdate(ctx, testRG, vnetName, armnetwork.VirtualNetwork{
			Location: to.Ptr("eastus"),
			Properties: &armnetwork.VirtualNetworkPropertiesFormat{
				AddressSpace: &armnetwork.AddressSpace{
					AddressPrefixes: []*string{to.Ptr(vnetCIDR)},
				},
			},
		}, nil)
		if perr != nil {
			return perr
		}

		created, perr := poller.PollUntilDone(ctx, pollOpts)
		if perr != nil {
			return perr
		}

		if created.Name == nil || *created.Name != vnetName {
			return fmt.Errorf("CreateVPC name = %v, want %q", created.Name, vnetName)
		}

		return nil
	})

	sess.Op(svc, "DescribeVPCs", func() error {
		found := false

		pager := vnets.NewListPager(testRG, nil)
		for pager.More() {
			page, perr := pager.NextPage(ctx)
			if perr != nil {
				return perr
			}

			for _, v := range page.Value {
				if v.Name != nil && *v.Name == vnetName {
					found = true
				}
			}
		}

		if !found {
			return fmt.Errorf("DescribeVPCs did not return %q", vnetName)
		}

		return nil
	})

	// virtualNetworks/subnets: create -> list -> delete.
	sess.Op(svc, "CreateSubnet", func() error {
		poller, perr := subnets.BeginCreateOrUpdate(ctx, testRG, vnetName, subnetName, armnetwork.Subnet{
			Properties: &armnetwork.SubnetPropertiesFormat{
				AddressPrefix: to.Ptr(subnetCIDR),
			},
		}, nil)
		if perr != nil {
			return perr
		}

		created, perr := poller.PollUntilDone(ctx, pollOpts)
		if perr != nil {
			return perr
		}

		if created.Name == nil || *created.Name != subnetName {
			return fmt.Errorf("CreateSubnet name = %v, want %q", created.Name, subnetName)
		}

		return nil
	})

	sess.Op(svc, "DescribeSubnets", func() error {
		found := false

		pager := subnets.NewListPager(testRG, vnetName, nil)
		for pager.More() {
			page, perr := pager.NextPage(ctx)
			if perr != nil {
				return perr
			}

			for _, s := range page.Value {
				if s.Name != nil && *s.Name == subnetName {
					found = true
				}
			}
		}

		if !found {
			return fmt.Errorf("DescribeSubnets did not return %q", subnetName)
		}

		return nil
	})

	sess.Op(svc, "DeleteSubnet", func() error {
		poller, perr := subnets.BeginDelete(ctx, testRG, vnetName, subnetName, nil)
		if perr != nil {
			return perr
		}

		_, perr = poller.PollUntilDone(ctx, pollOpts)

		return perr
	})

	// networkSecurityGroups: create -> list -> delete.
	sess.Op(svc, "CreateSecurityGroup", func() error {
		poller, perr := nsgs.BeginCreateOrUpdate(ctx, testRG, nsgName, armnetwork.SecurityGroup{
			Location: to.Ptr("eastus"),
		}, nil)
		if perr != nil {
			return perr
		}

		created, perr := poller.PollUntilDone(ctx, pollOpts)
		if perr != nil {
			return perr
		}

		if created.Name == nil || *created.Name != nsgName {
			return fmt.Errorf("CreateSecurityGroup name = %v, want %q", created.Name, nsgName)
		}

		return nil
	})

	sess.Op(svc, "DescribeSecurityGroups", func() error {
		found := false

		pager := nsgs.NewListPager(testRG, nil)
		for pager.More() {
			page, perr := pager.NextPage(ctx)
			if perr != nil {
				return perr
			}

			for _, g := range page.Value {
				if g.Name != nil && *g.Name == nsgName {
					found = true
				}
			}
		}

		if !found {
			return fmt.Errorf("DescribeSecurityGroups did not return %q", nsgName)
		}

		return nil
	})

	sess.Op(svc, "DeleteSecurityGroup", func() error {
		poller, perr := nsgs.BeginDelete(ctx, testRG, nsgName, nil)
		if perr != nil {
			return perr
		}

		_, perr = poller.PollUntilDone(ctx, pollOpts)

		return perr
	})

	// publicIPAddresses: allocate (create) -> describe (list). The handler
	// routes only PUT/GET for public IPs, so there is no ReleaseAddress here.
	sess.Op(svc, "AllocateAddress", func() error {
		poller, perr := pips.BeginCreateOrUpdate(ctx, testRG, pipName, armnetwork.PublicIPAddress{
			Location: to.Ptr("eastus"),
			SKU: &armnetwork.PublicIPAddressSKU{
				Name: to.Ptr(armnetwork.PublicIPAddressSKUNameStandard),
			},
			Properties: &armnetwork.PublicIPAddressPropertiesFormat{
				PublicIPAllocationMethod: to.Ptr(armnetwork.IPAllocationMethodStatic),
			},
		}, nil)
		if perr != nil {
			return perr
		}

		created, perr := poller.PollUntilDone(ctx, pollOpts)
		if perr != nil {
			return perr
		}

		if created.Properties == nil || created.Properties.IPAddress == nil ||
			*created.Properties.IPAddress == "" {
			return fmt.Errorf("AllocateAddress returned empty IP: %+v", created.Properties)
		}

		return nil
	})

	sess.Op(svc, "DescribeAddresses", func() error {
		found := false

		pager := pips.NewListPager(testRG, nil)
		for pager.More() {
			page, perr := pager.NextPage(ctx)
			if perr != nil {
				return perr
			}

			for _, p := range page.Value {
				if p.Name != nil && *p.Name == pipName {
					found = true
				}
			}
		}

		if !found {
			return fmt.Errorf("DescribeAddresses did not return %q", pipName)
		}

		return nil
	})

	// virtualNetworks delete last, after its subnets are gone.
	sess.Op(svc, "DeleteVPC", func() error {
		poller, perr := vnets.BeginDelete(ctx, testRG, vnetName, nil)
		if perr != nil {
			return perr
		}

		_, perr = poller.PollUntilDone(ctx, pollOpts)

		return perr
	})
}
