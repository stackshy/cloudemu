package vpc_test

import (
	"context"
	"net"
	"net/http"
	"strings"
	"testing"

	computev1 "google.golang.org/api/compute/v1"
)

// TestExternalAddressGetsPublicIP covers GVPC-20: an EXTERNAL address, the
// default type, regional or global, gets a public IP and echoes addressType
// EXTERNAL and networkTier PREMIUM.
func TestExternalAddressGetsPublicIP(t *testing.T) {
	ctx := context.Background()
	svc := newComputeV1(t)

	if _, err := svc.Addresses.Insert(testProject, testRegion, &computev1.Address{Name: "ext1"}).
		Context(ctx).Do(); err != nil {
		t.Fatalf("insert regional: %v", err)
	}

	if _, err := svc.GlobalAddresses.Insert(testProject, &computev1.Address{Name: "gext1"}).
		Context(ctx).Do(); err != nil {
		t.Fatalf("insert global: %v", err)
	}

	regional, err := svc.Addresses.Get(testProject, testRegion, "ext1").Context(ctx).Do()
	if err != nil {
		t.Fatalf("get regional: %v", err)
	}

	global, err := svc.GlobalAddresses.Get(testProject, "gext1").Context(ctx).Do()
	if err != nil {
		t.Fatalf("get global: %v", err)
	}

	for _, a := range []*computev1.Address{regional, global} {
		ip := net.ParseIP(a.Address)
		if ip == nil || ip.IsPrivate() || !(strings.HasPrefix(a.Address, "34.") || strings.HasPrefix(a.Address, "35.")) {
			t.Errorf("%s address=%q want a public 34.x/35.x IP", a.Name, a.Address)
		}

		if a.AddressType != "EXTERNAL" || a.NetworkTier != "PREMIUM" {
			t.Errorf("%s addressType=%q networkTier=%q want EXTERNAL/PREMIUM", a.Name, a.AddressType, a.NetworkTier)
		}
	}

	if regional.Address == global.Address {
		t.Errorf("both addresses got %s", regional.Address)
	}
}

// TestInternalAddressComesFromSubnet covers GVPC-21: an INTERNAL address takes
// an IP from its subnetwork's range, an explicit IP outside the range is 400,
// an IP already reserved is 400, and a missing subnetwork is 404.
func TestInternalAddressComesFromSubnet(t *testing.T) {
	ctx := context.Background()
	svc := newComputeV1(t)

	if _, err := svc.Networks.Insert(testProject, &computev1.Network{
		Name: "n1", ForceSendFields: []string{"AutoCreateSubnetworks"},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("insert network: %v", err)
	}

	if _, err := svc.Subnetworks.Insert(testProject, testRegion, &computev1.Subnetwork{
		Name: "s1", Network: "global/networks/n1", IpCidrRange: "10.1.0.0/16",
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("insert subnet: %v", err)
	}

	internal := func(name, subnet, ip string) error {
		_, err := svc.Addresses.Insert(testProject, testRegion, &computev1.Address{
			Name: name, AddressType: "INTERNAL", Address: ip,
			Subnetwork: "regions/" + testRegion + "/subnetworks/" + subnet,
		}).Context(ctx).Do()

		return err
	}

	if err := internal("a2", "s1", ""); err != nil {
		t.Fatalf("insert a2: %v", err)
	}

	if err := internal("a2b", "s1", ""); err != nil {
		t.Fatalf("insert a2b: %v", err)
	}

	a2, err := svc.Addresses.Get(testProject, testRegion, "a2").Context(ctx).Do()
	if err != nil {
		t.Fatalf("get a2: %v", err)
	}

	a2b, err := svc.Addresses.Get(testProject, testRegion, "a2b").Context(ctx).Do()
	if err != nil {
		t.Fatalf("get a2b: %v", err)
	}

	_, s1, _ := net.ParseCIDR("10.1.0.0/16")
	if !s1.Contains(net.ParseIP(a2.Address)) || !s1.Contains(net.ParseIP(a2b.Address)) || a2.Address == a2b.Address {
		t.Errorf("a2=%q a2b=%q want distinct IPs in 10.1.0.0/16", a2.Address, a2b.Address)
	}

	if err := internal("a3", "s1", "10.1.0.50"); err != nil {
		t.Fatalf("insert in-range explicit IP: %v", err)
	}

	wantStatus(t, "explicit IP outside the subnet", internal("a4", "s1", "10.99.0.5"), http.StatusBadRequest)
	wantStatus(t, "explicit IP already reserved", internal("a5", "s1", "10.1.0.50"), http.StatusBadRequest)
	wantStatus(t, "missing subnetwork", internal("a6", "nope", ""), http.StatusNotFound)
}
