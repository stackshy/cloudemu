package vpc_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	gcpcompute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

const (
	projA = "proj-a"
	projB = "proj-b"
)

// projClients is the set of gapic clients the project-isolation tests drive.
type projClients struct {
	nets  *gcpcompute.NetworksClient
	subs  *gcpcompute.SubnetworksClient
	fws   *gcpcompute.FirewallsClient
	insts *gcpcompute.InstancesClient
	ts    *httptest.Server
}

func newProjClients(t *testing.T) projClients {
	t.Helper()

	ts := newGCPNetServer(t)

	return projClients{
		nets:  newNetworksClient(t, ts),
		subs:  newSubnetsClient(t, ts),
		fws:   newFwClient(t, ts.URL, ts.Client()),
		insts: newInstancesClient(t, ts),
		ts:    ts,
	}
}

func waitOp(t *testing.T, ctx context.Context, what string, op *gcpcompute.Operation, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}

	if err := op.Wait(ctx); err != nil {
		t.Fatalf("%s wait: %v", what, err)
	}
}

func httpCode(err error) int {
	if err == nil {
		return http.StatusOK
	}

	var gerr *googleapi.Error
	if errors.As(err, &gerr) {
		return gerr.Code
	}

	return 0
}

// seedProject creates network w2-net, subnet w2-sub, firewall w2-fw and an
// instance w2-vm on that subnet, all in project p.
func seedProject(t *testing.T, ctx context.Context, c projClients, p, cidr string) {
	t.Helper()

	op, err := c.nets.Insert(ctx, &computepb.InsertNetworkRequest{
		Project: p,
		NetworkResource: &computepb.Network{
			Name: ptrStr("w2-net"), AutoCreateSubnetworks: ptrBool(false),
		},
	})
	waitOp(t, ctx, p+" network insert", op, err)

	netRef := "projects/" + p + "/global/networks/w2-net"
	op, err = c.subs.Insert(ctx, &computepb.InsertSubnetworkRequest{
		Project: p, Region: testRegion,
		SubnetworkResource: &computepb.Subnetwork{
			Name: ptrStr("w2-sub"), Network: ptrStr(netRef), IpCidrRange: ptrStr(cidr),
		},
	})
	waitOp(t, ctx, p+" subnet insert", op, err)

	op, err = c.fws.Insert(ctx, &computepb.InsertFirewallRequest{
		Project: p,
		FirewallResource: &computepb.Firewall{
			Name: ptrStr("w2-fw"), Network: ptrStr(netRef),
			Allowed: []*computepb.Allowed{{IPProtocol: ptrStr("tcp"), Ports: []string{"22"}}},
		},
	})
	waitOp(t, ctx, p+" firewall insert", op, err)

	op, err = c.insts.Insert(ctx, &computepb.InsertInstanceRequest{
		Project: p, Zone: zoneInRegion,
		InstanceResource: &computepb.Instance{
			Name:        ptrStr("w2-vm"),
			MachineType: ptrStr("zones/" + zoneInRegion + "/machineTypes/e2-small"),
			NetworkInterfaces: []*computepb.NetworkInterface{
				{Subnetwork: ptrStr("projects/" + p + "/regions/" + testRegion + "/subnetworks/w2-sub")},
			},
		},
	})
	waitOp(t, ctx, p+" instance insert", op, err)
}

// getAll reads w2-net, w2-sub, w2-fw and w2-vm in project p and returns the
// HTTP code of each read.
func getAll(ctx context.Context, c projClients, p string) map[string]int {
	_, nErr := c.nets.Get(ctx, &computepb.GetNetworkRequest{Project: p, Network: "w2-net"})
	_, sErr := c.subs.Get(ctx, &computepb.GetSubnetworkRequest{Project: p, Region: testRegion, Subnetwork: "w2-sub"})
	_, fErr := c.fws.Get(ctx, &computepb.GetFirewallRequest{Project: p, Firewall: "w2-fw"})
	_, iErr := c.insts.Get(ctx, &computepb.GetInstanceRequest{Project: p, Zone: zoneInRegion, Instance: "w2-vm"})

	return map[string]int{"network": httpCode(nErr), "subnet": httpCode(sErr), "firewall": httpCode(fErr), "instance": httpCode(iErr)}
}

func wantAll(t *testing.T, got map[string]int, code int, where string) {
	t.Helper()

	for kind, c := range got {
		if c != code {
			t.Errorf("%s %s: HTTP %d, want %d", where, kind, c, code)
		}
	}
}

// TestSameNamesInTwoProjectsStayIsolated pins GCE-01/GVPC-01: the same network,
// subnet, firewall and instance names coexist in two projects, each project
// reads back only its own, and deleting one project's copies leaves the
// other's in place.
func TestSameNamesInTwoProjectsStayIsolated(t *testing.T) {
	ctx := context.Background()
	c := newProjClients(t)

	seedProject(t, ctx, c, projA, "10.10.0.0/24")

	wantAll(t, getAll(ctx, c, projB), http.StatusNotFound, "before seeding "+projB)

	seedProject(t, ctx, c, projB, "10.20.0.0/24")

	sub, err := c.subs.Get(ctx, &computepb.GetSubnetworkRequest{Project: projB, Region: testRegion, Subnetwork: "w2-sub"})
	if err != nil {
		t.Fatalf("get %s subnet: %v", projB, err)
	}

	if sub.GetIpCidrRange() != "10.20.0.0/24" {
		t.Errorf("%s subnet cidr = %s, want its own 10.20.0.0/24", projB, sub.GetIpCidrRange())
	}

	vm, err := c.insts.Get(ctx, &computepb.GetInstanceRequest{Project: projB, Zone: zoneInRegion, Instance: "w2-vm"})
	if err != nil {
		t.Fatalf("get %s instance: %v", projB, err)
	}

	if ip := vm.GetNetworkInterfaces()[0].GetNetworkIP(); !strings.HasPrefix(ip, "10.20.0.") {
		t.Errorf("%s instance networkIP = %s, want one from its own subnet", projB, ip)
	}

	it := c.nets.List(ctx, &computepb.ListNetworksRequest{Project: projB})
	if n := countNetworks(t, it); n != 1 {
		t.Errorf("%s networks.list = %d items, want 1", projB, n)
	}

	op, err := c.insts.Delete(ctx, &computepb.DeleteInstanceRequest{Project: projB, Zone: zoneInRegion, Instance: "w2-vm"})
	waitOp(t, ctx, "delete "+projB+" instance", op, err)

	op, err = c.fws.Delete(ctx, &computepb.DeleteFirewallRequest{Project: projB, Firewall: "w2-fw"})
	waitOp(t, ctx, "delete "+projB+" firewall", op, err)

	op, err = c.subs.Delete(ctx, &computepb.DeleteSubnetworkRequest{Project: projB, Region: testRegion, Subnetwork: "w2-sub"})
	waitOp(t, ctx, "delete "+projB+" subnet", op, err)

	op, err = c.nets.Delete(ctx, &computepb.DeleteNetworkRequest{Project: projB, Network: "w2-net"})
	waitOp(t, ctx, "delete "+projB+" network", op, err)

	wantAll(t, getAll(ctx, c, projB), http.StatusNotFound, "after delete "+projB)
	wantAll(t, getAll(ctx, c, projA), http.StatusOK, projA+" after deleting "+projB)
}

func countNetworks(t *testing.T, it *gcpcompute.NetworkIterator) int {
	t.Helper()

	n := 0

	for {
		_, err := it.Next()
		if errors.Is(err, iterator.Done) {
			return n
		}

		if err != nil {
			t.Fatalf("networks list: %v", err)
		}

		n++
	}
}

// TestSubnetInUseGuardComparesProjects pins Revision 3 #2: deleting a subnet
// is not blocked by a same-named subnet's VM in another project, while a
// Shared VPC VM of another project on this subnet still blocks it.
func TestSubnetInUseGuardComparesProjects(t *testing.T) {
	ctx := context.Background()
	c := newProjClients(t)

	seedProject(t, ctx, c, projA, "10.10.0.0/24")
	seedProject(t, ctx, c, projB, "10.20.0.0/24")

	op, err := c.insts.Delete(ctx, &computepb.DeleteInstanceRequest{Project: projA, Zone: zoneInRegion, Instance: "w2-vm"})
	waitOp(t, ctx, "delete "+projA+" instance", op, err)

	// A Shared VPC VM in proj-b on proj-a's subnet keeps proj-a's subnet in use.
	op, err = c.insts.Insert(ctx, &computepb.InsertInstanceRequest{
		Project: projB, Zone: zoneInRegion,
		InstanceResource: &computepb.Instance{
			Name:        ptrStr("shared-vm"),
			MachineType: ptrStr("zones/" + zoneInRegion + "/machineTypes/e2-small"),
			NetworkInterfaces: []*computepb.NetworkInterface{
				{Subnetwork: ptrStr("projects/" + projA + "/regions/" + testRegion + "/subnetworks/w2-sub")},
			},
		},
	})
	waitOp(t, ctx, "insert shared-vpc instance", op, err)

	vm, err := c.insts.Get(ctx, &computepb.GetInstanceRequest{Project: projB, Zone: zoneInRegion, Instance: "shared-vm"})
	if err != nil {
		t.Fatalf("get shared-vm: %v", err)
	}

	if ip := vm.GetNetworkInterfaces()[0].GetNetworkIP(); !strings.HasPrefix(ip, "10.10.0.") {
		t.Errorf("shared-vm networkIP = %s, want one from %s's subnet 10.10.0.0/24", ip, projA)
	}

	_, err = c.subs.Delete(ctx, &computepb.DeleteSubnetworkRequest{Project: projA, Region: testRegion, Subnetwork: "w2-sub"})
	if code := httpCode(err); code != http.StatusBadRequest {
		t.Fatalf("delete %s subnet used by a Shared VPC VM: HTTP %d (%v), want 400", projA, code, err)
	}

	op, err = c.insts.Delete(ctx, &computepb.DeleteInstanceRequest{Project: projB, Zone: zoneInRegion, Instance: "shared-vm"})
	waitOp(t, ctx, "delete shared-vm", op, err)

	// proj-b's own w2-vm sits on proj-b's w2-sub and must not block proj-a's.
	op, err = c.subs.Delete(ctx, &computepb.DeleteSubnetworkRequest{Project: projA, Region: testRegion, Subnetwork: "w2-sub"})
	waitOp(t, ctx, "delete "+projA+" subnet", op, err)
}

// TestOperationsArePerProjectAndUnique pins GCE-21: an operation is polled
// only under the project that minted it, and re-creating a resource yields a
// new operation name in GCE's operation-<ms>-<hex> shape.
func TestOperationsArePerProjectAndUnique(t *testing.T) {
	ctx := context.Background()
	c := newProjClients(t)

	insert := func() string {
		op, err := c.nets.Insert(ctx, &computepb.InsertNetworkRequest{
			Project:         projA,
			NetworkResource: &computepb.Network{Name: ptrStr("op-net"), AutoCreateSubnetworks: ptrBool(false)},
		})
		waitOp(t, ctx, "network insert", op, err)

		return op.Name()
	}

	first := insert()

	if !regexp.MustCompile(`^operation-\d{13}-[0-9a-f]{8}$`).MatchString(first) {
		t.Errorf("operation name %q, want operation-<ms>-<8 hex>", first)
	}

	op, err := c.nets.Delete(ctx, &computepb.DeleteNetworkRequest{Project: projA, Network: "op-net"})
	waitOp(t, ctx, "network delete", op, err)

	if second := insert(); second == first {
		t.Errorf("re-insert reused operation name %q", first)
	}

	ops := newGlobalOpsClient(t, c)

	if _, err := ops.Get(ctx, &computepb.GetGlobalOperationRequest{Project: projA, Operation: first}); err != nil {
		t.Errorf("get op under minting project: %v", err)
	}

	_, err = ops.Get(ctx, &computepb.GetGlobalOperationRequest{Project: projB, Operation: first})
	if code := httpCode(err); code != http.StatusNotFound {
		t.Errorf("get %s op under %s: HTTP %d, want 404", projA, projB, code)
	}
}

func newGlobalOpsClient(t *testing.T, c projClients) *gcpcompute.GlobalOperationsClient {
	t.Helper()

	ops, err := gcpcompute.NewGlobalOperationsRESTClient(context.Background(),
		option.WithEndpoint(c.ts.URL), option.WithoutAuthentication(), option.WithHTTPClient(c.ts.Client()))
	if err != nil {
		t.Fatalf("NewGlobalOperationsRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = ops.Close() })

	return ops
}
