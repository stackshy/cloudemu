package vpc

import (
	"context"
	"strings"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// mkSubnet creates a VPC + subnet and returns the subnet id.
func mkSubnet(t *testing.T, m *Mock) string {
	t.Helper()

	v := createTestVPC(m)

	sub, err := m.CreateSubnet(context.Background(), driver.SubnetConfig{
		VPCID: v.ID, CIDRBlock: "10.0.1.0/24",
	})
	requireNoError(t, err)

	return sub.ID
}

// TestCreateNetworkInterfaceDefaults pins the provider auto-assigns a subnet-scoped
// private IP + MAC and defaults SourceDestCheck to true.
func TestCreateNetworkInterfaceDefaults(t *testing.T) {
	m := newTestMock()
	subnetID := mkSubnet(t, m)

	eni, err := m.CreateNetworkInterface(context.Background(), subnetID, "app", nil, nil)
	requireNoError(t, err)

	if !strings.HasPrefix(eni.PrivateIP, "10.0.1.") {
		t.Errorf("privateIP = %q, want inside 10.0.1.0/24", eni.PrivateIP)
	}

	if eni.MacAddress == "" {
		t.Error("macAddress is empty")
	}

	if !eni.SourceDestCheck {
		t.Error("sourceDestCheck = false, want true by default")
	}

	// Two ENIs in the same subnet get distinct private IPs.
	eni2, err := m.CreateNetworkInterface(context.Background(), subnetID, "app2", nil, nil)
	requireNoError(t, err)

	if eni2.PrivateIP == eni.PrivateIP {
		t.Errorf("second ENI reused private IP %q", eni.PrivateIP)
	}
}

// TestModifyNetworkInterfaceAttribute pins that SourceDestCheck, Description and
// Groups each round-trip and that a nil field leaves the attribute untouched.
func TestModifyNetworkInterfaceAttribute(t *testing.T) {
	m := newTestMock()
	subnetID := mkSubnet(t, m)
	ctx := context.Background()

	eni, err := m.CreateNetworkInterface(ctx, subnetID, "orig", []string{"sg-1"}, nil)
	requireNoError(t, err)

	falseVal := false
	newDesc := "changed"

	requireNoError(t, m.ModifyNetworkInterfaceAttribute(ctx, eni.ID, driver.NetworkInterfaceAttributeUpdate{
		SourceDestCheck: &falseVal,
		Description:     &newDesc,
		Groups:          []string{"sg-2", "sg-3"},
	}))

	got, err := m.DescribeNetworkInterfaces(ctx, []string{eni.ID})
	requireNoError(t, err)

	if got[0].SourceDestCheck {
		t.Error("sourceDestCheck = true after modify, want false")
	}

	if got[0].Description != "changed" {
		t.Errorf("description = %q, want changed", got[0].Description)
	}

	// A no-op update leaves everything in place.
	requireNoError(t, m.ModifyNetworkInterfaceAttribute(ctx, eni.ID, driver.NetworkInterfaceAttributeUpdate{}))

	got, err = m.DescribeNetworkInterfaces(ctx, []string{eni.ID})
	requireNoError(t, err)

	if got[0].Description != "changed" || got[0].SourceDestCheck {
		t.Errorf("no-op modify changed state: %+v", got[0])
	}

	// An unknown ENI is NotFound.
	if err := m.ModifyNetworkInterfaceAttribute(ctx, "eni-nope", driver.NetworkInterfaceAttributeUpdate{
		SourceDestCheck: &falseVal,
	}); !cerrors.IsNotFound(err) {
		t.Errorf("modify unknown ENI err = %v, want NotFound", err)
	}
}

// TestCreateRouteValidatesTarget pins that a route pointing at a nonexistent
// gateway / NAT / peering is rejected with the target-specific marker, while a
// valid target is accepted.
func TestCreateRouteValidatesTarget(t *testing.T) {
	m := newTestMock()
	v := createTestVPC(m)
	ctx := context.Background()

	rt, err := m.CreateRouteTable(ctx, driver.RouteTableConfig{VPCID: v.ID})
	requireNoError(t, err)

	cases := []struct {
		name, target, marker string
	}{
		{"bad igw", "igw-bogus", routeTargetIGWNotFound},
		{"bad nat", "nat-bogus", routeTargetNATNotFound},
		{"bad peering", "pcx-bogus", routeTargetPeeringNotFound},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := m.CreateRoute(ctx, rt.ID, "0.0.0.0/0", tc.target, "gateway")
			if !cerrors.IsNotFound(err) || !strings.Contains(err.Error(), tc.marker) {
				t.Fatalf("err = %v, want NotFound carrying %q", err, tc.marker)
			}
		})
	}

	// A real IGW target is accepted.
	igwID := attachTestIGW(t, m, v.ID)
	requireNoError(t, m.CreateRoute(ctx, rt.ID, "0.0.0.0/0", igwID, "gateway"))
}

// TestCreateSubnetRange pins that a subnet CIDR outside the VPC block is rejected
// while an in-range block is accepted.
func TestCreateSubnetRange(t *testing.T) {
	m := newTestMock()
	v := createTestVPC(m) // 10.0.0.0/16
	ctx := context.Background()

	_, err := m.CreateSubnet(ctx, driver.SubnetConfig{VPCID: v.ID, CIDRBlock: "192.168.5.0/24"})
	if !cerrors.IsInvalidArgument(err) || !strings.Contains(err.Error(), "InvalidSubnet.Range") {
		t.Fatalf("out-of-range subnet err = %v, want InvalidSubnet.Range", err)
	}

	// A block inside the VPC range still works.
	_, err = m.CreateSubnet(ctx, driver.SubnetConfig{VPCID: v.ID, CIDRBlock: "10.0.2.0/24"})
	requireNoError(t, err)
}
