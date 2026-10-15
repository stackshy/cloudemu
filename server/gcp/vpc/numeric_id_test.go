package vpc_test

import (
	"context"
	"math"
	"strconv"
	"testing"

	"cloud.google.com/go/compute/apiv1/computepb"
)

// TestSubnetworkIDFitsInt64 creates many subnetworks and checks every wire id
// fits an int64. Terraform reads subnetwork_id as an int and fails on a larger
// uint64, which a hashed id hits about half the time.
func TestSubnetworkIDFitsInt64(t *testing.T) {
	const subnets = 32

	ctx := context.Background()
	c := newProjClients(t)

	op, err := c.nets.Insert(ctx, &computepb.InsertNetworkRequest{
		Project:         projA,
		NetworkResource: &computepb.Network{Name: ptrStr("ids"), AutoCreateSubnetworks: ptrBool(false)},
	})
	waitOp(t, ctx, "network insert", op, err)

	for i := range subnets {
		name := "s" + strconv.Itoa(i)
		op, err := c.subs.Insert(ctx, &computepb.InsertSubnetworkRequest{
			Project: projA, Region: testRegion,
			SubnetworkResource: &computepb.Subnetwork{
				Name: ptrStr(name), Network: ptrStr("projects/" + projA + "/global/networks/ids"),
				IpCidrRange: ptrStr("10." + strconv.Itoa(i) + ".0.0/24"),
			},
		})
		waitOp(t, ctx, name+" insert", op, err)

		got, err := c.subs.Get(ctx, &computepb.GetSubnetworkRequest{Project: projA, Region: testRegion, Subnetwork: name})
		if err != nil {
			t.Fatalf("%s get: %v", name, err)
		}

		if got.GetId() > math.MaxInt64 {
			t.Errorf("%s id %d does not fit int64", name, got.GetId())
		}
	}
}
