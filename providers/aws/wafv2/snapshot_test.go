package wafv2

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/wafv2/driver"
)

// TestSnapshotRoundTripWAFV2 proves a snapshot/restore round-trip preserves the
// promoted resource stores (whose *xxxData carry unexported driver values) and
// the mutex-guarded association side map under their original identities.
func TestSnapshotRoundTripWAFV2(t *testing.T) {
	ctx := context.Background()
	src := newMock()

	acl, err := src.CreateWebACL(ctx, driver.CreateWebACLInput{
		Name:  "acl1",
		Scope: driver.ScopeRegional,
		Rules: json.RawMessage(`[{"Name":"r1"}]`),
	})
	if err != nil {
		t.Fatalf("create web acl: %v", err)
	}

	ipset, err := src.CreateIPSet(ctx, driver.CreateIPSetInput{
		Name: "ips1", Scope: driver.ScopeRegional, IPAddressVersion: "IPV4",
		Addresses: []string{"10.0.0.0/24"},
	})
	if err != nil {
		t.Fatalf("create ip set: %v", err)
	}

	const protectedARN = "arn:aws:elasticloadbalancing:us-east-1:000000000000:loadbalancer/app/x/y"
	if err := src.AssociateWebACL(ctx, acl.ARN, protectedARN); err != nil {
		t.Fatalf("associate: %v", err)
	}

	raw, err := src.Snapshot(ctx, true)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	dst := newMock()
	if err := dst.Restore(ctx, raw); err != nil {
		t.Fatalf("restore: %v", err)
	}

	gotACL, err := dst.GetWebACL(ctx, driver.Ref{Scope: driver.ScopeRegional, ID: acl.ID})
	if err != nil {
		t.Fatalf("get web acl: %v", err)
	}

	if gotACL.ARN != acl.ARN || string(gotACL.Rules) != `[{"Name":"r1"}]` {
		t.Fatalf("web acl not preserved: %+v", gotACL)
	}

	gotIPSet, err := dst.GetIPSet(ctx, driver.Ref{Scope: driver.ScopeRegional, ID: ipset.ID})
	if err != nil {
		t.Fatalf("get ip set: %v", err)
	}

	if len(gotIPSet.Addresses) != 1 || gotIPSet.Addresses[0] != "10.0.0.0/24" {
		t.Fatalf("ip set addresses not preserved: %+v", gotIPSet)
	}

	// The mutex-guarded association side map survived the round-trip.
	protecting, err := dst.GetWebACLForResource(ctx, protectedARN)
	if err != nil || protecting == nil || protecting.ARN != acl.ARN {
		t.Fatalf("restored association = %+v, err %v", protecting, err)
	}
}
