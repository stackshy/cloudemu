package vpc_test

import (
	"context"
	"testing"

	gcpcompute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

// TestSDKSubnetworkAggregatedList covers the MED finding that subnetworks
// aggregatedList returned zero items. gcloud/Terraform data sources depend on
// it; results must be grouped by the subnet's own region.
func TestSDKSubnetworkAggregatedList(t *testing.T) {
	ts := newGCPNetServer(t)
	ctx := context.Background()

	_, subClient := newNetAndSubnetClients(t, ctx, ts, "vpc-agg")

	insertSubnet2(t, ctx, subClient, "us-central1", "agg-central", "vpc-agg", "10.10.0.0/16")
	insertSubnet2(t, ctx, subClient, "us-east1", "agg-east", "vpc-agg", "10.20.0.0/16")

	it := subClient.AggregatedList(ctx, &computepb.AggregatedListSubnetworksRequest{Project: testProject})

	found := map[string]string{} // subnet name -> scope key

	for {
		pair, err := it.Next()
		if err == iterator.Done {
			break
		}

		if err != nil {
			t.Fatalf("AggregatedList Next: %v", err)
		}

		for _, s := range pair.Value.GetSubnetworks() {
			found[s.GetName()] = pair.Key
		}
	}

	if found["agg-central"] != "regions/us-central1" {
		t.Errorf("agg-central scope=%q want regions/us-central1", found["agg-central"])
	}

	if found["agg-east"] != "regions/us-east1" {
		t.Errorf("agg-east scope=%q want regions/us-east1", found["agg-east"])
	}
}

// TestSDKAddressAggregatedList covers the MED finding that addresses
// aggregatedList returned zero items; results must be grouped by region.
func TestSDKAddressAggregatedList(t *testing.T) {
	ts := newGCPNetServer(t)
	ctx := context.Background()

	client, err := gcpcompute.NewAddressesRESTClient(ctx,
		option.WithEndpoint(ts.URL), option.WithoutAuthentication(), option.WithHTTPClient(ts.Client()))
	if err != nil {
		t.Fatalf("NewAddressesRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	insertAddr(t, ctx, client, "us-central1", "addr-central")
	insertAddr(t, ctx, client, "us-east1", "addr-east")

	it := client.AggregatedList(ctx, &computepb.AggregatedListAddressesRequest{Project: testProject})

	found := map[string]string{}

	for {
		pair, err := it.Next()
		if err == iterator.Done {
			break
		}

		if err != nil {
			t.Fatalf("AggregatedList Next: %v", err)
		}

		for _, a := range pair.Value.GetAddresses() {
			found[a.GetName()] = pair.Key
		}
	}

	if found["addr-central"] != "regions/us-central1" {
		t.Errorf("addr-central scope=%q want regions/us-central1", found["addr-central"])
	}

	if found["addr-east"] != "regions/us-east1" {
		t.Errorf("addr-east scope=%q want regions/us-east1", found["addr-east"])
	}
}

func insertAddr(t *testing.T, ctx context.Context, c *gcpcompute.AddressesClient, region, name string) {
	t.Helper()

	op, err := c.Insert(ctx, &computepb.InsertAddressRequest{
		Project: testProject,
		Region:  region,
		AddressResource: &computepb.Address{
			Name: ptrStr(name),
		},
	})
	if err != nil {
		t.Fatalf("address Insert %s/%s: %v", region, name, err)
	}

	if err := op.Wait(ctx); err != nil {
		t.Fatalf("address Insert wait %s/%s: %v", region, name, err)
	}
}
