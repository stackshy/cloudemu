package vpc_test

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"

	computev1 "google.golang.org/api/compute/v1"
)

func insertCustomNet(ctx context.Context, t *testing.T, svc *computev1.Service, name string, cidrs ...string) {
	t.Helper()

	if _, err := svc.Networks.Insert(testProject, &computev1.Network{
		Name: name, ForceSendFields: []string{"AutoCreateSubnetworks"},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("insert %s: %v", name, err)
	}

	for i, cidr := range cidrs {
		if _, err := svc.Subnetworks.Insert(testProject, testRegion, &computev1.Subnetwork{
			Name: fmt.Sprintf("%s-s%d", name, i), Network: "global/networks/" + name, IpCidrRange: cidr,
		}).Context(ctx).Do(); err != nil {
			t.Fatalf("insert subnet of %s: %v", name, err)
		}
	}
}

func addPeeringTo(ctx context.Context, svc *computev1.Service, network, name, peer string, importRoutes bool) error {
	_, err := svc.Networks.AddPeering(testProject, network, &computev1.NetworksAddPeeringRequest{
		NetworkPeering: &computev1.NetworkPeering{
			Name: name, Network: "projects/" + testProject + "/global/networks/" + peer,
			ExchangeSubnetRoutes: true, ImportCustomRoutes: importRoutes,
		},
	}).Context(ctx).Do()

	return err
}

// TestConcurrentAddPeeringKeepsEveryEntry checks that parallel addPeering
// calls on one network all land in its peerings[].
func TestConcurrentAddPeeringKeepsEveryEntry(t *testing.T) {
	ctx := context.Background()
	svc := newComputeV1(t)

	const n = 8

	insertCustomNet(ctx, t, svc, "hub")

	for i := range n {
		insertCustomNet(ctx, t, svc, fmt.Sprintf("spoke%d", i))
	}

	errs := make(chan error, n)

	var wg sync.WaitGroup

	for i := range n {
		wg.Add(1)

		go func() {
			defer wg.Done()

			errs <- addPeeringTo(ctx, svc, "hub", fmt.Sprintf("p%d", i), fmt.Sprintf("spoke%d", i), false)
		}()
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("addPeering: %v", err)
		}
	}

	hub, err := svc.Networks.Get(testProject, "hub").Context(ctx).Do()
	if err != nil {
		t.Fatalf("get hub: %v", err)
	}

	if len(hub.Peerings) != n {
		t.Fatalf("hub has %d peerings, want %d", len(hub.Peerings), n)
	}
}

// TestAddPeeringRejections checks the 400s real GCP returns: a duplicate
// peering name, a second peering to the same peer network, and a peering that
// would go ACTIVE between networks with overlapping subnet ranges.
func TestAddPeeringRejections(t *testing.T) {
	ctx := context.Background()
	svc := newComputeV1(t)

	insertCustomNet(ctx, t, svc, "va", "10.1.0.0/24")
	insertCustomNet(ctx, t, svc, "vb", "10.2.0.0/24")
	insertCustomNet(ctx, t, svc, "vc", "10.1.0.128/25")

	if err := addPeeringTo(ctx, svc, "va", "a-b", "vb", false); err != nil {
		t.Fatalf("addPeering a-b: %v", err)
	}

	wantStatus(t, "duplicate name", addPeeringTo(ctx, svc, "va", "a-b", "vc", false), http.StatusBadRequest)
	wantStatus(t, "same peer twice", addPeeringTo(ctx, svc, "va", "a-b-2", "vb", false), http.StatusBadRequest)

	if err := addPeeringTo(ctx, svc, "va", "a-c", "vc", false); err != nil {
		t.Fatalf("one-sided peering with an overlapping network: %v", err)
	}

	wantStatus(t, "overlapping ranges", addPeeringTo(ctx, svc, "vc", "c-a", "va", false), http.StatusBadRequest)
}

// TestPartialUpdatePeeringKeepsOmittedFlags checks that updatePeering changes
// only the route flags the request sets.
func TestPartialUpdatePeeringKeepsOmittedFlags(t *testing.T) {
	ctx := context.Background()
	svc := newComputeV1(t)

	insertCustomNet(ctx, t, svc, "ua")
	insertCustomNet(ctx, t, svc, "ub")

	if err := addPeeringTo(ctx, svc, "ua", "u", "ub", true); err != nil {
		t.Fatalf("addPeering: %v", err)
	}

	if _, err := svc.Networks.UpdatePeering(testProject, "ua", &computev1.NetworksUpdatePeeringRequest{
		NetworkPeering: &computev1.NetworkPeering{Name: "u", ExportCustomRoutes: true},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("updatePeering: %v", err)
	}

	p := wantPeering(ctx, t, svc, "ua", "u", "INACTIVE")
	if !p.ExportCustomRoutes || !p.ImportCustomRoutes || !p.ExportSubnetRoutesWithPublicIp {
		t.Errorf("export=%v import=%v exportPublic=%v want all true",
			p.ExportCustomRoutes, p.ImportCustomRoutes, p.ExportSubnetRoutesWithPublicIp)
	}
}
