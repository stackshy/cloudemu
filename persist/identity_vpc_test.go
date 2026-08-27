package persist_test

import (
	"context"
	"encoding/json"
	"testing"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/persist"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// TestVPCInstanceCrossRefPreservedAcrossRestore is the point of snapshotting the
// networking layer alongside compute: an EC2 instance launched into a VPC subnet
// keeps its SubnetID / VPCID / security-group references after a full
// Export→Restore into a FRESH provider, and — crucially — those references still
// RESOLVE, because the subnet, VPC and security group themselves were captured
// and restored under the SAME ids. A snapshot that carried the instance but not
// its networking would leave a dangling reference (bug #582).
func TestVPCInstanceCrossRefPreservedAcrossRestore(t *testing.T) {
	ctx := context.Background()

	src := cloudemu.NewAWS()

	vpc, err := src.VPC.CreateVPC(ctx, netdriver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	if err != nil {
		t.Fatalf("create vpc: %v", err)
	}

	subnet, err := src.VPC.CreateSubnet(ctx, netdriver.SubnetConfig{VPCID: vpc.ID, CIDRBlock: "10.0.1.0/24"})
	if err != nil {
		t.Fatalf("create subnet: %v", err)
	}

	sg, err := src.VPC.CreateSecurityGroup(ctx, netdriver.SecurityGroupConfig{
		Name: "app", Description: "app tier", VPCID: vpc.ID,
	})
	if err != nil {
		t.Fatalf("create security group: %v", err)
	}

	launched, err := src.EC2.RunInstances(ctx, computedriver.InstanceConfig{
		ImageID: "ami-123", InstanceType: "t3.micro",
		SubnetID: subnet.ID, SecurityGroups: []string{sg.ID},
	}, 1)
	if err != nil {
		t.Fatalf("run instances: %v", err)
	}
	if len(launched) != 1 {
		t.Fatalf("launched %d instances, want 1", len(launched))
	}

	wantInstanceID := launched[0].ID

	// Export the whole emulator (compute + networking are both Snapshottable) and
	// restore into a completely fresh provider.
	snap, err := persist.ExportAll(ctx, map[string]persist.Services{"aws": src.SnapshotServices()}, persist.Options{IncludeAssets: true})
	if err != nil {
		t.Fatalf("ExportAll: %v", err)
	}

	raw, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}

	var got persist.Snapshot
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal snapshot: %v", err)
	}

	dst := cloudemu.NewAWS()
	if err := persist.RestoreAll(ctx, &got, map[string]persist.Services{"aws": dst.SnapshotServices()}); err != nil {
		t.Fatalf("RestoreAll: %v", err)
	}

	// The instance keeps its identity and every networking cross-reference.
	insts, err := dst.EC2.DescribeInstances(ctx, []string{wantInstanceID}, nil)
	if err != nil {
		t.Fatalf("describe restored instances: %v", err)
	}
	if len(insts) != 1 {
		t.Fatalf("restored %d instances, want 1", len(insts))
	}

	inst := insts[0]
	if inst.ID != wantInstanceID {
		t.Fatalf("restored instance id = %q, want SAME id %q", inst.ID, wantInstanceID)
	}
	if inst.SubnetID != subnet.ID {
		t.Fatalf("restored instance SubnetID = %q, want %q", inst.SubnetID, subnet.ID)
	}
	if inst.VPCID != vpc.ID {
		t.Fatalf("restored instance VPCID = %q, want %q", inst.VPCID, vpc.ID)
	}
	if len(inst.SecurityGroups) != 1 || inst.SecurityGroups[0] != sg.ID {
		t.Fatalf("restored instance SGs = %v, want [%s]", inst.SecurityGroups, sg.ID)
	}

	// The referenced networking resources themselves survived under the same ids,
	// so the instance's references are NOT dangling.
	subnets, err := dst.VPC.DescribeSubnets(ctx, []string{inst.SubnetID})
	if err != nil {
		t.Fatalf("describe restored subnet: %v", err)
	}
	if len(subnets) != 1 || subnets[0].VPCID != vpc.ID {
		t.Fatalf("instance SubnetID %q does not resolve to a restored subnet in VPC %q: %v", inst.SubnetID, vpc.ID, subnets)
	}

	sgs, err := dst.VPC.DescribeSecurityGroups(ctx, []string{sg.ID})
	if err != nil {
		t.Fatalf("describe restored security group: %v", err)
	}
	if len(sgs) != 1 || sgs[0].ID != sg.ID {
		t.Fatalf("instance SG %q does not resolve to a restored security group: %v", sg.ID, sgs)
	}

	vpcs, err := dst.VPC.DescribeVPCs(ctx, []string{vpc.ID})
	if err != nil {
		t.Fatalf("describe restored vpc: %v", err)
	}
	if len(vpcs) != 1 || vpcs[0].ID != vpc.ID {
		t.Fatalf("instance VPCID %q does not resolve to a restored VPC: %v", vpc.ID, vpcs)
	}
}
