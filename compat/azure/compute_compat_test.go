package azure

import (
	"context"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/internal/compat"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// TestAzureComputeCompat drives an Azure VM lifecycle through the real
// azure-sdk-for-go armcompute client against CloudEmu's in-process wire server.
// Operation names match the portable "compute" driver in coverage.json, whose
// Azure native surface is VirtualMachines: BeginCreateOrUpdate → RunInstances,
// Get/List → DescribeInstances, BeginStart → StartInstances, BeginPowerOff →
// StopInstances, BeginRestart → RebootInstances, BeginDelete →
// TerminateInstances.
//
// ARM is a control plane whose SDK refuses bearer tokens over plaintext, so the
// harness boots a TLS server (BootAzureTLS) and pairs a static credential
// (FakeAzureCred) with the test server transport.
func TestAzureComputeCompat(t *testing.T) {
	const (
		svc  = "compute"
		rg   = "rg-compat"
		vmNm = "compat-vm"
		loc  = "eastus"
	)

	provider := cloudemu.NewAzure()
	sess := compat.BootAzureTLS(t, azureserver.Drivers{VirtualMachines: provider.VirtualMachines})
	ctx := context.Background()

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

	client, err := armcompute.NewVirtualMachinesClient("sub-compat", compat.FakeAzureCred(), opts)
	if err != nil {
		t.Fatalf("NewVirtualMachinesClient: %v", err)
	}

	pollOpts := &runtime.PollUntilDoneOptions{Frequency: time.Millisecond}

	sess.Op(svc, "RunInstances", func() error {
		poller, err := client.BeginCreateOrUpdate(ctx, rg, vmNm, armcompute.VirtualMachine{
			Location: to.Ptr(loc),
			Properties: &armcompute.VirtualMachineProperties{
				HardwareProfile: &armcompute.HardwareProfile{
					VMSize: to.Ptr(armcompute.VirtualMachineSizeTypesStandardD2SV3),
				},
				OSProfile: &armcompute.OSProfile{
					ComputerName:  to.Ptr(vmNm),
					AdminUsername: to.Ptr("azureuser"),
				},
			},
		}, nil)
		if err != nil {
			return err
		}

		_, err = poller.PollUntilDone(ctx, pollOpts)

		return err
	})

	// Get and List both exercise the portable DescribeInstances read path.
	sess.Op(svc, "DescribeInstances", func() error {
		if _, err := client.Get(ctx, rg, vmNm, nil); err != nil {
			return err
		}

		pager := client.NewListPager(rg, nil)
		for pager.More() {
			if _, err := pager.NextPage(ctx); err != nil {
				return err
			}
		}

		return nil
	})

	// The VM is created running, so power it off before starting it again.
	sess.Op(svc, "StopInstances", func() error {
		poller, err := client.BeginPowerOff(ctx, rg, vmNm, nil)
		if err != nil {
			return err
		}

		_, err = poller.PollUntilDone(ctx, pollOpts)

		return err
	})

	sess.Op(svc, "StartInstances", func() error {
		poller, err := client.BeginStart(ctx, rg, vmNm, nil)
		if err != nil {
			return err
		}

		_, err = poller.PollUntilDone(ctx, pollOpts)

		return err
	})

	sess.Op(svc, "RebootInstances", func() error {
		poller, err := client.BeginRestart(ctx, rg, vmNm, nil)
		if err != nil {
			return err
		}

		_, err = poller.PollUntilDone(ctx, pollOpts)

		return err
	})

	sess.Op(svc, "TerminateInstances", func() error {
		poller, err := client.BeginDelete(ctx, rg, vmNm, nil)
		if err != nil {
			return err
		}

		_, err = poller.PollUntilDone(ctx, pollOpts)

		return err
	})
}
