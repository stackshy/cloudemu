package loadbalancer_test

import (
	"context"
	"testing"

	gcpcompute "cloud.google.com/go/compute/apiv1"
	computepb "cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/option"
)

func ptrI32(v int32) *int32 { return &v }

// TestSDKGCPBackendServicePatch reproduces the [BLOCKER] finding that
// compute.backendServices.patch returned 405 (no update path), leaving the
// resource read-only after create.
func TestSDKGCPBackendServicePatch(t *testing.T) {
	ts := newGCPLBServer(t)
	ctx := context.Background()

	client, err := gcpcompute.NewBackendServicesRESTClient(ctx,
		option.WithEndpoint(ts.URL), option.WithoutAuthentication(), option.WithHTTPClient(ts.Client()))
	if err != nil {
		t.Fatalf("client: %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	insertOp, err := client.Insert(ctx, &computepb.InsertBackendServiceRequest{
		Project: testProject,
		BackendServiceResource: &computepb.BackendService{
			Name:                ptrStr("web-bs"),
			Protocol:            ptrStr("HTTP"),
			TimeoutSec:          ptrI32(30),
			LoadBalancingScheme: ptrStr("EXTERNAL_MANAGED"),
			SessionAffinity:     ptrStr("NONE"),
		},
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	if err := insertOp.Wait(ctx); err != nil {
		t.Fatalf("Insert wait: %v", err)
	}

	// Patch must succeed (previously 405) and mutate the resource.
	patchOp, err := client.Patch(ctx, &computepb.PatchBackendServiceRequest{
		Project:        testProject,
		BackendService: "web-bs",
		BackendServiceResource: &computepb.BackendService{
			TimeoutSec:      ptrI32(45),
			SessionAffinity: ptrStr("CLIENT_IP"),
		},
	})
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}

	if err := patchOp.Wait(ctx); err != nil {
		t.Fatalf("Patch wait: %v", err)
	}

	got, err := client.Get(ctx, &computepb.GetBackendServiceRequest{Project: testProject, BackendService: "web-bs"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.GetTimeoutSec() != 45 {
		t.Errorf("timeoutSec = %d, want 45 (patch not applied)", got.GetTimeoutSec())
	}

	if got.GetSessionAffinity() != "CLIENT_IP" {
		t.Errorf("sessionAffinity = %q, want CLIENT_IP", got.GetSessionAffinity())
	}

	// Protocol was not in the patch body, so it must be preserved.
	if got.GetProtocol() != "HTTP" {
		t.Errorf("protocol = %q, want HTTP (patch clobbered an omitted field)", got.GetProtocol())
	}
}

// TestSDKGCPBackendServiceFields reproduces the [HIGH] finding that
// loadBalancingScheme/timeoutSec/sessionAffinity/fingerprint/creationTimestamp
// were all dropped on get.
func TestSDKGCPBackendServiceFields(t *testing.T) {
	ts := newGCPLBServer(t)
	ctx := context.Background()

	client, err := gcpcompute.NewBackendServicesRESTClient(ctx,
		option.WithEndpoint(ts.URL), option.WithoutAuthentication(), option.WithHTTPClient(ts.Client()))
	if err != nil {
		t.Fatalf("client: %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	op, err := client.Insert(ctx, &computepb.InsertBackendServiceRequest{
		Project: testProject,
		BackendServiceResource: &computepb.BackendService{
			Name:                ptrStr("full-bs"),
			Protocol:            ptrStr("HTTP"),
			LoadBalancingScheme: ptrStr("INTERNAL_MANAGED"),
			SessionAffinity:     ptrStr("CLIENT_IP"),
			TimeoutSec:          ptrI32(25),
		},
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	if err := op.Wait(ctx); err != nil {
		t.Fatalf("Insert wait: %v", err)
	}

	got, err := client.Get(ctx, &computepb.GetBackendServiceRequest{Project: testProject, BackendService: "full-bs"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.GetLoadBalancingScheme() != "INTERNAL_MANAGED" {
		t.Errorf("loadBalancingScheme = %q, want INTERNAL_MANAGED", got.GetLoadBalancingScheme())
	}

	if got.GetSessionAffinity() != "CLIENT_IP" {
		t.Errorf("sessionAffinity = %q, want CLIENT_IP", got.GetSessionAffinity())
	}

	if got.GetTimeoutSec() != 25 {
		t.Errorf("timeoutSec = %d, want 25", got.GetTimeoutSec())
	}

	if got.GetFingerprint() == "" {
		t.Error("fingerprint empty; blocks all future patches")
	}

	if got.GetCreationTimestamp() == "" {
		t.Error("creationTimestamp empty")
	}
}

// TestSDKGCPBackendServiceDuplicate reproduces the #643-deferred portion: a
// duplicate-name insert must fail with 409 alreadyExists, not silently succeed.
func TestSDKGCPBackendServiceDuplicate(t *testing.T) {
	ts := newGCPLBServer(t)
	ctx := context.Background()

	client, err := gcpcompute.NewBackendServicesRESTClient(ctx,
		option.WithEndpoint(ts.URL), option.WithoutAuthentication(), option.WithHTTPClient(ts.Client()))
	if err != nil {
		t.Fatalf("client: %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	insert := func() error {
		op, err := client.Insert(ctx, &computepb.InsertBackendServiceRequest{
			Project:                testProject,
			BackendServiceResource: &computepb.BackendService{Name: ptrStr("dup-bs"), Protocol: ptrStr("TCP")},
		})
		if err != nil {
			return err
		}

		return op.Wait(ctx)
	}

	if err := insert(); err != nil {
		t.Fatalf("first insert: %v", err)
	}

	if err := insert(); err == nil {
		t.Fatal("second insert of duplicate name: want error, got nil")
	}
}
