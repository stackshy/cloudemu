package virtualmachines_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// powerStateFromInstanceView pulls the PowerState/<code> suffix out of a VM's
// instance view statuses via the real SDK.
func powerStateFromInstanceView(t *testing.T, ts *httptest.Server, name string) string {
	t.Helper()

	client := newSDKClient(t, ts)

	view, err := client.InstanceView(context.Background(), "rg-1", name, nil)
	if err != nil {
		t.Fatalf("InstanceView %s: %v", name, err)
	}

	for _, s := range view.Statuses {
		if s.Code == nil {
			continue
		}

		if code := *s.Code; strings.HasPrefix(code, "PowerState/") {
			return strings.TrimPrefix(code, "PowerState/")
		}
	}

	return ""
}

// TestSDKRedeployReimage drives the single-VM redeploy and reimage actions with
// a real armcompute client: both are long-running operations whose pollers must
// complete (no 202 hang) and leave the VM running.
func TestSDKRedeployReimage(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{VirtualMachines: cloudP.VirtualMachines})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	client := newSDKClient(t, ts)
	ctx := context.Background()

	createSDKVM(t, client, "rr-vm")

	// Redeploy a running VM: power-cycle, ends running.
	redeployPoller, err := client.BeginRedeploy(ctx, "rg-1", "rr-vm", nil)
	if err != nil {
		t.Fatalf("BeginRedeploy: %v", err)
	}

	if _, err := redeployPoller.PollUntilDone(ctx,
		&runtime.PollUntilDoneOptions{Frequency: time.Millisecond}); err != nil {
		t.Fatalf("Redeploy poll: %v", err)
	}

	if got := powerStateFromInstanceView(t, ts, "rr-vm"); got != "running" {
		t.Errorf("after redeploy powerState=%q want running", got)
	}

	// Reimage: resets the OS disk (OS-disk-reset fidelity deferred), VM ends
	// running.
	reimagePoller, err := client.BeginReimage(ctx, "rg-1", "rr-vm", nil)
	if err != nil {
		t.Fatalf("BeginReimage: %v", err)
	}

	if _, err := reimagePoller.PollUntilDone(ctx,
		&runtime.PollUntilDoneOptions{Frequency: time.Millisecond}); err != nil {
		t.Fatalf("Reimage poll: %v", err)
	}

	if got := powerStateFromInstanceView(t, ts, "rr-vm"); got != "running" {
		t.Errorf("after reimage powerState=%q want running", got)
	}
}

// TestSDKRedeployReimageFromDeallocated verifies redeploy/reimage bring a
// stopped VM back up to running, matching the real-cloud observable that the
// VM ends powered on.
func TestSDKRedeployReimageFromDeallocated(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{VirtualMachines: cloudP.VirtualMachines})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	client := newSDKClient(t, ts)
	ctx := context.Background()

	createSDKVM(t, client, "rr-dealloc")
	deallocateSDKVM(t, client, "rr-dealloc")

	poller, err := client.BeginRedeploy(ctx, "rg-1", "rr-dealloc", nil)
	if err != nil {
		t.Fatalf("BeginRedeploy: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx,
		&runtime.PollUntilDoneOptions{Frequency: time.Millisecond}); err != nil {
		t.Fatalf("Redeploy poll: %v", err)
	}

	if got := powerStateFromInstanceView(t, ts, "rr-dealloc"); got != "running" {
		t.Errorf("after redeploy of deallocated VM powerState=%q want running", got)
	}
}

// TestSDKRedeployReimageMissingVM verifies redeploy/reimage of a non-existent VM
// surface a 404 rather than a poller hang or a 501.
func TestSDKRedeployReimageMissingVM(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{VirtualMachines: cloudP.VirtualMachines})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	client := newSDKClient(t, ts)
	ctx := context.Background()

	if _, err := client.BeginRedeploy(ctx, "rg-1", "ghost", nil); !isNotFound(err) {
		t.Errorf("BeginRedeploy on missing VM err=%v, want 404", err)
	}

	if _, err := client.BeginReimage(ctx, "rg-1", "ghost", nil); !isNotFound(err) {
		t.Errorf("BeginReimage on missing VM err=%v, want 404", err)
	}
}
