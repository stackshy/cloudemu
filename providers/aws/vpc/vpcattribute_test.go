package vpc

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

func TestModifyVPCAttributePartialUpdate(t *testing.T) {
	ctx := context.Background()
	m := New(config.NewOptions())

	v, err := m.CreateVPC(ctx, driver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	if err != nil {
		t.Fatalf("CreateVPC: %v", err)
	}

	// EC2 defaults DNS support on and hostnames off for a new VPC.
	if !v.EnableDNSSupport || v.EnableDNSHostnames {
		t.Fatalf("unexpected defaults: support=%v hostnames=%v",
			v.EnableDNSSupport, v.EnableDNSHostnames)
	}

	// The real API takes one attribute per call, so setting hostnames must not
	// disturb DNS support — a nil pointer means "unchanged", not "false".
	on := true
	if err := m.ModifyVPCAttribute(ctx, v.ID, driver.VPCAttributeUpdate{
		EnableDNSHostnames: &on,
	}); err != nil {
		t.Fatalf("ModifyVPCAttribute: %v", err)
	}

	got, err := m.DescribeVPCs(ctx, []string{v.ID})
	if err != nil || len(got) != 1 {
		t.Fatalf("DescribeVPCs: %v (%d)", err, len(got))
	}

	if !got[0].EnableDNSHostnames {
		t.Error("dns hostnames was not enabled")
	}

	if !got[0].EnableDNSSupport {
		t.Error("dns support was cleared by an unrelated attribute write")
	}
}

func TestModifyVPCAttributeUnknownVPC(t *testing.T) {
	on := true
	if err := New(config.NewOptions()).
		ModifyVPCAttribute(context.Background(), "vpc-nope",
			driver.VPCAttributeUpdate{EnableDNSSupport: &on}); err == nil {
		t.Error("modifying an unknown VPC should fail")
	}
}
