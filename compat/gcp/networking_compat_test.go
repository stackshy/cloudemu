package gcp

import (
	"context"
	"errors"
	"testing"

	gcpcompute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/internal/compat"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

// TestCompatGCPNetworkingVPC drives the real cloud.google.com/go/compute REST
// clients against CloudEmu's in-process GCP wire server and records one compat
// result per portable networking op the VPC handler routes: networks,
// subnetworks and firewalls each through create -> describe -> delete.
func TestCompatGCPNetworkingVPC(t *testing.T) {
	const (
		service = "networking"
		region  = "us-central1"
		netName = "compat-net"
		subName = "compat-subnet"
		fwName  = "compat-fw"
	)

	ctx := context.Background()

	cloudP := cloudemu.NewGCP()
	// Compute is registered too so the shared operations-polling endpoint is
	// wired up — networking mutations return Operation envelopes the SDK polls.
	sess := compat.BootGCP(t, gcpserver.Drivers{Networking: cloudP.VPC, Compute: cloudP.GCE})

	opts := []option.ClientOption{
		option.WithEndpoint(sess.Endpoint()),
		option.WithoutAuthentication(),
		option.WithHTTPClient(sess.Transport()),
	}

	nets, err := gcpcompute.NewNetworksRESTClient(ctx, opts...)
	if err != nil {
		t.Fatalf("NewNetworksRESTClient: %v", err)
	}
	t.Cleanup(func() { _ = nets.Close() })

	subs, err := gcpcompute.NewSubnetworksRESTClient(ctx, opts...)
	if err != nil {
		t.Fatalf("NewSubnetworksRESTClient: %v", err)
	}
	t.Cleanup(func() { _ = subs.Close() })

	fws, err := gcpcompute.NewFirewallsRESTClient(ctx, opts...)
	if err != nil {
		t.Fatalf("NewFirewallsRESTClient: %v", err)
	}
	t.Cleanup(func() { _ = fws.Close() })

	proj := compat.GCPProject
	falseVal := false

	// CreateVPC — insert a network.
	sess.Op(service, "CreateVPC", func() error {
		op, oErr := nets.Insert(ctx, &computepb.InsertNetworkRequest{
			Project: proj,
			NetworkResource: &computepb.Network{
				Name:                  &[]string{netName}[0],
				AutoCreateSubnetworks: &falseVal,
			},
		})
		if oErr != nil {
			return oErr
		}

		return op.Wait(ctx)
	})

	// CreateSubnet — insert a regional subnetwork under the network.
	sess.Op(service, "CreateSubnet", func() error {
		op, oErr := subs.Insert(ctx, &computepb.InsertSubnetworkRequest{
			Project: proj,
			Region:  region,
			SubnetworkResource: &computepb.Subnetwork{
				Name:        &[]string{subName}[0],
				Network:     &[]string{netName}[0],
				IpCidrRange: &[]string{"10.0.1.0/24"}[0],
			},
		})
		if oErr != nil {
			return oErr
		}

		return op.Wait(ctx)
	})

	// CreateSecurityGroup — insert a firewall (GCP firewalls map to SGs).
	sess.Op(service, "CreateSecurityGroup", func() error {
		op, oErr := fws.Insert(ctx, &computepb.InsertFirewallRequest{
			Project: proj,
			FirewallResource: &computepb.Firewall{
				Name:      &[]string{fwName}[0],
				Direction: &[]string{"INGRESS"}[0],
				Allowed: []*computepb.Allowed{{
					IPProtocol: &[]string{"tcp"}[0],
					Ports:      []string{"443"},
				}},
				SourceRanges: []string{"10.0.0.0/8"},
			},
		})
		if oErr != nil {
			return oErr
		}

		return op.Wait(ctx)
	})

	// DescribeVPCs — get then list the network.
	sess.Op(service, "DescribeVPCs", func() error {
		if _, gErr := nets.Get(ctx, &computepb.GetNetworkRequest{Project: proj, Network: netName}); gErr != nil {
			return gErr
		}

		it := nets.List(ctx, &computepb.ListNetworksRequest{Project: proj})
		_, lErr := it.Next()

		return skipIteratorDone(lErr)
	})

	// DescribeSubnets — get then list the subnetwork.
	sess.Op(service, "DescribeSubnets", func() error {
		if _, gErr := subs.Get(ctx, &computepb.GetSubnetworkRequest{
			Project: proj, Region: region, Subnetwork: subName,
		}); gErr != nil {
			return gErr
		}

		it := subs.List(ctx, &computepb.ListSubnetworksRequest{Project: proj, Region: region})
		_, lErr := it.Next()

		return skipIteratorDone(lErr)
	})

	// DescribeSecurityGroups — get then list the firewall.
	sess.Op(service, "DescribeSecurityGroups", func() error {
		if _, gErr := fws.Get(ctx, &computepb.GetFirewallRequest{Project: proj, Firewall: fwName}); gErr != nil {
			return gErr
		}

		it := fws.List(ctx, &computepb.ListFirewallsRequest{Project: proj})
		_, lErr := it.Next()

		return skipIteratorDone(lErr)
	})

	// DeleteSubnet — delete the subnetwork.
	sess.Op(service, "DeleteSubnet", func() error {
		op, oErr := subs.Delete(ctx, &computepb.DeleteSubnetworkRequest{
			Project: proj, Region: region, Subnetwork: subName,
		})
		if oErr != nil {
			return oErr
		}

		return op.Wait(ctx)
	})

	// DeleteSecurityGroup — delete the firewall.
	sess.Op(service, "DeleteSecurityGroup", func() error {
		op, oErr := fws.Delete(ctx, &computepb.DeleteFirewallRequest{Project: proj, Firewall: fwName})
		if oErr != nil {
			return oErr
		}

		return op.Wait(ctx)
	})

	// DeleteVPC — delete the network.
	sess.Op(service, "DeleteVPC", func() error {
		op, oErr := nets.Delete(ctx, &computepb.DeleteNetworkRequest{Project: proj, Network: netName})
		if oErr != nil {
			return oErr
		}

		return op.Wait(ctx)
	})
}

// skipIteratorDone treats the iterator's terminal sentinel as success — a List
// that yields no error before exhaustion still proves the op is routed.
func skipIteratorDone(err error) error {
	if errors.Is(err, iterator.Done) {
		return nil
	}

	return err
}

// TestRouteNetworkSelfLink proves compute.routes.get normalizes the Route's
// network reference to a fully-qualified self-link URL on read, rather than
// echoing the caller's relative "global/networks/{name}" verbatim (a perpetual
// Terraform google_compute_route.network diff pre-fix). The global next-hop
// gateway reference is normalized the same way.
func TestRouteNetworkSelfLink(t *testing.T) {
	const (
		routeNet  = "compat-route-net"
		routeName = "compat-route"
	)

	ctx := context.Background()
	cloudP := cloudemu.NewGCP()
	sess := compat.BootGCP(t, gcpserver.Drivers{Networking: cloudP.VPC, Compute: cloudP.GCE})

	opts := []option.ClientOption{
		option.WithEndpoint(sess.Endpoint()),
		option.WithoutAuthentication(),
		option.WithHTTPClient(sess.Transport()),
	}

	nets, err := gcpcompute.NewNetworksRESTClient(ctx, opts...)
	if err != nil {
		t.Fatalf("NewNetworksRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = nets.Close() })

	routes, err := gcpcompute.NewRoutesRESTClient(ctx, opts...)
	if err != nil {
		t.Fatalf("NewRoutesRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = routes.Close() })

	strp := func(s string) *string { return &s }
	boolp := func(b bool) *bool { return &b }
	project := compat.GCPProject

	netOp, err := nets.Insert(ctx, &computepb.InsertNetworkRequest{
		Project: project,
		NetworkResource: &computepb.Network{
			Name:                  strp(routeNet),
			AutoCreateSubnetworks: boolp(false),
		},
	})
	if err != nil {
		t.Fatalf("insert network: %v", err)
	}

	if werr := netOp.Wait(ctx); werr != nil {
		t.Fatalf("wait network: %v", werr)
	}

	// Insert a route referencing the network by a relative path — the shape
	// Terraform sends.
	rtOp, err := routes.Insert(ctx, &computepb.InsertRouteRequest{
		Project: project,
		RouteResource: &computepb.Route{
			Name:           strp(routeName),
			Network:        strp("global/networks/" + routeNet),
			DestRange:      strp("10.20.0.0/16"),
			NextHopGateway: strp("global/gateways/default-internet-gateway"),
			Priority:       func() *uint32 { p := uint32(1000); return &p }(),
		},
	})
	if err != nil {
		t.Fatalf("insert route: %v", err)
	}

	if werr := rtOp.Wait(ctx); werr != nil {
		t.Fatalf("wait route: %v", werr)
	}

	got, err := routes.Get(ctx, &computepb.GetRouteRequest{Project: project, Route: routeName})
	if err != nil {
		t.Fatalf("get route: %v", err)
	}

	wantNet := sess.Endpoint() + "/compute/v1/projects/" + project + "/global/networks/" + routeNet
	if got.GetNetwork() != wantNet {
		t.Fatalf("route network = %q, want self-link %q", got.GetNetwork(), wantNet)
	}

	wantGw := sess.Endpoint() + "/compute/v1/projects/" + project + "/global/gateways/default-internet-gateway"
	if got.GetNextHopGateway() != wantGw {
		t.Fatalf("route nextHopGateway = %q, want self-link %q", got.GetNextHopGateway(), wantGw)
	}
}
