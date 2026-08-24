package vpc

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// TestNetworkResourceTagger covers UpdateResourceTags/RemoveResourceTags for the
// VPC-family resources routed through the optional interface: the tag lands on
// the owning store, delete removes it, and an unknown id is NotFound.
func TestNetworkResourceTagger(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	v := createTestVPC(m)

	rt, err := m.CreateRouteTable(ctx, driver.RouteTableConfig{VPCID: v.ID})
	requireNoError(t, err)

	igw, err := m.CreateInternetGateway(ctx, driver.InternetGatewayConfig{})
	requireNoError(t, err)

	dopt, err := m.CreateDHCPOptions(ctx, driver.DHCPOptionsConfig{
		Configuration: map[string][]string{"domain-name-servers": {"10.0.0.2"}},
	})
	requireNoError(t, err)

	for _, id := range []string{rt.ID, igw.ID, dopt.ID} {
		requireNoError(t, m.UpdateResourceTags(ctx, id, map[string]string{"Name": "n"}))
	}

	assertEqual(t, "n", routeTableTag(t, m, rt.ID, "Name"))

	requireNoError(t, m.RemoveResourceTags(ctx, rt.ID, []string{"Name"}))
	assertEqual(t, "", routeTableTag(t, m, rt.ID, "Name"))

	if err := m.UpdateResourceTags(ctx, "rtb-missing", map[string]string{"k": "v"}); !isNotFound(err) {
		t.Fatalf("UpdateResourceTags on missing id = %v, want NotFound", err)
	}

	if err := m.RemoveResourceTags(ctx, "vol-notnetwork", []string{"k"}); !isNotFound(err) {
		t.Fatalf("RemoveResourceTags on non-network id = %v, want NotFound", err)
	}
}

func routeTableTag(t *testing.T, m *Mock, id, key string) string {
	t.Helper()

	rts, err := m.DescribeRouteTables(context.Background(), []string{id})
	requireNoError(t, err)

	return rts[0].Tags[key]
}

func isNotFound(err error) bool {
	return err != nil && errors.IsNotFound(err)
}
