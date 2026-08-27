package compute_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	gcpcompute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

// testRegion is the region enclosing testZone; reserved addresses are regional.
const testRegion = "us-central1"

// newGCPServerWithNet builds an in-process GCP server backed by a fresh GCP
// cloud with both compute and networking wired, so instances and reserved
// addresses share one backend — the setup the external-IP linkage needs.
func newGCPServerWithNet(t *testing.T) *httptest.Server {
	t.Helper()

	cloudP := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.Drivers{Compute: cloudP.GCE, Networking: cloudP.VPC})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	return ts
}

func newAddressesSDKClient(t *testing.T, ts *httptest.Server) *gcpcompute.AddressesClient {
	t.Helper()

	ctx := context.Background()

	client, err := gcpcompute.NewAddressesRESTClient(ctx,
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
		option.WithHTTPClient(ts.Client()),
	)
	if err != nil {
		t.Fatalf("NewAddressesRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	return client
}

// reserveAddress reserves a regional address and returns its allocated IP.
func reserveAddress(t *testing.T, c *gcpcompute.AddressesClient, name string) string {
	t.Helper()

	ctx := context.Background()

	op, err := c.Insert(ctx, &computepb.InsertAddressRequest{
		Project: testProject, Region: testRegion,
		AddressResource: &computepb.Address{Name: ptrStr(name)},
	})
	if err != nil {
		t.Fatalf("address Insert: %v", err)
	}

	if err := op.Wait(ctx); err != nil {
		t.Fatalf("address Insert wait: %v", err)
	}

	got, err := c.Get(ctx, &computepb.GetAddressRequest{
		Project: testProject, Region: testRegion, Address: name,
	})
	if err != nil {
		t.Fatalf("address Get: %v", err)
	}

	if got.GetAddress() == "" {
		t.Fatalf("reserved address %s has no allocated IP", name)
	}

	return got.GetAddress()
}

// insertInstanceWithAccessConfig creates an instance whose nic0 carries a single
// accessConfig. natIP is set only when non-empty (a reserved address); an empty
// natIP exercises the ephemeral-IP synthesis path.
func insertInstanceWithAccessConfig(t *testing.T, c *gcpcompute.InstancesClient, name, natIP string) {
	t.Helper()

	ctx := context.Background()

	ac := &computepb.AccessConfig{}
	if natIP != "" {
		ac.NatIP = ptrStr(natIP)
	}

	op, err := c.Insert(ctx, &computepb.InsertInstanceRequest{
		Project: testProject, Zone: testZone,
		InstanceResource: &computepb.Instance{
			Name:        ptrStr(name),
			MachineType: ptrStr("zones/" + testZone + "/machineTypes/n1-standard-1"),
			NetworkInterfaces: []*computepb.NetworkInterface{{
				Network:       ptrStr("global/networks/default"),
				AccessConfigs: []*computepb.AccessConfig{ac},
			}},
		},
	})
	if err != nil {
		t.Fatalf("instance Insert: %v", err)
	}

	if err := op.Wait(ctx); err != nil {
		t.Fatalf("instance Insert wait: %v", err)
	}
}

func firstAccessConfig(t *testing.T, inst *computepb.Instance) *computepb.AccessConfig {
	t.Helper()

	if len(inst.GetNetworkInterfaces()) == 0 {
		t.Fatalf("instance %s has no networkInterfaces", inst.GetName())
	}

	acs := inst.GetNetworkInterfaces()[0].GetAccessConfigs()
	if len(acs) == 0 {
		t.Fatalf("instance %s nic0 has no accessConfigs", inst.GetName())
	}

	return acs[0]
}

// TestSDKReservedAddressReflectedAndInUse proves an accessConfig natIP pointing
// at a reserved address round-trips on the instance and flips that address to
// IN_USE with users[] naming the instance (test a).
func TestSDKReservedAddressReflectedAndInUse(t *testing.T) {
	ts := newGCPServerWithNet(t)
	instances := newSDKInstancesClient(t, ts)
	addresses := newAddressesSDKClient(t, ts)
	ctx := context.Background()

	ip := reserveAddress(t, addresses, "web-ip")
	insertInstanceWithAccessConfig(t, instances, "web-vm", ip)

	inst, err := instances.Get(ctx, &computepb.GetInstanceRequest{
		Project: testProject, Zone: testZone, Instance: "web-vm",
	})
	if err != nil {
		t.Fatalf("instance Get: %v", err)
	}

	ac := firstAccessConfig(t, inst)
	if ac.GetNatIP() != ip {
		t.Errorf("accessConfig natIP=%q want %q", ac.GetNatIP(), ip)
	}

	if ac.GetType() != "ONE_TO_ONE_NAT" {
		t.Errorf("accessConfig type=%q want ONE_TO_ONE_NAT", ac.GetType())
	}

	if ac.GetName() != "External NAT" {
		t.Errorf("accessConfig name=%q want External NAT", ac.GetName())
	}

	addr, err := addresses.Get(ctx, &computepb.GetAddressRequest{
		Project: testProject, Region: testRegion, Address: "web-ip",
	})
	if err != nil {
		t.Fatalf("address Get: %v", err)
	}

	if addr.GetStatus() != "IN_USE" {
		t.Errorf("address status=%q want IN_USE", addr.GetStatus())
	}

	users := addr.GetUsers()
	if len(users) == 0 || !strings.HasSuffix(users[0], "/instances/web-vm") {
		t.Errorf("address users=%v want one ending /instances/web-vm", users)
	}
}

// TestSDKEphemeralExternalIPSynthesized proves an accessConfig with no natIP
// gets an ephemeral external IP synthesized and reflected on the instance,
// without touching any reserved address (test b).
func TestSDKEphemeralExternalIPSynthesized(t *testing.T) {
	ts := newGCPServerWithNet(t)
	instances := newSDKInstancesClient(t, ts)
	ctx := context.Background()

	insertInstanceWithAccessConfig(t, instances, "ephemeral-vm", "")

	inst, err := instances.Get(ctx, &computepb.GetInstanceRequest{
		Project: testProject, Zone: testZone, Instance: "ephemeral-vm",
	})
	if err != nil {
		t.Fatalf("instance Get: %v", err)
	}

	ac := firstAccessConfig(t, inst)
	if ac.GetNatIP() == "" {
		t.Error("ephemeral accessConfig has no synthesized natIP")
	}

	if ac.GetType() != "ONE_TO_ONE_NAT" {
		t.Errorf("accessConfig type=%q want ONE_TO_ONE_NAT", ac.GetType())
	}
}

// TestSDKDeleteInUseAddressRejected proves a reserved address held by an
// instance cannot be deleted (400 resourceInUseByAnotherResource), and that it
// reverts to RESERVED and deletes cleanly once the instance is gone (test c).
func TestSDKDeleteInUseAddressRejected(t *testing.T) {
	ts := newGCPServerWithNet(t)
	instances := newSDKInstancesClient(t, ts)
	addresses := newAddressesSDKClient(t, ts)
	ctx := context.Background()

	ip := reserveAddress(t, addresses, "pinned-ip")
	insertInstanceWithAccessConfig(t, instances, "holder-vm", ip)

	// Delete while IN_USE must fail.
	_, err := addresses.Delete(ctx, &computepb.DeleteAddressRequest{
		Project: testProject, Region: testRegion, Address: "pinned-ip",
	})
	if err == nil {
		t.Fatal("Delete of in-use address succeeded, want resourceInUseByAnotherResource")
	}

	if !strings.Contains(err.Error(), "resourceInUseByAnotherResource") && !strings.Contains(err.Error(), "400") {
		t.Errorf("Delete error = %v, want resourceInUseByAnotherResource/400", err)
	}

	// Delete the instance releasing the address.
	delOp, err := instances.Delete(ctx, &computepb.DeleteInstanceRequest{
		Project: testProject, Zone: testZone, Instance: "holder-vm",
	})
	if err != nil {
		t.Fatalf("instance Delete: %v", err)
	}

	if err := delOp.Wait(ctx); err != nil {
		t.Fatalf("instance Delete wait: %v", err)
	}

	// Address is back to RESERVED.
	addr, err := addresses.Get(ctx, &computepb.GetAddressRequest{
		Project: testProject, Region: testRegion, Address: "pinned-ip",
	})
	if err != nil {
		t.Fatalf("address Get after instance delete: %v", err)
	}

	if addr.GetStatus() != "RESERVED" {
		t.Errorf("address status=%q want RESERVED after release", addr.GetStatus())
	}

	if len(addr.GetUsers()) != 0 {
		t.Errorf("address users=%v want empty after release", addr.GetUsers())
	}

	// And now it deletes cleanly.
	addrDelOp, err := addresses.Delete(ctx, &computepb.DeleteAddressRequest{
		Project: testProject, Region: testRegion, Address: "pinned-ip",
	})
	if err != nil {
		t.Fatalf("address Delete after release: %v", err)
	}

	if err := addrDelOp.Wait(ctx); err != nil {
		t.Fatalf("address Delete wait: %v", err)
	}
}

// TestSDKInstanceWithoutAccessConfigHasNoExternalIP proves the no-regression
// case: an instance created without an accessConfig has no external IP and its
// nic0 carries no accessConfigs (test d).
func TestSDKInstanceWithoutAccessConfigHasNoExternalIP(t *testing.T) {
	ts := newGCPServerWithNet(t)
	instances := newSDKInstancesClient(t, ts)
	ctx := context.Background()

	op, err := instances.Insert(ctx, &computepb.InsertInstanceRequest{
		Project: testProject, Zone: testZone,
		InstanceResource: &computepb.Instance{
			Name:        ptrStr("private-vm"),
			MachineType: ptrStr("zones/" + testZone + "/machineTypes/n1-standard-1"),
			NetworkInterfaces: []*computepb.NetworkInterface{{
				Network: ptrStr("global/networks/default"),
			}},
		},
	})
	if err != nil {
		t.Fatalf("instance Insert: %v", err)
	}

	if err := op.Wait(ctx); err != nil {
		t.Fatalf("instance Insert wait: %v", err)
	}

	inst, err := instances.Get(ctx, &computepb.GetInstanceRequest{
		Project: testProject, Zone: testZone, Instance: "private-vm",
	})
	if err != nil {
		t.Fatalf("instance Get: %v", err)
	}

	if len(inst.GetNetworkInterfaces()) == 0 {
		t.Fatal("private-vm has no networkInterfaces")
	}

	if acs := inst.GetNetworkInterfaces()[0].GetAccessConfigs(); len(acs) != 0 {
		t.Errorf("private-vm nic0 has %d accessConfigs, want 0", len(acs))
	}
}
