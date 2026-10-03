package gcp_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gcpcompute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

const (
	opsProject = "ops-p"
	opsZone    = "us-central1-a"
	opsRegion  = "us-central1"
)

func opsClientOpts(ts *httptest.Server) []option.ClientOption {
	return []option.ClientOption{
		option.WithEndpoint(ts.URL), option.WithoutAuthentication(), option.WithHTTPClient(ts.Client()),
	}
}

// waitOp waits on op (which polls the operation get) and checks the polled
// operation is the stored one: same name, the real operationType, the
// resource's own targetLink and a DONE status.
func waitOp(t *testing.T, op *gcpcompute.Operation, wantType, wantTarget, wantScope string) *computepb.Operation {
	t.Helper()

	minted := op.Proto().GetName()
	mintedID := op.Proto().GetId()

	if err := op.Wait(context.Background()); err != nil {
		t.Fatalf("%s %s: Wait: %v", wantType, wantTarget, err)
	}

	got := op.Proto()

	checks := []struct {
		field string
		ok    bool
	}{
		{"name", got.GetName() == minted},
		{"id", got.GetId() == mintedID && mintedID != 0},
		{"operationType", got.GetOperationType() == wantType},
		{"targetLink", strings.HasSuffix(got.GetTargetLink(), "/compute/v1/projects/"+opsProject+"/"+wantTarget)},
		{"status", got.GetStatus() == computepb.Operation_DONE},
		{"progress", got.GetProgress() == 100},
		{"times", got.GetInsertTime() != "" && got.GetStartTime() != "" && got.GetEndTime() != ""},
		{"user", got.GetUser() != ""},
		{"selfLink", strings.HasSuffix(got.GetSelfLink(), "/"+wantScope+"/operations/"+minted)},
	}

	for _, c := range checks {
		if !c.ok {
			t.Errorf("%s %s: polled op field %s wrong: %v", wantType, wantTarget, c.field, got)
		}
	}

	return got
}

func is404(err error) bool {
	var gerr *googleapi.Error

	return errors.As(err, &gerr) && gerr.Code == http.StatusNotFound
}

// TestComputeOperationsReturnStoredOp drives the compute gapic clients the
// way Terraform and gcloud do: every mutation's op.Wait polls the operation
// get, which must return the operation as minted rather than a rebuilt one.
func TestComputeOperationsReturnStoredOp(t *testing.T) {
	ts := fullServer(t)
	ctx := context.Background()
	opts := opsClientOpts(ts)

	inst, err := gcpcompute.NewInstancesRESTClient(ctx, opts...)
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Close()

	nets, err := gcpcompute.NewNetworksRESTClient(ctx, opts...)
	if err != nil {
		t.Fatal(err)
	}
	defer nets.Close()

	fws, err := gcpcompute.NewFirewallsRESTClient(ctx, opts...)
	if err != nil {
		t.Fatal(err)
	}
	defer fws.Close()

	name := func(s string) *string { return &s }
	zoneScope, global := "zones/"+opsZone, "global"

	op, err := inst.Insert(ctx, &computepb.InsertInstanceRequest{Project: opsProject, Zone: opsZone,
		InstanceResource: &computepb.Instance{Name: name("vm1"), MachineType: name("zones/" + opsZone + "/machineTypes/e2-small")}})
	if err != nil {
		t.Fatalf("instance insert: %v", err)
	}

	got := waitOp(t, op, "insert", zoneScope+"/instances/vm1", zoneScope)
	if !strings.HasSuffix(got.GetZone(), "/zones/"+opsZone) || got.GetTargetId() == 0 {
		t.Errorf("instance insert op zone %q targetId %d", got.GetZone(), got.GetTargetId())
	}

	op, err = inst.Delete(ctx, &computepb.DeleteInstanceRequest{Project: opsProject, Zone: opsZone, Instance: "vm1"})
	if err != nil {
		t.Fatalf("instance delete: %v", err)
	}

	waitOp(t, op, "delete", zoneScope+"/instances/vm1", zoneScope)

	op, err = nets.Insert(ctx, &computepb.InsertNetworkRequest{Project: opsProject,
		NetworkResource: &computepb.Network{Name: name("n1"), AutoCreateSubnetworks: new(bool)}})
	if err != nil {
		t.Fatalf("network insert: %v", err)
	}

	waitOp(t, op, "insert", "global/networks/n1", global)

	op, err = fws.Insert(ctx, &computepb.InsertFirewallRequest{Project: opsProject, FirewallResource: &computepb.Firewall{
		Name: name("fw1"), Network: name("global/networks/n1"),
		Allowed: []*computepb.Allowed{{IPProtocol: name("tcp"), Ports: []string{"22"}}},
	}})
	if err != nil {
		t.Fatalf("firewall insert: %v", err)
	}

	waitOp(t, op, "insert", "global/firewalls/fw1", global)

	op, err = fws.Patch(ctx, &computepb.PatchFirewallRequest{Project: opsProject, Firewall: "fw1",
		FirewallResource: &computepb.Firewall{Description: name("patched")}})
	if err != nil {
		t.Fatalf("firewall patch: %v", err)
	}

	waitOp(t, op, "patch", "global/firewalls/fw1", global)

	op, err = fws.Delete(ctx, &computepb.DeleteFirewallRequest{Project: opsProject, Firewall: "fw1"})
	if err != nil {
		t.Fatalf("firewall delete: %v", err)
	}

	waitOp(t, op, "delete", "global/firewalls/fw1", global)

	op, err = nets.Delete(ctx, &computepb.DeleteNetworkRequest{Project: opsProject, Network: "n1"})
	if err != nil {
		t.Fatalf("network delete: %v", err)
	}

	waitOp(t, op, "delete", "global/networks/n1", global)
}

// TestComputeOperationsListGetDelete covers operations list (paged), the
// aggregated list, delete, and 404 for an unknown or deleted operation.
func TestComputeOperationsListGetDelete(t *testing.T) {
	ts := fullServer(t)
	ctx := context.Background()
	opts := opsClientOpts(ts)

	zops, err := gcpcompute.NewZoneOperationsRESTClient(ctx, opts...)
	if err != nil {
		t.Fatal(err)
	}
	defer zops.Close()

	gops, err := gcpcompute.NewGlobalOperationsRESTClient(ctx, opts...)
	if err != nil {
		t.Fatal(err)
	}
	defer gops.Close()

	for _, d := range []string{"d1", "d2", "d3"} {
		mustDo(t, ts, http.MethodPost, "/compute/v1/projects/"+opsProject+"/zones/"+opsZone+"/disks",
			`{"name":"`+d+`","sizeGb":"10"}`)
	}

	mustDo(t, ts, http.MethodPost, "/compute/v1/projects/"+opsProject+"/global/networks",
		`{"name":"n1","autoCreateSubnetworks":false}`)
	// An operation in another project must not leak into this project's list.
	mustDo(t, ts, http.MethodPost, "/compute/v1/projects/other-p/zones/"+opsZone+"/disks", `{"name":"x","sizeGb":"10"}`)

	size := uint32(1)
	it := zops.List(ctx, &computepb.ListZoneOperationsRequest{Project: opsProject, Zone: opsZone, MaxResults: &size})

	var targets []string

	for {
		o, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}

		if err != nil {
			t.Fatalf("zone operations list: %v", err)
		}

		targets = append(targets, o.GetOperationType()+" "+o.GetTargetLink()[strings.LastIndex(o.GetTargetLink(), "/")+1:])
	}

	if list := mustDo(t, ts, http.MethodGet, "/compute/v1/projects/"+opsProject+"/global/operations", ""); !strings.Contains(list,
		`/compute/v1/projects/`+opsProject+`/global/operations"`) {
		t.Errorf("global operations list selfLink must end in /operations: %.300s", list)
	}

	if strings.Join(targets, ",") != "insert d1,insert d2,insert d3" {
		t.Errorf("zone operations = %v, want the 3 disk inserts in order", targets)
	}

	agg := gops.AggregatedList(ctx, &computepb.AggregatedListGlobalOperationsRequest{Project: opsProject})
	counts := map[string]int{}

	for {
		pair, err := agg.Next()
		if errors.Is(err, iterator.Done) {
			break
		}

		if err != nil {
			t.Fatalf("aggregated operations list: %v", err)
		}

		counts[pair.Key] += len(pair.Value.GetOperations())
	}

	if counts["zones/"+opsZone] != 3 || counts["global"] != 1 {
		t.Errorf("aggregated operations = %v, want 3 zonal and 1 global", counts)
	}

	glist := gops.List(ctx, &computepb.ListGlobalOperationsRequest{Project: opsProject})

	first, err := glist.Next()
	if err != nil {
		t.Fatalf("global operations list: %v", err)
	}

	if _, err := gops.Delete(ctx, &computepb.DeleteGlobalOperationRequest{Project: opsProject, Operation: first.GetName()}); err != nil {
		t.Fatalf("global operation delete: %v", err)
	}

	if _, err := gops.Get(ctx, &computepb.GetGlobalOperationRequest{Project: opsProject, Operation: first.GetName()}); !is404(err) {
		t.Errorf("get deleted op: err = %v, want 404", err)
	}

	if _, err := zops.Get(ctx, &computepb.GetZoneOperationRequest{Project: opsProject, Zone: opsZone, Operation: "operation-bogus"}); !is404(err) {
		t.Errorf("get unknown zone op: err = %v, want 404", err)
	}

	if _, err := zops.Delete(ctx, &computepb.DeleteZoneOperationRequest{Project: opsProject, Zone: opsZone, Operation: "operation-bogus"}); !is404(err) {
		t.Errorf("delete unknown zone op: err = %v, want 404", err)
	}
}

// TestRegionalOperationStored polls a regional (subnetwork) operation and
// checks its region field and targetLink.
func TestRegionalOperationStored(t *testing.T) {
	ts := fullServer(t)
	ctx := context.Background()

	mustDo(t, ts, http.MethodPost, "/compute/v1/projects/"+opsProject+"/global/networks",
		`{"name":"n1","autoCreateSubnetworks":false}`)

	c, err := gcpcompute.NewSubnetworksRESTClient(ctx, opsClientOpts(ts)...)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	name := func(s string) *string { return &s }

	op, err := c.Insert(ctx, &computepb.InsertSubnetworkRequest{Project: opsProject, Region: opsRegion,
		SubnetworkResource: &computepb.Subnetwork{Name: name("s1"), Network: name("global/networks/n1"), IpCidrRange: name("10.0.0.0/24")}})
	if err != nil {
		t.Fatalf("subnetwork insert: %v", err)
	}

	got := waitOp(t, op, "insert", "regions/"+opsRegion+"/subnetworks/s1", "regions/"+opsRegion)
	if !strings.HasSuffix(got.GetRegion(), "/regions/"+opsRegion) || got.GetZone() != "" || got.GetTargetId() == 0 {
		t.Errorf("regional op region %q zone %q", got.GetRegion(), got.GetZone())
	}
}
