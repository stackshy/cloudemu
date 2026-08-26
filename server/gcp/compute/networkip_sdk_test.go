package compute_test

import (
	"context"
	"net"
	"net/http/httptest"
	"testing"

	gcpcompute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/option"
)

// TestSDKInstanceServiceAccountsRoundTrip proves an instance created with an
// explicit service_account (email + scopes) reflects that exact block on GET,
// while an instance created without one falls back to the default compute
// service account real GCP attaches.
func TestSDKInstanceServiceAccountsRoundTrip(t *testing.T) {
	ts := newGCPServerWithNet(t)
	client := newSDKInstancesClient(t, ts)

	email := "sa@p-1.iam.gserviceaccount.com"
	scopes := []string{
		"https://www.googleapis.com/auth/devstorage.read_only",
		"https://www.googleapis.com/auth/logging.write",
	}

	insertBasicInstance(t, client, "sa-vm", &computepb.NetworkInterface{
		Network: ptrStr("global/networks/default"),
	}, []*computepb.ServiceAccount{{Email: ptrStr(email), Scopes: scopes}})

	got := getSDKInstance(t, client, "sa-vm")

	sas := got.GetServiceAccounts()
	if len(sas) != 1 {
		t.Fatalf("sa-vm has %d serviceAccounts, want 1", len(sas))
	}

	if sas[0].GetEmail() != email {
		t.Errorf("serviceAccount email=%q want %q", sas[0].GetEmail(), email)
	}

	if len(sas[0].GetScopes()) != 2 || sas[0].GetScopes()[0] != scopes[0] || sas[0].GetScopes()[1] != scopes[1] {
		t.Errorf("serviceAccount scopes=%v want %v", sas[0].GetScopes(), scopes)
	}

	// An instance without a service_account gets the default compute SA.
	insertBasicInstance(t, client, "default-sa-vm", &computepb.NetworkInterface{
		Network: ptrStr("global/networks/default"),
	}, nil)

	def := getSDKInstance(t, client, "default-sa-vm")

	dsas := def.GetServiceAccounts()
	if len(dsas) != 1 || dsas[0].GetEmail() != "default" {
		t.Fatalf("default-sa-vm serviceAccounts=%v want single email=default", dsas)
	}

	if len(dsas[0].GetScopes()) != 1 || dsas[0].GetScopes()[0] != "https://www.googleapis.com/auth/cloud-platform" {
		t.Errorf("default SA scopes=%v want [cloud-platform]", dsas[0].GetScopes())
	}
}

// TestSDKInstanceNetworkIPFromSubnetCIDR proves an instance launched into a
// subnetwork gets a private networkIP inside that subnet's ipCidrRange (past
// the reserved low addresses), a second instance gets a distinct in-range IP,
// an explicit networkIP is honored verbatim, and an instance in no subnet
// falls back cleanly to the synthetic allocator.
func TestSDKInstanceNetworkIPFromSubnetCIDR(t *testing.T) {
	ts := newGCPServerWithNet(t)
	client := newSDKInstancesClient(t, ts)

	const cidr = "10.20.0.0/24"

	createSubnet(t, ts, "vpc-ip", "sub-ip", cidr)

	subRef := "projects/" + testProject + "/regions/" + testRegion + "/subnetworks/sub-ip"

	insertBasicInstance(t, client, "sub-vm-1", &computepb.NetworkInterface{
		Subnetwork: ptrStr(subRef),
	}, nil)
	insertBasicInstance(t, client, "sub-vm-2", &computepb.NetworkInterface{
		Subnetwork: ptrStr(subRef),
	}, nil)

	ip1 := getSDKInstanceNetworkIP(t, client, "sub-vm-1")
	ip2 := getSDKInstanceNetworkIP(t, client, "sub-vm-2")

	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		t.Fatalf("ParseCIDR: %v", err)
	}

	assertInRangePastReserved(t, ipnet, ip1)
	assertInRangePastReserved(t, ipnet, ip2)

	if ip1 == ip2 {
		t.Errorf("sub-vm-1 and sub-vm-2 share networkIP %s, want distinct", ip1)
	}

	// An explicit networkIP is honored as-is.
	const explicit = "10.20.0.99"

	insertBasicInstance(t, client, "sub-vm-explicit", &computepb.NetworkInterface{
		Subnetwork: ptrStr(subRef),
		NetworkIP:  ptrStr(explicit),
	}, nil)

	if got := getSDKInstanceNetworkIP(t, client, "sub-vm-explicit"); got != explicit {
		t.Errorf("explicit networkIP=%s want %s", got, explicit)
	}

	// An instance in no subnetwork falls back to the synthetic allocator: it
	// still gets an IP and does not land in the subnet's range.
	insertBasicInstance(t, client, "no-sub-vm", &computepb.NetworkInterface{
		Network: ptrStr("global/networks/default"),
	}, nil)

	fallback := getSDKInstanceNetworkIP(t, client, "no-sub-vm")
	if fallback == "" {
		t.Fatal("no-sub-vm has no networkIP, want synthetic fallback")
	}

	if ipnet.Contains(net.ParseIP(fallback)) {
		t.Errorf("no-sub-vm networkIP=%s unexpectedly inside %s", fallback, cidr)
	}
}

// insertBasicInstance creates an instance with a single NIC and the given
// service accounts, waiting for the operation to complete.
func insertBasicInstance(
	t *testing.T, c *gcpcompute.InstancesClient, name string,
	nic *computepb.NetworkInterface, sas []*computepb.ServiceAccount,
) {
	t.Helper()

	ctx := context.Background()

	op, err := c.Insert(ctx, &computepb.InsertInstanceRequest{
		Project: testProject, Zone: testZone,
		InstanceResource: &computepb.Instance{
			Name:              ptrStr(name),
			MachineType:       ptrStr("zones/" + testZone + "/machineTypes/n1-standard-1"),
			NetworkInterfaces: []*computepb.NetworkInterface{nic},
			ServiceAccounts:   sas,
		},
	})
	if err != nil {
		t.Fatalf("instance %s Insert: %v", name, err)
	}

	if err := op.Wait(ctx); err != nil {
		t.Fatalf("instance %s Insert wait: %v", name, err)
	}
}

func getSDKInstance(t *testing.T, c *gcpcompute.InstancesClient, name string) *computepb.Instance {
	t.Helper()

	inst, err := c.Get(context.Background(), &computepb.GetInstanceRequest{
		Project: testProject, Zone: testZone, Instance: name,
	})
	if err != nil {
		t.Fatalf("instance %s Get: %v", name, err)
	}

	return inst
}

func getSDKInstanceNetworkIP(t *testing.T, c *gcpcompute.InstancesClient, name string) string {
	t.Helper()

	inst := getSDKInstance(t, c, name)

	nics := inst.GetNetworkInterfaces()
	if len(nics) == 0 {
		t.Fatalf("instance %s has no networkInterfaces", name)
	}

	return nics[0].GetNetworkIP()
}

// createSubnet creates a custom-mode network and a subnetwork with the given
// CIDR in testRegion, so an instance can reference it by name.
func createSubnet(t *testing.T, ts *httptest.Server, netName, subName, cidr string) {
	t.Helper()

	ctx := context.Background()

	netClient, err := gcpcompute.NewNetworksRESTClient(ctx,
		option.WithEndpoint(ts.URL), option.WithoutAuthentication(), option.WithHTTPClient(ts.Client()))
	if err != nil {
		t.Fatalf("NewNetworksRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = netClient.Close() })

	auto := false

	netOp, err := netClient.Insert(ctx, &computepb.InsertNetworkRequest{
		Project: testProject,
		NetworkResource: &computepb.Network{
			Name:                  ptrStr(netName),
			AutoCreateSubnetworks: &auto,
		},
	})
	if err != nil {
		t.Fatalf("network Insert: %v", err)
	}

	if err := netOp.Wait(ctx); err != nil {
		t.Fatalf("network Insert wait: %v", err)
	}

	subClient, err := gcpcompute.NewSubnetworksRESTClient(ctx,
		option.WithEndpoint(ts.URL), option.WithoutAuthentication(), option.WithHTTPClient(ts.Client()))
	if err != nil {
		t.Fatalf("NewSubnetworksRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = subClient.Close() })

	subOp, err := subClient.Insert(ctx, &computepb.InsertSubnetworkRequest{
		Project: testProject, Region: testRegion,
		SubnetworkResource: &computepb.Subnetwork{
			Name:        ptrStr(subName),
			Network:     ptrStr("projects/" + testProject + "/global/networks/" + netName),
			IpCidrRange: ptrStr(cidr),
		},
	})
	if err != nil {
		t.Fatalf("subnetwork Insert: %v", err)
	}

	if err := subOp.Wait(ctx); err != nil {
		t.Fatalf("subnetwork Insert wait: %v", err)
	}
}

// assertInRangePastReserved fails unless ip is inside ipnet and past the four
// reserved low addresses (network, gateway, and two more).
func assertInRangePastReserved(t *testing.T, ipnet *net.IPNet, ip string) {
	t.Helper()

	parsed := net.ParseIP(ip)
	if parsed == nil {
		t.Fatalf("networkIP %q is not a valid IP", ip)
	}

	if !ipnet.Contains(parsed) {
		t.Fatalf("networkIP %s is not inside %s", ip, ipnet.String())
	}

	v4 := parsed.To4()
	base := ipnet.IP.To4()

	if v4 == nil || base == nil {
		t.Fatalf("networkIP %s / range %s are not IPv4", ip, ipnet.String())
	}

	// The host offset within the range must skip the four reserved low addresses.
	offset := (uint32(v4[0])<<24 | uint32(v4[1])<<16 | uint32(v4[2])<<8 | uint32(v4[3])) -
		(uint32(base[0])<<24 | uint32(base[1])<<16 | uint32(base[2])<<8 | uint32(base[3]))
	if offset < 4 {
		t.Errorf("networkIP %s offset %d falls in the reserved low addresses (want >= 4)", ip, offset)
	}
}
