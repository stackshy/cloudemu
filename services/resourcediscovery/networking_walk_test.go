package resourcediscovery

import (
	"context"
	"errors"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/vpc"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// Discovery is what a sweep for orphaned infrastructure walks. An elastic IP
// that never appears keeps costing money, and an interface that never appears
// keeps blocking the VPC delete nobody can explain.
func TestWalkNetworkingIncludesAddressesAndInterfaces(t *testing.T) {
	ctx := context.Background()
	opts := config.NewOptions()
	vpcMock := vpc.New(opts)

	v, err := vpcMock.CreateVPC(ctx, netdriver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	if err != nil {
		t.Fatalf("CreateVPC: %v", err)
	}

	sub, err := vpcMock.CreateSubnet(ctx, netdriver.SubnetConfig{
		VPCID: v.ID, CIDRBlock: "10.0.1.0/24",
	})
	if err != nil {
		t.Fatalf("CreateSubnet: %v", err)
	}

	if _, err := vpcMock.AllocateAddress(ctx, netdriver.ElasticIPConfig{}); err != nil {
		t.Fatalf("AllocateAddress: %v", err)
	}

	// A NAT gateway holds an interface for as long as it lives.
	if _, err := vpcMock.CreateNATGateway(ctx, netdriver.NATGatewayConfig{SubnetID: sub.ID}); err != nil {
		t.Fatalf("CreateNATGateway: %v", err)
	}

	eng := New(ProviderAWS, "123456789012", "us-east-1", &Drivers{Networking: vpcMock})

	got, err := eng.walkNetworking(ctx)
	if err != nil {
		t.Fatalf("walkNetworking: %v", err)
	}

	seen := map[string]int{}
	for _, r := range got {
		seen[r.Type]++
	}

	for _, want := range []string{TypeVPC, TypeSubnet, TypeElasticIP, TypeNetworkIface} {
		if seen[want] == 0 {
			t.Errorf("%s missing from discovery: %+v", want, seen)
		}
	}
}

// A driver that models interfaces and then fails to list them has a real
// problem. Reporting a complete inventory that silently omits whatever could
// not be read is worse than reporting the failure.
func TestWalkNetworkingPropagatesInterfaceErrors(t *testing.T) {
	eng := New(ProviderAWS, "123456789012", "us-east-1", &Drivers{
		Networking: failingInterfaces{Networking: vpc.New(config.NewOptions())},
	})

	if _, err := eng.walkNetworking(context.Background()); err == nil {
		t.Error("a failing interface listing should surface, not be swallowed")
	}
}

type failingInterfaces struct {
	netdriver.Networking
}

func (failingInterfaces) DescribeNetworkInterfaces(
	_ context.Context, _ []string,
) ([]netdriver.NetworkInterface, error) {
	return nil, errListFailed
}

func (failingInterfaces) DetachNetworkInterface(_ context.Context, _ string, _ bool) error {
	return nil
}

func (failingInterfaces) DeleteNetworkInterface(_ context.Context, _ string) error { return nil }

var errListFailed = errors.New("listing interfaces failed")
