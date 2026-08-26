package vpc

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// describeSubnetCount returns the AvailableIPAddressCount DescribeSubnets reports
// for the given subnet.
func describeSubnetCount(t *testing.T, m *Mock, subnetID string) int {
	t.Helper()

	subs, err := m.DescribeSubnets(context.Background(), []string{subnetID})
	requireNoError(t, err)

	if len(subs) != 1 {
		t.Fatalf("DescribeSubnets returned %d subnets, want 1", len(subs))
	}

	return subs[0].AvailableIPAddressCount
}

// TestSubnetAvailableIPAddressCount pins that a fresh subnet advertises its CIDR's
// host space minus AWS's five reserved addresses, that launching an instance into
// it consumes one address (its primary ENI), and that terminating the instance
// gives the address back.
func TestSubnetAvailableIPAddressCount(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	v := createTestVPC(m)

	// A /24 has 256 addresses; AWS reserves 5, so a fresh subnet reports 251.
	const wantEmpty = 251

	sub, err := m.CreateSubnet(ctx, driver.SubnetConfig{VPCID: v.ID, CIDRBlock: "10.0.1.0/24"})
	requireNoError(t, err)
	assertEqual(t, wantEmpty, sub.AvailableIPAddressCount)
	assertEqual(t, wantEmpty, describeSubnetCount(t, m, sub.ID))

	// Launching an instance materializes its primary ENI in the subnet, consuming
	// one address.
	requireNoError(t, m.CreatePrimaryNetworkInterface(ctx, "i-123", sub.ID, nil))
	assertEqual(t, wantEmpty-1, describeSubnetCount(t, m, sub.ID))

	// A standalone ENI consumes another.
	_, err = m.CreateNetworkInterface(ctx, sub.ID, "extra", nil, nil)
	requireNoError(t, err)
	assertEqual(t, wantEmpty-2, describeSubnetCount(t, m, sub.ID))

	// Terminating the instance releases its primary ENI, re-incrementing the count.
	requireNoError(t, m.ReleaseInstanceNetworkInterfaces(ctx, "i-123"))
	assertEqual(t, wantEmpty-1, describeSubnetCount(t, m, sub.ID))
}

// TestSubnetAvailableIPAddressCountPrefixSizes pins the reserved-address math across
// prefix lengths.
func TestSubnetAvailableIPAddressCountPrefixSizes(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	v := createTestVPC(m)

	cases := []struct {
		cidr string
		want int
	}{
		{"10.0.0.0/25", 123}, // 128 - 5
		{"10.0.2.0/28", 11},  // 16 - 5
	}

	for _, c := range cases {
		sub, err := m.CreateSubnet(ctx, driver.SubnetConfig{VPCID: v.ID, CIDRBlock: c.cidr})
		requireNoError(t, err)
		assertEqual(t, c.want, sub.AvailableIPAddressCount)
	}
}
