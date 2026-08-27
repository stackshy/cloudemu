package loadbalancer_test

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
)

// backendPoolARMID builds the ARM resource id of a load balancer's backend
// address pool, the value a NIC ipConfiguration references to join the pool.
func backendPoolARMID(lbName, pool string) string {
	return "/subscriptions/" + testSub + "/resourceGroups/" + testRG +
		"/providers/Microsoft.Network/loadBalancers/" + lbName + "/backendAddressPools/" + pool
}

// ipConfigARMID builds the ARM resource id of a NIC ipConfiguration, the value
// a backend address pool projects back in its backendIPConfigurations.
func ipConfigARMID(nicName, configName string) string {
	return "/subscriptions/" + testSub + "/resourceGroups/" + testRG +
		"/providers/Microsoft.Network/networkInterfaces/" + nicName + "/ipConfigurations/" + configName
}

// newInterfacesClient builds an armnetwork InterfacesClient pointed at the same
// in-memory server as the load-balancer clients, so a NIC and a load balancer
// created in one test share one driver instance.
func newInterfacesClient(t *testing.T, srv lbServer) *armnetwork.InterfacesClient {
	t.Helper()

	client, err := armnetwork.NewInterfacesClient(testSub, fakeCred{}, srv.Opts)
	if err != nil {
		t.Fatalf("NewInterfacesClient: %v", err)
	}

	return client
}

// putNICWithPools creates or updates a NIC whose single ipConfiguration
// references the given backend address pool ids.
func putNICWithPools(
	t *testing.T, nics *armnetwork.InterfacesClient, nicName, configName string, poolIDs ...string,
) {
	t.Helper()

	ctx := context.Background()

	pools := make([]*armnetwork.BackendAddressPool, 0, len(poolIDs))
	for _, id := range poolIDs {
		pools = append(pools, &armnetwork.BackendAddressPool{ID: to.Ptr(id)})
	}

	poller, err := nics.BeginCreateOrUpdate(ctx, testRG, nicName, armnetwork.Interface{
		Location: to.Ptr("eastus"),
		Properties: &armnetwork.InterfacePropertiesFormat{
			IPConfigurations: []*armnetwork.InterfaceIPConfiguration{{
				Name: to.Ptr(configName),
				Properties: &armnetwork.InterfaceIPConfigurationPropertiesFormat{
					PrivateIPAllocationMethod:       to.Ptr(armnetwork.IPAllocationMethodDynamic),
					LoadBalancerBackendAddressPools: pools,
				},
			}},
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate NIC %q: %v", nicName, err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("poll NIC %q: %v", nicName, err)
	}
}

// nicPoolIDs returns the backend-pool ids the NIC's first ipConfiguration
// currently references (loadBalancerBackendAddressPools).
func nicPoolIDs(t *testing.T, nics *armnetwork.InterfacesClient, nicName string) []string {
	t.Helper()

	got, err := nics.Get(context.Background(), testRG, nicName, nil)
	if err != nil {
		t.Fatalf("Get NIC %q: %v", nicName, err)
	}

	if got.Properties == nil || len(got.Properties.IPConfigurations) == 0 {
		t.Fatalf("NIC %q has no ipConfigurations", nicName)
	}

	cfg := got.Properties.IPConfigurations[0]
	if cfg.Properties == nil {
		return nil
	}

	out := make([]string, 0, len(cfg.Properties.LoadBalancerBackendAddressPools))
	for _, p := range cfg.Properties.LoadBalancerBackendAddressPools {
		if p != nil && p.ID != nil {
			out = append(out, *p.ID)
		}
	}

	return out
}

// poolMemberIDs returns the ipConfiguration ids a backend pool reports as
// members (backendIPConfigurations), read via the standalone pools client.
func poolMemberIDs(t *testing.T, pools *armnetwork.LoadBalancerBackendAddressPoolsClient, lbName, poolName string) []string {
	t.Helper()

	got, err := pools.Get(context.Background(), testRG, lbName, poolName, nil)
	if err != nil {
		t.Fatalf("Get pool %q: %v", poolName, err)
	}

	if got.Properties == nil {
		return nil
	}

	out := make([]string, 0, len(got.Properties.BackendIPConfigurations))
	for _, ipc := range got.Properties.BackendIPConfigurations {
		if ipc != nil && ipc.ID != nil {
			out = append(out, *ipc.ID)
		}
	}

	return out
}

func containsID(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}

	return false
}

// TestSDKNICBackendPoolMembershipReflectsBothSides proves the NIC ipConfig ↔ LB
// backend-pool association is reflected on BOTH sides: after a NIC ipConfig
// joins a pool, GET NIC lists the pool in loadBalancerBackendAddressPools AND
// GET pool lists the ipConfig in backendIPConfigurations.
func TestSDKNICBackendPoolMembershipReflectsBothSides(t *testing.T) {
	srv := newLBServer(t)
	ctx := context.Background()

	lb, err := armnetwork.NewLoadBalancersClient(testSub, fakeCred{}, srv.Opts)
	if err != nil {
		t.Fatalf("NewLoadBalancersClient: %v", err)
	}

	pools, err := armnetwork.NewLoadBalancerBackendAddressPoolsClient(testSub, fakeCred{}, srv.Opts)
	if err != nil {
		t.Fatalf("NewLoadBalancerBackendAddressPoolsClient: %v", err)
	}

	nics := newInterfacesClient(t, srv)

	const (
		lbName   = "lb-nic"
		poolName = "pool1"
		nicName  = "nic1"
		cfgName  = "ipcfg1"
	)

	poller, err := lb.BeginCreateOrUpdate(ctx, testRG, lbName, armnetwork.LoadBalancer{
		Location: to.Ptr("eastus"),
		Properties: &armnetwork.LoadBalancerPropertiesFormat{
			BackendAddressPools: []*armnetwork.BackendAddressPool{{Name: to.Ptr(poolName)}},
		},
	}, nil)
	if err != nil {
		t.Fatalf("create LB: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("poll LB: %v", err)
	}

	poolID := backendPoolARMID(lbName, poolName)
	ipCfgID := ipConfigARMID(nicName, cfgName)

	putNICWithPools(t, nics, nicName, cfgName, poolID)

	// Forward direction: GET NIC shows the pool.
	if got := nicPoolIDs(t, nics, nicName); !containsID(got, poolID) {
		t.Fatalf("NIC ipConfig loadBalancerBackendAddressPools = %v, want to contain %q", got, poolID)
	}

	// Reverse direction: GET pool shows the ipConfig.
	if got := poolMemberIDs(t, pools, lbName, poolName); !containsID(got, ipCfgID) {
		t.Fatalf("pool backendIPConfigurations = %v, want to contain %q", got, ipCfgID)
	}

	// The whole-LB GET projects the same membership onto the nested pool.
	lbGot, err := lb.Get(ctx, testRG, lbName, nil)
	if err != nil {
		t.Fatalf("Get LB: %v", err)
	}

	var found bool

	for _, p := range lbGot.Properties.BackendAddressPools {
		if p.Name == nil || *p.Name != poolName || p.Properties == nil {
			continue
		}

		for _, ipc := range p.Properties.BackendIPConfigurations {
			if ipc != nil && ipc.ID != nil && *ipc.ID == ipCfgID {
				found = true
			}
		}
	}

	if !found {
		t.Fatalf("whole-LB GET pool %q did not project ipConfig %q in backendIPConfigurations", poolName, ipCfgID)
	}
}

// TestSDKNICBackendPoolMembershipClearedBothSides proves that removing the
// association — by re-PUTting the NIC without the pool ref, or by deleting the
// NIC — clears it on BOTH sides.
func TestSDKNICBackendPoolMembershipClearedBothSides(t *testing.T) {
	srv := newLBServer(t)
	ctx := context.Background()

	lb, err := armnetwork.NewLoadBalancersClient(testSub, fakeCred{}, srv.Opts)
	if err != nil {
		t.Fatalf("NewLoadBalancersClient: %v", err)
	}

	pools, err := armnetwork.NewLoadBalancerBackendAddressPoolsClient(testSub, fakeCred{}, srv.Opts)
	if err != nil {
		t.Fatalf("NewLoadBalancerBackendAddressPoolsClient: %v", err)
	}

	nics := newInterfacesClient(t, srv)

	const (
		lbName   = "lb-nic2"
		poolName = "pool1"
		nicName  = "nic1"
		cfgName  = "ipcfg1"
	)

	poller, err := lb.BeginCreateOrUpdate(ctx, testRG, lbName, armnetwork.LoadBalancer{
		Location: to.Ptr("eastus"),
		Properties: &armnetwork.LoadBalancerPropertiesFormat{
			BackendAddressPools: []*armnetwork.BackendAddressPool{{Name: to.Ptr(poolName)}},
		},
	}, nil)
	if err != nil {
		t.Fatalf("create LB: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("poll LB: %v", err)
	}

	poolID := backendPoolARMID(lbName, poolName)
	ipCfgID := ipConfigARMID(nicName, cfgName)

	// Associate, then disassociate by re-PUTting the NIC without the pool.
	putNICWithPools(t, nics, nicName, cfgName, poolID)
	putNICWithPools(t, nics, nicName, cfgName)

	if got := nicPoolIDs(t, nics, nicName); len(got) != 0 {
		t.Fatalf("after clearing, NIC loadBalancerBackendAddressPools = %v, want empty", got)
	}

	if got := poolMemberIDs(t, pools, lbName, poolName); containsID(got, ipCfgID) {
		t.Fatalf("after clearing, pool backendIPConfigurations = %v, still contains %q", got, ipCfgID)
	}

	// Re-associate, then delete the NIC entirely; the pool must drop the member.
	putNICWithPools(t, nics, nicName, cfgName, poolID)

	if got := poolMemberIDs(t, pools, lbName, poolName); !containsID(got, ipCfgID) {
		t.Fatalf("after re-associate, pool backendIPConfigurations = %v, want to contain %q", got, ipCfgID)
	}

	delPoller, err := nics.BeginDelete(ctx, testRG, nicName, nil)
	if err != nil {
		t.Fatalf("BeginDelete NIC: %v", err)
	}

	if _, err := delPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("poll delete NIC: %v", err)
	}

	if got := poolMemberIDs(t, pools, lbName, poolName); containsID(got, ipCfgID) {
		t.Fatalf("after NIC delete, pool backendIPConfigurations = %v, still contains %q", got, ipCfgID)
	}
}
