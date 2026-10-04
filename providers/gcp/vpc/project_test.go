package vpc

import (
	"bytes"
	"context"
	"log"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/internal/projectctx"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

func TestDescribeIsScopedToProject(t *testing.T) {
	m := newTestMock()
	bg := context.Background()
	pb := projectctx.WithProject(bg, "p-b")

	for _, ctx := range []context.Context{bg, pb} {
		v, err := m.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
		if err != nil {
			t.Fatal(err)
		}

		if _, err := m.CreateSubnet(ctx, driver.SubnetConfig{VPCID: v.ID, CIDRBlock: "10.0.1.0/24"}); err != nil {
			t.Fatal(err)
		}

		if _, err := m.CreateSecurityGroup(ctx, driver.SecurityGroupConfig{Name: "fw", VPCID: v.ID}); err != nil {
			t.Fatal(err)
		}
	}

	tests := []struct {
		name string
		ctx  context.Context
		want int
	}{
		{"unstamped is the default project", bg, 1},
		{"stamped project", pb, 1},
		{"other project", projectctx.WithProject(bg, "p-c"), 0},
		{"all projects", projectctx.AllProjects(bg), 2},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			vpcs, _ := m.DescribeVPCs(tc.ctx, nil)
			subs, _ := m.DescribeSubnets(tc.ctx, nil)
			fws, _ := m.DescribeSecurityGroups(tc.ctx, nil)

			if len(vpcs) != tc.want || len(subs) != tc.want || len(fws) != tc.want {
				t.Fatalf("vpcs=%d subnets=%d firewalls=%d, want %d each", len(vpcs), len(subs), len(fws), tc.want)
			}
		})
	}
}

func TestRestoreAdoptsLegacyRecords(t *testing.T) {
	ctx := context.Background()
	src := newTestMock()
	src.vpcs.Set("n1", &vpcData{ID: "n1", Tags: map[string]string{}})
	src.subnets.Set("s1", &subnetData{ID: "s1", VPCID: "n1"})

	data, err := src.Snapshot(ctx, false)
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer

	prev, prevFlags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)

	t.Cleanup(func() { log.SetOutput(prev); log.SetFlags(prevFlags) })

	dst := newTestMock()
	if err := dst.Restore(ctx, data); err != nil {
		t.Fatal(err)
	}

	if got := strings.Count(buf.String(), "gcp/vpc: adopted 2 legacy"); got != 1 {
		t.Fatalf("want one adoption warning for 2 records, log: %q", buf.String())
	}

	if subs, _ := dst.DescribeSubnets(ctx, nil); len(subs) != 1 || subs[0].Tags[ProjectTag] != "test-project" {
		t.Errorf("default project subnets = %+v, want the adopted one", subs)
	}

	if vpcs, _ := dst.DescribeVPCs(projectctx.WithProject(ctx, "p-b"), nil); len(vpcs) != 0 {
		t.Errorf("p-b sees %d legacy networks, want 0", len(vpcs))
	}
}
