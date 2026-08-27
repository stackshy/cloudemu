package persist_test

import (
	"encoding/json"
	"testing"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/persist"
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// TestOCIProviderRoundTripPreservesCrossRefs is the whole-provider proof for the
// OCI half of #582: a VCN + subnet, a user, a policy and a metric seeded across
// the Identity / VCN / Monitoring mocks all survive a full ExportAll -> JSON ->
// RestoreAll into a FRESH provider, and — crucially — the subnet still resolves
// to its VCN under the same OCIDs. The OCI provider exposes services as driver
// interfaces, so this also exercises the interface-aware discovery path.
func TestOCIProviderRoundTripPreservesCrossRefs(t *testing.T) {
	ctx := t.Context()
	src := cloudemu.NewOCI()

	vpc, err := src.VCN.CreateVPC(ctx, netdriver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	if err != nil {
		t.Fatalf("create vcn: %v", err)
	}

	subnet, err := src.VCN.CreateSubnet(ctx, netdriver.SubnetConfig{VPCID: vpc.ID, CIDRBlock: "10.0.1.0/24"})
	if err != nil {
		t.Fatalf("create subnet: %v", err)
	}

	if _, err = src.Identity.CreateUser(ctx, iamdriver.UserConfig{Name: "alice"}); err != nil {
		t.Fatalf("create user: %v", err)
	}

	pol, err := src.Identity.CreatePolicy(ctx, iamdriver.PolicyConfig{
		Name:           "p1",
		PolicyDocument: "Allow group Admins to manage all-resources in tenancy",
	})
	if err != nil {
		t.Fatalf("create policy: %v", err)
	}

	if err = src.Monitoring.PutMetricData(ctx, []mondriver.MetricDatum{
		{Namespace: "myapp", MetricName: "requests", Value: 7},
	}); err != nil {
		t.Fatalf("put metric: %v", err)
	}

	// Export the whole OCI provider and restore into a completely fresh one.
	snap, err := persist.ExportAll(ctx,
		map[string]persist.Services{"oci": src.SnapshotServices()}, persist.Options{})
	if err != nil {
		t.Fatalf("ExportAll: %v", err)
	}

	raw, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}

	var got persist.Snapshot
	if err = json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal snapshot: %v", err)
	}

	dst := cloudemu.NewOCI()
	if err = persist.RestoreAll(ctx, &got, map[string]persist.Services{"oci": dst.SnapshotServices()}); err != nil {
		t.Fatalf("RestoreAll: %v", err)
	}

	// The subnet keeps its OCID and its VCNID cross-reference resolves to the
	// restored VCN — a snapshot that dropped the VCN would leave it dangling.
	subnets, err := dst.VCN.DescribeSubnets(ctx, []string{subnet.ID})
	if err != nil {
		t.Fatalf("describe restored subnet: %v", err)
	}
	if len(subnets) != 1 || subnets[0].VPCID != vpc.ID {
		t.Fatalf("subnet %q does not resolve to restored VCN %q: %v", subnet.ID, vpc.ID, subnets)
	}

	vpcs, err := dst.VCN.DescribeVPCs(ctx, []string{vpc.ID})
	if err != nil {
		t.Fatalf("describe restored vcn: %v", err)
	}
	if len(vpcs) != 1 || vpcs[0].ID != vpc.ID {
		t.Fatalf("VCN %q not restored: %v", vpc.ID, vpcs)
	}

	// The user and policy survived under their identities.
	if _, err = dst.Identity.GetUser(ctx, "alice"); err != nil {
		t.Fatalf("restored user alice: %v", err)
	}

	if _, err = dst.Identity.GetPolicy(ctx, pol.ID); err != nil {
		t.Fatalf("restored policy %q: %v", pol.ID, err)
	}

	// The metric survived.
	names, err := dst.Monitoring.ListMetrics(ctx, "myapp")
	if err != nil {
		t.Fatalf("list restored metrics: %v", err)
	}
	if len(names) != 1 || names[0] != "requests" {
		t.Fatalf("restored metric names = %v, want [requests]", names)
	}
}
