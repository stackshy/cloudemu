package ec2_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// TestDescribeInstanceStatusReachabilityDetail pins that a running instance's
// system/instance status summaries carry the reachability status-check detail
// (previously the <details> children were omitted, so SDK consumers reading
// the reachability check saw nothing).
func TestDescribeInstanceStatusReachabilityDetail(t *testing.T) {
	ctx := context.Background()
	c := newEC2Client(t)
	id := runOneInstance(t, c)

	out, err := c.DescribeInstanceStatus(ctx, &ec2.DescribeInstanceStatusInput{
		InstanceIds: []string{id},
	})
	if err != nil {
		t.Fatalf("DescribeInstanceStatus: %v", err)
	}

	if len(out.InstanceStatuses) != 1 {
		t.Fatalf("InstanceStatuses len=%d, want 1", len(out.InstanceStatuses))
	}

	st := out.InstanceStatuses[0]
	assertReachabilityPassed(t, "systemStatus", st.SystemStatus)
	assertReachabilityPassed(t, "instanceStatus", st.InstanceStatus)
}

func assertReachabilityPassed(t *testing.T, label string, s *ec2types.InstanceStatusSummary) {
	t.Helper()

	if s == nil {
		t.Fatalf("%s summary nil", label)
	}

	if len(s.Details) == 0 {
		t.Fatalf("%s has no status-check details", label)
	}

	d := s.Details[0]
	if d.Name != ec2types.StatusNameReachability {
		t.Fatalf("%s detail name = %q, want reachability", label, d.Name)
	}

	if d.Status != ec2types.StatusTypePassed {
		t.Fatalf("%s reachability status = %q, want passed", label, d.Status)
	}
}
