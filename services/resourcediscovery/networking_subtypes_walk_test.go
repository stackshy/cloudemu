package resourcediscovery

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/vpc"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

func TestWalkNetworkingSubTypes(t *testing.T) {
	ctx := context.Background()
	opts := config.NewOptions(config.WithClock(config.NewFakeClock(time.Unix(0, 0))))
	n := vpc.New(opts)

	v, err := n.CreateVPC(ctx, netdriver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	if err != nil {
		t.Fatalf("create vpc: %v", err)
	}

	sub, err := n.CreateSubnet(ctx, netdriver.SubnetConfig{VPCID: v.ID, CIDRBlock: "10.0.1.0/24"})
	if err != nil {
		t.Fatalf("create subnet: %v", err)
	}

	if _, err := n.CreateNATGateway(ctx,
		netdriver.NATGatewayConfig{SubnetID: sub.ID, ConnectivityType: "private"}); err != nil {
		t.Fatalf("create nat gateway: %v", err)
	}

	if _, err := n.CreateInternetGateway(ctx, netdriver.InternetGatewayConfig{}); err != nil {
		t.Fatalf("create internet gateway: %v", err)
	}

	v2, err := n.CreateVPC(ctx, netdriver.VPCConfig{CIDRBlock: "10.1.0.0/16"})
	if err != nil {
		t.Fatalf("create vpc2: %v", err)
	}

	if _, err := n.CreatePeeringConnection(ctx, netdriver.PeeringConfig{RequesterVPC: v.ID, AccepterVPC: v2.ID}); err != nil {
		t.Fatalf("create peering: %v", err)
	}

	if _, err := n.CreateRouteTable(ctx, netdriver.RouteTableConfig{VPCID: v.ID}); err != nil {
		t.Fatalf("create route table: %v", err)
	}

	eng := New(ProviderAWS, "123456789012", "us-east-1", &Drivers{Networking: n})

	res, err := eng.ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}

	want := map[string]int{
		TypeNATGateway:        0,
		TypeInternetGateway:   0,
		TypePeeringConnection: 0,
		TypeRouteTable:        0,
	}
	for i := range res {
		if res[i].Service == ServiceNetworking {
			if _, ok := want[res[i].Type]; ok {
				want[res[i].Type]++
			}
		}
	}

	for typ, n := range want {
		if n < 1 {
			t.Errorf("expected at least 1 discovered %s, got %d", typ, n)
		}
	}
}
