package virtualmachines_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
	"github.com/stackshy/cloudemu"
	azureserver "github.com/stackshy/cloudemu/server/azure"
)

// fakeCred is a static-token credential for tests. The real ARM endpoint
// requires AAD tokens; our handler ignores the Authorization header, but the
// SDK still demands a credential implementation.
type fakeCred struct{}

func (fakeCred) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fake", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

// newSDKClient builds an azure-sdk-for-go armcompute VirtualMachinesClient
// configured to talk to the given httptest server.
func newSDKClient(t *testing.T, ts *httptest.Server) *armcompute.VirtualMachinesClient {
	t.Helper()

	myCloud := cloud.Configuration{
		ActiveDirectoryAuthorityHost: "https://login.microsoftonline.com/",
		Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
			cloud.ResourceManager: {
				Endpoint: ts.URL,
				Audience: "https://management.azure.com",
			},
		},
	}

	opts := &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Cloud:     myCloud,
			Transport: ts.Client(),
			// SDK wants TLS by default; we're on http httptest. Disable retries
			// so test failures are visible immediately rather than masked.
			Retry: policy.RetryOptions{MaxRetries: -1},
		},
	}

	client, err := armcompute.NewVirtualMachinesClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	return client
}

// TestSDKVMRoundTrip is the load-bearing parity test: a real
// azure-sdk-for-go armcompute client drives the full lifecycle (create →
// get → list → start/stop/restart → delete) against our handler and
// receives well-shaped responses for every step.
//
// The Azure SDK refuses bearer-token credentials over plain HTTP, so this
// test spins up a TLS httptest server. The SDK uses ts.Client() which
// trusts the test server's self-signed cert.
func TestSDKVMRoundTrip(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{VirtualMachines: cloudP.VirtualMachines})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	client := newSDKClient(t, ts)
	ctx := context.Background()

	// CreateOrUpdate is a long-running operation in real ARM. Our handler
	// returns 200 with the body immediately; the SDK's poller treats any
	// terminal status as DONE on the first poll.
	poller, err := client.BeginCreateOrUpdate(ctx, "rg-1", "sdk-vm",
		armcompute.VirtualMachine{
			Location: to.Ptr("eastus"),
			Properties: &armcompute.VirtualMachineProperties{
				HardwareProfile: &armcompute.HardwareProfile{
					VMSize: to.Ptr(armcompute.VirtualMachineSizeTypesStandardD2SV3),
				},
				StorageProfile: &armcompute.StorageProfile{
					ImageReference: &armcompute.ImageReference{
						Publisher: to.Ptr("Canonical"),
						Offer:     to.Ptr("UbuntuServer"),
						SKU:       to.Ptr("22.04-LTS"),
						Version:   to.Ptr("latest"),
					},
				},
				OSProfile: &armcompute.OSProfile{
					ComputerName:  to.Ptr("sdk-vm"),
					AdminUsername: to.Ptr("azureuser"),
				},
			},
		}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}

	created, err := pollUntilDone(ctx, poller)
	if err != nil {
		t.Fatalf("CreateOrUpdate poll: %v", err)
	}

	if created.Name == nil || *created.Name != "sdk-vm" {
		t.Errorf("created.Name=%v want sdk-vm", created.Name)
	}

	if created.Properties == nil || created.Properties.ProvisioningState == nil ||
		*created.Properties.ProvisioningState != "Succeeded" {
		t.Errorf("provisioningState=%v want Succeeded", created.Properties.ProvisioningState)
	}

	// Get the VM by name.
	got, err := client.Get(ctx, "rg-1", "sdk-vm", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Name == nil || *got.Name != "sdk-vm" {
		t.Errorf("got.Name=%v", got.Name)
	}

	// List in the resource group; we should see the VM we created.
	pager := client.NewListPager("rg-1", nil)

	found := false
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("NextPage: %v", err)
		}

		for _, vm := range page.Value {
			if vm.Name != nil && *vm.Name == "sdk-vm" {
				found = true
			}
		}
	}

	if !found {
		t.Error("List did not return sdk-vm")
	}

	// Lifecycle: Stop, Start, Restart should all succeed via the SDK.
	powerOff, err := client.BeginPowerOff(ctx, "rg-1", "sdk-vm", nil)
	if err != nil {
		t.Fatalf("BeginPowerOff: %v", err)
	}

	if _, err := powerOff.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond}); err != nil {
		t.Errorf("PowerOff poll: %v", err)
	}

	startPoller, err := client.BeginStart(ctx, "rg-1", "sdk-vm", nil)
	if err != nil {
		t.Fatalf("BeginStart: %v", err)
	}

	if _, err := startPoller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond}); err != nil {
		t.Errorf("Start poll: %v", err)
	}

	restartPoller, err := client.BeginRestart(ctx, "rg-1", "sdk-vm", nil)
	if err != nil {
		t.Fatalf("BeginRestart: %v", err)
	}

	if _, err := restartPoller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond}); err != nil {
		t.Errorf("Restart poll: %v", err)
	}

	deletePoller, err := client.BeginDelete(ctx, "rg-1", "sdk-vm", nil)
	if err != nil {
		t.Fatalf("BeginDelete: %v", err)
	}

	if _, err := deletePoller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond}); err != nil {
		t.Errorf("Delete poll: %v", err)
	}
}

// pollUntilDone runs a CreateOrUpdate poller to completion and returns the VM.
func pollUntilDone(ctx context.Context,
	p *runtime.Poller[armcompute.VirtualMachinesClientCreateOrUpdateResponse],
) (armcompute.VirtualMachine, error) {
	resp, err := p.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond})
	if err != nil {
		return armcompute.VirtualMachine{}, err
	}

	return resp.VirtualMachine, nil
}
