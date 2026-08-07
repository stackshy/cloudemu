package ec2_test

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/ec2"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

type fakeSubnetResolver struct{ vpc string }

func (f fakeSubnetResolver) DescribeSubnets(_ context.Context, ids []string) ([]netdriver.SubnetInfo, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	return []netdriver.SubnetInfo{{ID: ids[0], VPCID: f.vpc}}, nil
}

func newEC2(t *testing.T) *ec2.Mock {
	t.Helper()
	opts := config.NewOptions(config.WithClock(config.NewFakeClock(time.Unix(0, 0))))

	return ec2.New(opts)
}

// TestRunInstancesResolvesVPCFromSubnet is what makes the topology feature work
// via the wire: an instance launched with a subnet must carry the subnet's VPC.
func TestRunInstancesResolvesVPCFromSubnet(t *testing.T) {
	ctx := context.Background()
	m := newEC2(t)
	m.SetSubnetResolver(fakeSubnetResolver{vpc: "vpc-abc"})

	insts, err := m.RunInstances(ctx, computedriver.InstanceConfig{ImageID: "ami-1", SubnetID: "subnet-1"}, 1)
	if err != nil || len(insts) != 1 {
		t.Fatalf("RunInstances: %v %d", err, len(insts))
	}
	if insts[0].VPCID != "vpc-abc" {
		t.Fatalf("instance VPCID = %q, want vpc-abc", insts[0].VPCID)
	}
}

// TestRunInstancesNoSubnetNoVPC confirms an instance without a subnet (or with
// no resolver wired) simply has an empty VPCID rather than erroring.
func TestRunInstancesNoSubnetNoVPC(t *testing.T) {
	ctx := context.Background()
	m := newEC2(t)
	m.SetSubnetResolver(fakeSubnetResolver{vpc: "vpc-abc"})

	insts, err := m.RunInstances(ctx, computedriver.InstanceConfig{ImageID: "ami-1"}, 1)
	if err != nil || len(insts) != 1 {
		t.Fatalf("RunInstances: %v %d", err, len(insts))
	}
	if insts[0].VPCID != "" {
		t.Fatalf("instance VPCID = %q, want empty", insts[0].VPCID)
	}
}
