package vpc_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	computev1 "google.golang.org/api/compute/v1"
)

// TestNetworkPeeringLifecycle covers GVPC-06: addPeering records a peering on
// its network, INACTIVE until the peer adds the reverse side and ACTIVE once it
// has; updatePeering changes the route flags; removePeering drops it and the
// other side goes back to INACTIVE.
func TestNetworkPeeringLifecycle(t *testing.T) {
	ctx := context.Background()
	svc := newComputeV1(t)

	for _, n := range []string{"na", "nb"} {
		if _, err := svc.Networks.Insert(testProject, &computev1.Network{
			Name: n, ForceSendFields: []string{"AutoCreateSubnetworks"},
		}).Context(ctx).Do(); err != nil {
			t.Fatalf("insert %s: %v", n, err)
		}
	}

	addPeering := func(network, name, peer string) error {
		_, err := svc.Networks.AddPeering(testProject, network, &computev1.NetworksAddPeeringRequest{
			NetworkPeering: &computev1.NetworkPeering{
				Name: name, Network: "projects/" + testProject + "/global/networks/" + peer,
				ExchangeSubnetRoutes: true,
			},
		}).Context(ctx).Do()

		return err
	}

	if err := addPeering("na", "a-to-b", "nb"); err != nil {
		t.Fatalf("addPeering na: %v", err)
	}

	wantPeering(ctx, t, svc, "na", "a-to-b", "INACTIVE")

	wantStatus(t, "duplicate peering", addPeering("na", "a-to-b", "nb"), http.StatusConflict)
	wantStatus(t, "peering to a missing network", addPeering("na", "a-to-x", "nx"), http.StatusNotFound)

	if err := addPeering("nb", "b-to-a", "na"); err != nil {
		t.Fatalf("addPeering nb: %v", err)
	}

	p := wantPeering(ctx, t, svc, "na", "a-to-b", "ACTIVE")
	if !strings.HasSuffix(p.Network, "/projects/"+testProject+"/global/networks/nb") || !p.ExchangeSubnetRoutes {
		t.Errorf("peering network=%q exchangeSubnetRoutes=%v", p.Network, p.ExchangeSubnetRoutes)
	}

	if _, err := svc.Networks.UpdatePeering(testProject, "na", &computev1.NetworksUpdatePeeringRequest{
		NetworkPeering: &computev1.NetworkPeering{Name: "a-to-b", ImportCustomRoutes: true},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("updatePeering: %v", err)
	}

	if p := wantPeering(ctx, t, svc, "na", "a-to-b", "ACTIVE"); !p.ImportCustomRoutes {
		t.Error("importCustomRoutes not updated")
	}

	if _, err := svc.Networks.RemovePeering(testProject, "nb", &computev1.NetworksRemovePeeringRequest{
		Name: "b-to-a",
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("removePeering: %v", err)
	}

	wantPeering(ctx, t, svc, "na", "a-to-b", "INACTIVE")

	if nb, err := svc.Networks.Get(testProject, "nb").Context(ctx).Do(); err != nil || len(nb.Peerings) != 0 {
		t.Errorf("nb peerings=%v err=%v want none", nb.Peerings, err)
	}
}

func wantPeering(ctx context.Context, t *testing.T, svc *computev1.Service, network, name, state string,
) *computev1.NetworkPeering {
	t.Helper()

	n, err := svc.Networks.Get(testProject, network).Context(ctx).Do()
	if err != nil {
		t.Fatalf("get %s: %v", network, err)
	}

	for _, p := range n.Peerings {
		if p.Name == name {
			if p.State != state {
				t.Fatalf("%s peering %s state=%q want %q", network, name, p.State, state)
			}

			return p
		}
	}

	t.Fatalf("%s peerings=%v missing %s", network, n.Peerings, name)

	return nil
}
