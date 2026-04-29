package compute_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	gcpcompute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu"
	gcpserver "github.com/stackshy/cloudemu/server/gcp"
)

// newSDKInstancesClient builds a real google-cloud-go InstancesRESTClient
// pointing at the given test server. Authentication is disabled — our
// handler ignores credential headers, and the SDK is happy without an
// Application Default Credential when WithoutAuthentication is set.
func newSDKInstancesClient(t *testing.T, ts *httptest.Server) *gcpcompute.InstancesClient {
	t.Helper()

	ctx := context.Background()

	client, err := gcpcompute.NewInstancesRESTClient(ctx,
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
		option.WithHTTPClient(ts.Client()),
	)
	if err != nil {
		t.Fatalf("NewInstancesRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	return client
}

// TestSDKGCEInstanceRoundTrip drives the full instance lifecycle (insert →
// get → list → start/stop/reset → delete) using a real cloud.google.com/go
// InstancesClient so we know the wire shapes are SDK-compatible end to end.
func TestSDKGCEInstanceRoundTrip(t *testing.T) {
	cloudP := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.Drivers{Compute: cloudP.GCE})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	client := newSDKInstancesClient(t, ts)
	ctx := context.Background()

	insertOp, err := client.Insert(ctx, &computepb.InsertInstanceRequest{
		Project: testProject,
		Zone:    testZone,
		InstanceResource: &computepb.Instance{
			Name:        ptrStr("sdk-vm"),
			MachineType: ptrStr("zones/" + testZone + "/machineTypes/n1-standard-1"),
			Disks: []*computepb.AttachedDisk{{
				Boot:       ptrBool(true),
				AutoDelete: ptrBool(true),
				InitializeParams: &computepb.AttachedDiskInitializeParams{
					SourceImage: ptrStr("projects/debian-cloud/global/images/family/debian-12"),
				},
			}},
			NetworkInterfaces: []*computepb.NetworkInterface{
				{Network: ptrStr("global/networks/default")},
			},
		},
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	if err := insertOp.Wait(ctx); err != nil {
		t.Fatalf("Insert wait: %v", err)
	}

	got, err := client.Get(ctx, &computepb.GetInstanceRequest{
		Project: testProject, Zone: testZone, Instance: "sdk-vm",
	})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.GetName() != "sdk-vm" {
		t.Errorf("name=%s want sdk-vm", got.GetName())
	}

	if !strings.HasSuffix(got.GetMachineType(), "/machineTypes/n1-standard-1") {
		t.Errorf("machineType=%s", got.GetMachineType())
	}

	// List in the zone — we should see our VM.
	it := client.List(ctx, &computepb.ListInstancesRequest{
		Project: testProject, Zone: testZone,
	})

	found := false
	for {
		inst, err := it.Next()
		if err != nil {
			break
		}

		if inst.GetName() == "sdk-vm" {
			found = true
		}
	}

	if !found {
		t.Error("List did not return sdk-vm")
	}

	// Lifecycle ops.
	stopOp, err := client.Stop(ctx, &computepb.StopInstanceRequest{
		Project: testProject, Zone: testZone, Instance: "sdk-vm",
	})
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}

	if err := stopOp.Wait(ctx); err != nil {
		t.Errorf("Stop wait: %v", err)
	}

	startOp, err := client.Start(ctx, &computepb.StartInstanceRequest{
		Project: testProject, Zone: testZone, Instance: "sdk-vm",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err := startOp.Wait(ctx); err != nil {
		t.Errorf("Start wait: %v", err)
	}

	resetOp, err := client.Reset(ctx, &computepb.ResetInstanceRequest{
		Project: testProject, Zone: testZone, Instance: "sdk-vm",
	})
	if err != nil {
		t.Fatalf("Reset: %v", err)
	}

	if err := resetOp.Wait(ctx); err != nil {
		t.Errorf("Reset wait: %v", err)
	}

	delOp, err := client.Delete(ctx, &computepb.DeleteInstanceRequest{
		Project: testProject, Zone: testZone, Instance: "sdk-vm",
	})
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if err := delOp.Wait(ctx); err != nil {
		t.Errorf("Delete wait: %v", err)
	}
}

// ptr helpers — computepb fields are pointers because the protocol uses
// proto3-with-presence and the SDK marshalers care about the distinction
// between unset and zero-value.
func ptrStr(s string) *string { return &s }
func ptrBool(b bool) *bool    { return &b }
