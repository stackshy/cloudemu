package vpc_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	computev1 "google.golang.org/api/compute/v1"
)

// TestAutoModeNetworkCreatesRegionalSubnets covers GVPC-08 and GVPC-09: an auto
// mode network gets one /20 subnetwork per region from GCP's 10.128.0.0/9 plan,
// named after the network; network GET lists them in subnetworks[]; an
// instance naming only the network lands in its region's auto subnetwork; and
// deleting the network removes its auto subnetworks once no instance uses them.
func TestAutoModeNetworkCreatesRegionalSubnets(t *testing.T) {
	ctx := context.Background()
	svc := newComputeV1(t)

	_, err := svc.Networks.Insert(testProject, &computev1.Network{
		Name: "auto1", AutoCreateSubnetworks: true,
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("insert network: %v", err)
	}

	for region, want := range map[string]string{
		"us-central1":  "10.128.0.0/20",
		"europe-west1": "10.132.0.0/20",
		"us-east1":     "10.142.0.0/20",
	} {
		sub, err := svc.Subnetworks.Get(testProject, region, "auto1").Context(ctx).Do()
		if err != nil {
			t.Fatalf("get auto subnet in %s: %v", region, err)
		}

		if sub.IpCidrRange != want || !strings.HasSuffix(sub.Network, "/global/networks/auto1") {
			t.Errorf("%s: range=%q network=%q want %q on auto1", region, sub.IpCidrRange, sub.Network, want)
		}
	}

	net, err := svc.Networks.Get(testProject, "auto1").Context(ctx).Do()
	if err != nil {
		t.Fatalf("get network: %v", err)
	}

	wantLink := "/projects/" + testProject + "/regions/us-central1/subnetworks/auto1"
	if !containsSuffix(net.Subnetworks, wantLink) || len(net.Subnetworks) < 20 {
		t.Fatalf("subnetworks=%v want one per region incl %s", net.Subnetworks, wantLink)
	}

	_, err = svc.Instances.Insert(testProject, "us-central1-a", &computev1.Instance{
		Name:              "vm1",
		MachineType:       "zones/us-central1-a/machineTypes/e2-small",
		NetworkInterfaces: []*computev1.NetworkInterface{{Network: "global/networks/auto1"}},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("insert instance: %v", err)
	}

	vm, err := svc.Instances.Get(testProject, "us-central1-a", "vm1").Context(ctx).Do()
	if err != nil {
		t.Fatalf("get instance: %v", err)
	}

	nic := vm.NetworkInterfaces[0]
	if !strings.HasSuffix(nic.Subnetwork, wantLink) || !strings.HasPrefix(nic.NetworkIP, "10.128.") {
		t.Errorf("nic subnetwork=%q ip=%q want the us-central1 auto subnet", nic.Subnetwork, nic.NetworkIP)
	}

	_, err = svc.Networks.Delete(testProject, "auto1").Context(ctx).Do()
	wantStatus(t, "delete network with an instance in its subnet", err, http.StatusBadRequest)

	if _, err := svc.Instances.Delete(testProject, "us-central1-a", "vm1").Context(ctx).Do(); err != nil {
		t.Fatalf("delete instance: %v", err)
	}

	if _, err := svc.Networks.Delete(testProject, "auto1").Context(ctx).Do(); err != nil {
		t.Fatalf("delete network: %v", err)
	}

	_, err = svc.Subnetworks.Get(testProject, "us-central1", "auto1").Context(ctx).Do()
	wantStatus(t, "auto subnet after network delete", err, http.StatusNotFound)
}

// TestCustomNetworkListsSubnetworks covers GVPC-09 for a custom mode network:
// subnetworks[] carries the self-link of each child subnet and nothing else.
func TestCustomNetworkListsSubnetworks(t *testing.T) {
	ctx := context.Background()
	svc := newComputeV1(t)

	if _, err := svc.Networks.Insert(testProject, &computev1.Network{
		Name: "custom1", ForceSendFields: []string{"AutoCreateSubnetworks"},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("insert network: %v", err)
	}

	for _, s := range []struct{ name, region, cidr string }{
		{"s1", "us-central1", "10.1.0.0/16"},
		{"s2", "europe-west1", "10.2.0.0/16"},
	} {
		if _, err := svc.Subnetworks.Insert(testProject, s.region, &computev1.Subnetwork{
			Name: s.name, Network: "global/networks/custom1", IpCidrRange: s.cidr,
		}).Context(ctx).Do(); err != nil {
			t.Fatalf("insert %s: %v", s.name, err)
		}
	}

	net, err := svc.Networks.Get(testProject, "custom1").Context(ctx).Do()
	if err != nil {
		t.Fatalf("get network: %v", err)
	}

	if len(net.Subnetworks) != 2 ||
		!containsSuffix(net.Subnetworks, "/regions/us-central1/subnetworks/s1") ||
		!containsSuffix(net.Subnetworks, "/regions/europe-west1/subnetworks/s2") {
		t.Errorf("subnetworks=%v want s1 and s2 self-links", net.Subnetworks)
	}
}

func containsSuffix(list []string, suffix string) bool {
	for _, s := range list {
		if strings.HasSuffix(s, suffix) {
			return true
		}
	}

	return false
}
