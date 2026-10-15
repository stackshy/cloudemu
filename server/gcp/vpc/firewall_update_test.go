package vpc_test

import (
	"context"
	"testing"

	gcpcompute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
)

// waitFwOp fails the test when a firewall mutation or its operation errors.
func waitFwOp(t *testing.T, ctx context.Context, op *gcpcompute.Operation, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("call: %v", err)
	}

	if err := op.Wait(ctx); err != nil {
		t.Fatalf("wait: %v", err)
	}
}

func getFw(t *testing.T, ctx context.Context, c *gcpcompute.FirewallsClient, name string) *computepb.Firewall {
	t.Helper()

	got, err := c.Get(ctx, &computepb.GetFirewallRequest{Project: testProject, Firewall: name})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	return got
}

// TestSDKFirewallPatchAndUpdateMutableFields: PATCH must persist every mutable
// field it carries (description was dropped, a permanent terraform diff), and
// PUT must replace the whole rule so omitted fields reset to their defaults.
func TestSDKFirewallPatchAndUpdateMutableFields(t *testing.T) {
	ts := newGCPNetServer(t)
	ctx := context.Background()

	client := newFwClient(t, ts.URL, ts.Client())

	op, err := client.Insert(ctx, &computepb.InsertFirewallRequest{
		Project: testProject,
		FirewallResource: &computepb.Firewall{
			Name:         ptrStr("fw-mut"),
			Description:  ptrStr("d-a"),
			Allowed:      []*computepb.Allowed{{IPProtocol: ptrStr("tcp"), Ports: []string{"22"}}},
			SourceRanges: []string{"10.0.0.0/8"},
			TargetTags:   []string{"web"},
		},
	})
	waitFwOp(t, ctx, op, err)

	op, err = client.Patch(ctx, &computepb.PatchFirewallRequest{
		Project: testProject, Firewall: "fw-mut",
		FirewallResource: &computepb.Firewall{
			Description:  ptrStr("d-b"),
			Priority:     ptrInt32(900),
			SourceRanges: []string{"192.168.0.0/16"},
			TargetTags:   []string{"db"},
			Disabled:     ptrBool(true),
			LogConfig:    &computepb.FirewallLogConfig{Enable: ptrBool(true)},
			Denied:       []*computepb.Denied{{IPProtocol: ptrStr("udp")}},
		},
	})
	waitFwOp(t, ctx, op, err)

	got := getFw(t, ctx, client, "fw-mut")
	if got.GetDescription() != "d-b" || got.GetPriority() != 900 || !got.GetDisabled() ||
		!got.GetLogConfig().GetEnable() || len(got.GetDenied()) != 1 ||
		len(got.GetSourceRanges()) != 1 || got.GetSourceRanges()[0] != "192.168.0.0/16" ||
		len(got.GetTargetTags()) != 1 || got.GetTargetTags()[0] != "db" ||
		len(got.GetAllowed()) != 1 {
		t.Fatalf("patched firewall = %+v", got)
	}

	op, err = client.Update(ctx, &computepb.UpdateFirewallRequest{
		Project: testProject, Firewall: "fw-mut",
		FirewallResource: &computepb.Firewall{
			Name:         ptrStr("fw-mut"),
			Description:  ptrStr("d-c"),
			Allowed:      []*computepb.Allowed{{IPProtocol: ptrStr("tcp"), Ports: []string{"443"}}},
			SourceRanges: []string{"10.1.0.0/16"},
		},
	})
	waitFwOp(t, ctx, op, err)

	got = getFw(t, ctx, client, "fw-mut")
	if got.GetDescription() != "d-c" || got.GetPriority() != 1000 || got.GetDisabled() ||
		got.GetLogConfig() != nil || len(got.GetDenied()) != 0 || len(got.GetTargetTags()) != 0 ||
		len(got.GetAllowed()) != 1 || got.GetAllowed()[0].GetPorts()[0] != "443" ||
		len(got.GetSourceRanges()) != 1 || got.GetSourceRanges()[0] != "10.1.0.0/16" ||
		got.GetDirection() != "INGRESS" {
		t.Fatalf("updated firewall = %+v", got)
	}

	op, err = client.Patch(ctx, &computepb.PatchFirewallRequest{
		Project: testProject, Firewall: "fw-mut",
		FirewallResource: &computepb.Firewall{Priority: ptrInt32(800)},
	})
	waitFwOp(t, ctx, op, err)

	if got = getFw(t, ctx, client, "fw-mut"); got.GetDescription() != "d-c" || got.GetPriority() != 800 {
		t.Fatalf("priority-only patch = %+v; want description kept", got)
	}
}

// TestSDKFirewallPatchKeepsInsertDescription: a patch that omits description
// keeps the one set at insert.
func TestSDKFirewallPatchKeepsInsertDescription(t *testing.T) {
	ts := newGCPNetServer(t)
	ctx := context.Background()

	client := newFwClient(t, ts.URL, ts.Client())

	op, err := client.Insert(ctx, &computepb.InsertFirewallRequest{
		Project: testProject,
		FirewallResource: &computepb.Firewall{
			Name:        ptrStr("fw-keep"),
			Description: ptrStr("kept"),
			Allowed:     []*computepb.Allowed{{IPProtocol: ptrStr("tcp")}},
		},
	})
	waitFwOp(t, ctx, op, err)

	op, err = client.Patch(ctx, &computepb.PatchFirewallRequest{
		Project: testProject, Firewall: "fw-keep",
		FirewallResource: &computepb.Firewall{Priority: ptrInt32(10)},
	})
	waitFwOp(t, ctx, op, err)

	if got := getFw(t, ctx, client, "fw-keep"); got.GetDescription() != "kept" || got.GetPriority() != 10 {
		t.Fatalf("got %+v", got)
	}
}
