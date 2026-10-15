package vpc_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	computev1 "google.golang.org/api/compute/v1"
)

// TestConcurrentInternalAllocationsAreUnique checks that parallel INTERNAL
// address reservations and instance launches in one subnet each get a
// distinct IP.
func TestConcurrentInternalAllocationsAreUnique(t *testing.T) {
	ctx := context.Background()
	svc := newComputeV1(t)

	if _, err := svc.Networks.Insert(testProject, &computev1.Network{
		Name: "rn", ForceSendFields: []string{"AutoCreateSubnetworks"},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("insert network: %v", err)
	}

	if _, err := svc.Subnetworks.Insert(testProject, testRegion, &computev1.Subnetwork{
		Name: "rs", Network: "global/networks/rn", IpCidrRange: "10.10.0.0/24",
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("insert subnet: %v", err)
	}

	const n = 12

	subnet := "regions/" + testRegion + "/subnetworks/rs"
	zone := testRegion + "-a"
	errs := make(chan error, 2*n)

	var wg sync.WaitGroup

	for i := range n {
		wg.Add(2)

		go func() {
			defer wg.Done()

			_, err := svc.Addresses.Insert(testProject, testRegion, &computev1.Address{
				Name: fmt.Sprintf("ra%d", i), AddressType: "INTERNAL", Subnetwork: subnet,
			}).Context(ctx).Do()
			errs <- err
		}()

		go func() {
			defer wg.Done()

			_, err := svc.Instances.Insert(testProject, zone, &computev1.Instance{
				Name:              fmt.Sprintf("rv%d", i),
				MachineType:       "zones/" + zone + "/machineTypes/e2-small",
				NetworkInterfaces: []*computev1.NetworkInterface{{Subnetwork: subnet}},
			}).Context(ctx).Do()
			errs <- err
		}()
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	seen := map[string]string{}

	for i := range n {
		a, err := svc.Addresses.Get(testProject, testRegion, fmt.Sprintf("ra%d", i)).Context(ctx).Do()
		if err != nil {
			t.Fatalf("get address: %v", err)
		}

		vm, err := svc.Instances.Get(testProject, zone, fmt.Sprintf("rv%d", i)).Context(ctx).Do()
		if err != nil {
			t.Fatalf("get instance: %v", err)
		}

		for name, ip := range map[string]string{a.Name: a.Address, vm.Name: vm.NetworkInterfaces[0].NetworkIP} {
			if prev, dup := seen[ip]; dup {
				t.Errorf("%s and %s both got %s", prev, name, ip)
			}

			seen[ip] = name
		}
	}
}

// TestSubnetDeleteBlockedByReservedAddress checks that deleting a subnetwork,
// or an auto mode network, while an INTERNAL address is reserved in it is 400
// resourceInUseByAnotherResource until the address is released.
func TestSubnetDeleteBlockedByReservedAddress(t *testing.T) {
	ctx := context.Background()
	svc := newComputeV1(t)

	if _, err := svc.Networks.Insert(testProject, &computev1.Network{
		Name: "dn", ForceSendFields: []string{"AutoCreateSubnetworks"},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("insert network: %v", err)
	}

	if _, err := svc.Subnetworks.Insert(testProject, testRegion, &computev1.Subnetwork{
		Name: "ds", Network: "global/networks/dn", IpCidrRange: "10.20.0.0/24",
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("insert subnet: %v", err)
	}

	if _, err := svc.Networks.Insert(testProject, &computev1.Network{Name: "dauto", AutoCreateSubnetworks: true}).
		Context(ctx).Do(); err != nil {
		t.Fatalf("insert auto network: %v", err)
	}

	for name, subnet := range map[string]string{"da1": "ds", "da2": "dauto"} {
		if _, err := svc.Addresses.Insert(testProject, testRegion, &computev1.Address{
			Name: name, AddressType: "INTERNAL", Subnetwork: "regions/" + testRegion + "/subnetworks/" + subnet,
		}).Context(ctx).Do(); err != nil {
			t.Fatalf("insert %s: %v", name, err)
		}
	}

	_, err := svc.Subnetworks.Delete(testProject, testRegion, "ds").Context(ctx).Do()
	wantStatus(t, "delete subnet holding an address", err, http.StatusBadRequest)

	_, err = svc.Networks.Delete(testProject, "dauto").Context(ctx).Do()
	wantStatus(t, "delete auto network holding an address", err, http.StatusBadRequest)

	for _, name := range []string{"da1", "da2"} {
		if _, err := svc.Addresses.Delete(testProject, testRegion, name).Context(ctx).Do(); err != nil {
			t.Fatalf("delete %s: %v", name, err)
		}
	}

	if _, err := svc.Subnetworks.Delete(testProject, testRegion, "ds").Context(ctx).Do(); err != nil {
		t.Fatalf("delete subnet: %v", err)
	}

	if _, err := svc.Networks.Delete(testProject, "dauto").Context(ctx).Do(); err != nil {
		t.Fatalf("delete auto network: %v", err)
	}
}

// TestAutoSubnetCoversNewerRegions checks that an instance in a region added
// to the auto mode plan after 10.192.0.0/20 (us-south1, me-west1) lands in
// that region's auto subnetwork.
func TestAutoSubnetCoversNewerRegions(t *testing.T) {
	ctx := context.Background()
	svc := newComputeV1(t)

	if _, err := svc.Networks.Insert(testProject, &computev1.Network{Name: "an", AutoCreateSubnetworks: true}).
		Context(ctx).Do(); err != nil {
		t.Fatalf("insert network: %v", err)
	}

	for zone, prefix := range map[string]string{"us-south1-a": "10.206.", "me-west1-b": "10.208."} {
		name := "vm-" + zone

		if _, err := svc.Instances.Insert(testProject, zone, &computev1.Instance{
			Name:              name,
			MachineType:       "zones/" + zone + "/machineTypes/e2-small",
			NetworkInterfaces: []*computev1.NetworkInterface{{Network: "global/networks/an"}},
		}).Context(ctx).Do(); err != nil {
			t.Fatalf("insert %s: %v", name, err)
		}

		vm, err := svc.Instances.Get(testProject, zone, name).Context(ctx).Do()
		if err != nil {
			t.Fatalf("get %s: %v", name, err)
		}

		nic := vm.NetworkInterfaces[0]
		if !strings.HasSuffix(nic.Subnetwork, "/subnetworks/an") || !strings.HasPrefix(nic.NetworkIP, prefix) {
			t.Errorf("%s subnetwork=%q ip=%q want the auto subnet %s0.0/20", zone, nic.Subnetwork, nic.NetworkIP, prefix)
		}
	}
}
