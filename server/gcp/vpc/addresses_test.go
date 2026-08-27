package vpc_test

import (
	"context"
	"testing"

	gcpcompute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/option"
)

// TestSDKGlobalAddressAllocatesIP covers the finding that a reserved address
// was stored verbatim with no IP allocated — Get returned status/address/
// selfLink/kind empty and id=0, breaking PSA/VPC_PEERING range reservation.
func TestSDKGlobalAddressAllocatesIP(t *testing.T) {
	ts := newGCPNetServer(t)
	ctx := context.Background()

	client, err := gcpcompute.NewGlobalAddressesRESTClient(ctx,
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
		option.WithHTTPClient(ts.Client()),
	)
	if err != nil {
		t.Fatalf("NewGlobalAddressesRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	insertOp, err := client.Insert(ctx, &computepb.InsertGlobalAddressRequest{
		Project: testProject,
		AddressResource: &computepb.Address{
			Name:         ptrStr("psa-range"),
			Purpose:      ptrStr("VPC_PEERING"),
			AddressType:  ptrStr("INTERNAL"),
			PrefixLength: ptrInt32(16),
		},
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	if err := insertOp.Wait(ctx); err != nil {
		t.Fatalf("Insert wait: %v", err)
	}

	got, err := client.Get(ctx, &computepb.GetGlobalAddressRequest{Project: testProject, Address: "psa-range"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.GetStatus() != "RESERVED" {
		t.Errorf("status=%q want RESERVED", got.GetStatus())
	}

	if got.GetAddress() == "" {
		t.Error("address empty, want an allocated IP")
	}

	if got.GetId() == 0 {
		t.Error("id=0, want a non-zero numeric id")
	}

	if got.GetKind() != "compute#address" || got.GetSelfLink() == "" {
		t.Errorf("kind=%q selfLink=%q want populated", got.GetKind(), got.GetSelfLink())
	}

	// Caller-supplied fields must round-trip.
	if got.GetPurpose() != "VPC_PEERING" || got.GetPrefixLength() != 16 {
		t.Errorf("purpose=%q prefixLength=%d want VPC_PEERING/16", got.GetPurpose(), got.GetPrefixLength())
	}
}
