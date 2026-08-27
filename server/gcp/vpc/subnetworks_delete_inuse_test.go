package vpc_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	gcpcompute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/option"
)

// zoneInRegion is a zone belonging to testRegion (us-central1); instances live
// in zones while subnets are regional, so an in-region zone puts the instance in
// the subnet under test.
const zoneInRegion = "us-central1-a"

func newSubnetsClient(t *testing.T, ts *httptest.Server) *gcpcompute.SubnetworksClient {
	t.Helper()

	c, err := gcpcompute.NewSubnetworksRESTClient(context.Background(),
		option.WithEndpoint(ts.URL), option.WithoutAuthentication(), option.WithHTTPClient(ts.Client()))
	if err != nil {
		t.Fatalf("NewSubnetworksRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = c.Close() })

	return c
}

func newInstancesClient(t *testing.T, ts *httptest.Server) *gcpcompute.InstancesClient {
	t.Helper()

	c, err := gcpcompute.NewInstancesRESTClient(context.Background(),
		option.WithEndpoint(ts.URL), option.WithoutAuthentication(), option.WithHTTPClient(ts.Client()))
	if err != nil {
		t.Fatalf("NewInstancesRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = c.Close() })

	return c
}

func newNetworksClient(t *testing.T, ts *httptest.Server) *gcpcompute.NetworksClient {
	t.Helper()

	c, err := gcpcompute.NewNetworksRESTClient(context.Background(),
		option.WithEndpoint(ts.URL), option.WithoutAuthentication(), option.WithHTTPClient(ts.Client()))
	if err != nil {
		t.Fatalf("NewNetworksRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = c.Close() })

	return c
}

func insertNetworkNamed(t *testing.T, ctx context.Context, c *gcpcompute.NetworksClient, name string) {
	t.Helper()

	op, err := c.Insert(ctx, &computepb.InsertNetworkRequest{
		Project: testProject,
		NetworkResource: &computepb.Network{
			Name:                  ptrStr(name),
			AutoCreateSubnetworks: func() *bool { b := false; return &b }(),
		},
	})
	if err != nil {
		t.Fatalf("network Insert %s: %v", name, err)
	}

	if err := op.Wait(ctx); err != nil {
		t.Fatalf("network Insert wait %s: %v", name, err)
	}
}

func insertInstanceInSubnet(t *testing.T, ctx context.Context, c *gcpcompute.InstancesClient, name, subnetRef string) {
	t.Helper()

	op, err := c.Insert(ctx, &computepb.InsertInstanceRequest{
		Project: testProject, Zone: zoneInRegion,
		InstanceResource: &computepb.Instance{
			Name:        ptrStr(name),
			MachineType: ptrStr("zones/" + zoneInRegion + "/machineTypes/n1-standard-1"),
			NetworkInterfaces: []*computepb.NetworkInterface{
				{Subnetwork: ptrStr(subnetRef)},
			},
		},
	})
	if err != nil {
		t.Fatalf("instance Insert %s: %v", name, err)
	}

	if err := op.Wait(ctx); err != nil {
		t.Fatalf("instance Insert wait %s: %v", name, err)
	}
}

// TestSDKDeleteSubnetInUseByInstanceRejected proves deleting a subnetwork that
// still has an instance in it is rejected with 400
// resourceInUseByAnotherResource, and that the delete succeeds once the instance
// is gone.
func TestSDKDeleteSubnetInUseByInstanceRejected(t *testing.T) {
	ts := newGCPNetServer(t)
	ctx := context.Background()

	nets := newNetworksClient(t, ts)
	subs := newSubnetsClient(t, ts)
	instances := newInstancesClient(t, ts)

	insertNetworkNamed(t, ctx, nets, "app-vpc")
	insertSubnet2(t, ctx, subs, testRegion, "app-subnet", "app-vpc", "10.5.0.0/16")

	subnetRef := "projects/" + testProject + "/regions/" + testRegion + "/subnetworks/app-subnet"
	insertInstanceInSubnet(t, ctx, instances, "app-vm", subnetRef)

	// Delete while an instance is in the subnet must fail.
	_, err := subs.Delete(ctx, &computepb.DeleteSubnetworkRequest{
		Project: testProject, Region: testRegion, Subnetwork: "app-subnet",
	})
	if err == nil {
		t.Fatal("Delete of in-use subnet succeeded, want 400 resourceInUseByAnotherResource")
	}

	if !strings.Contains(err.Error(), "resourceInUseByAnotherResource") && !strings.Contains(err.Error(), "400") {
		t.Errorf("Delete error = %v, want resourceInUseByAnotherResource/400", err)
	}

	// Subnet survived the rejected delete.
	if _, err := subs.Get(ctx, &computepb.GetSubnetworkRequest{
		Project: testProject, Region: testRegion, Subnetwork: "app-subnet",
	}); err != nil {
		t.Fatalf("Get after rejected delete: %v (subnet should still exist)", err)
	}

	// Remove the instance, then delete must succeed.
	delInst, err := instances.Delete(ctx, &computepb.DeleteInstanceRequest{
		Project: testProject, Zone: zoneInRegion, Instance: "app-vm",
	})
	if err != nil {
		t.Fatalf("instance Delete: %v", err)
	}

	if err := delInst.Wait(ctx); err != nil {
		t.Fatalf("instance Delete wait: %v", err)
	}

	delSub, err := subs.Delete(ctx, &computepb.DeleteSubnetworkRequest{
		Project: testProject, Region: testRegion, Subnetwork: "app-subnet",
	})
	if err != nil {
		t.Fatalf("Delete after instance removed: %v (should succeed)", err)
	}

	if err := delSub.Wait(ctx); err != nil {
		t.Fatalf("Delete after instance removed wait: %v", err)
	}
}

// TestSDKDeleteEmptySubnetSucceeds proves the in-use guard does not block an
// empty subnet (no instances) from being deleted.
func TestSDKDeleteEmptySubnetSucceeds(t *testing.T) {
	ts := newGCPNetServer(t)
	ctx := context.Background()

	nets := newNetworksClient(t, ts)
	subs := newSubnetsClient(t, ts)

	insertNetworkNamed(t, ctx, nets, "empty-vpc")
	insertSubnet2(t, ctx, subs, testRegion, "empty-subnet", "empty-vpc", "10.6.0.0/16")

	op, err := subs.Delete(ctx, &computepb.DeleteSubnetworkRequest{
		Project: testProject, Region: testRegion, Subnetwork: "empty-subnet",
	})
	if err != nil {
		t.Fatalf("Delete of empty subnet: %v", err)
	}

	if err := op.Wait(ctx); err != nil {
		t.Fatalf("Delete wait: %v", err)
	}
}
