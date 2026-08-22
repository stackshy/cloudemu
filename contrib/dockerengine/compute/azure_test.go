package compute_test

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/contrib/dockerengine/compute"
	"github.com/stackshy/cloudemu/v2/contrib/dockerengine/internal/dtest"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// TestComputeAzureVMBootDiagnosticsE2E runs the exact flow a real user runs against
// Azure: create a VM with the armcompute SDK, passing a boot script via
// osProfile.customData, then call RetrieveBootDiagnosticsData, download the serial
// log the returned URI points at, and assert it contains the marker the boot script
// echoed — proving a real container actually ran the boot script — all against
// CloudEmu backed by a real Docker container (no cloud account). Then delete the VM
// and assert the serial log no longer surfaces the marker.
//
// The Azure SDK refuses bearer-token credentials over plain HTTP, so this test uses
// a TLS httptest server; the SDK (and our manual blob GET) use ts.Client(), which
// trusts the test server's self-signed cert.
func TestComputeAzureVMBootDiagnosticsE2E(t *testing.T) {
	if !dtest.DockerUp() {
		t.Skip("docker daemon not available")
	}

	eng := compute.New()
	t.Cleanup(func() { _ = eng.Close() })

	cloud := cloudemu.NewAzure(config.WithComputeEngine(eng))
	ts := httptest.NewTLSServer(azureserver.New(azureserver.Drivers{VirtualMachines: cloud.VirtualMachines}))
	t.Cleanup(ts.Close)

	client, err := armcompute.NewVirtualMachinesClient("sub-1", dtest.FakeCred{}, dtest.ARMOptions(ts))
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	ctx := context.Background()

	const (
		marker = "cloudemu-azvm-marker-42"
		rg     = "rg-1"
		vmName = "az-vm"
	)

	script := "#!/bin/sh\necho " + marker
	customData := base64.StdEncoding.EncodeToString([]byte(script))

	// 1. Create the VM — exactly like `az vm create`, boot script in customData.
	poller, err := client.BeginCreateOrUpdate(ctx, rg, vmName, armcompute.VirtualMachine{
		Location: to.Ptr("eastus"),
		Properties: &armcompute.VirtualMachineProperties{
			HardwareProfile: &armcompute.HardwareProfile{
				VMSize: to.Ptr(armcompute.VirtualMachineSizeTypesStandardD2SV3),
			},
			OSProfile: &armcompute.OSProfile{
				ComputerName:  to.Ptr(vmName),
				AdminUsername: to.Ptr("azureuser"),
				CustomData:    to.Ptr(customData),
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond}); err != nil {
		t.Fatalf("CreateOrUpdate poll: %v", err)
	}

	// 2. Retrieve boot diagnostics — returns the serial-log blob URI.
	diag, err := client.RetrieveBootDiagnosticsData(ctx, rg, vmName, nil)
	if err != nil {
		t.Fatalf("RetrieveBootDiagnosticsData: %v", err)
	}

	if diag.SerialConsoleLogBlobURI == nil {
		t.Fatal("no serialConsoleLogBlobUri returned")
	}

	// 3. Download the serial log the URI points at — the real container's boot output.
	if got := fetchSerialLog(t, ts, *diag.SerialConsoleLogBlobURI); !strings.Contains(got, marker) {
		t.Fatalf("serial log missing marker %q: got %q", marker, got)
	}

	// 4. Delete the VM — the real container is torn down.
	delPoller, err := client.BeginDelete(ctx, rg, vmName, nil)
	if err != nil {
		t.Fatalf("BeginDelete: %v", err)
	}

	if _, err := delPoller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond}); err != nil {
		t.Fatalf("Delete poll: %v", err)
	}

	// 5. Serial log for the torn-down VM no longer surfaces the marker.
	after, err := client.RetrieveBootDiagnosticsData(ctx, rg, vmName, nil)
	if err == nil && after.SerialConsoleLogBlobURI != nil {
		if got := fetchSerialLog(t, ts, *after.SerialConsoleLogBlobURI); strings.Contains(got, marker) {
			t.Fatalf("serial log still surfaces marker after delete: %q", got)
		}
	}
}

// fetchSerialLog downloads the serial-log blob at uri using the TLS test server's
// client (which trusts its self-signed cert) and returns the body as a string.
func fetchSerialLog(t *testing.T, ts *httptest.Server, uri string) string {
	t.Helper()

	resp, err := ts.Client().Get(uri)
	if err != nil {
		t.Fatalf("GET serial log: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("serial log status = %d", resp.StatusCode)
	}

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read serial log: %v", err)
	}

	return string(b)
}
