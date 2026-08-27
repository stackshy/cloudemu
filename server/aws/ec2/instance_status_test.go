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

// TestMonitoringStateTransitions pins the detailed-monitoring lifecycle: a fresh
// instance reports "disabled"; MonitorInstances echoes the transitional
// "pending" and DescribeInstances then reports "enabled"; UnmonitorInstances
// echoes "disabling" and DescribeInstances returns to "disabled".
func TestMonitoringStateTransitions(t *testing.T) {
	ctx := context.Background()
	c := newEC2Client(t)
	id := runOneInstance(t, c)

	if got := describeMonitoringState(t, c, id); got != ec2types.MonitoringStateDisabled {
		t.Fatalf("initial monitoring state = %q, want disabled", got)
	}

	mon, err := c.MonitorInstances(ctx, &ec2.MonitorInstancesInput{InstanceIds: []string{id}})
	if err != nil {
		t.Fatalf("MonitorInstances: %v", err)
	}

	if mon.InstanceMonitorings[0].Monitoring.State != ec2types.MonitoringStatePending {
		t.Fatalf("MonitorInstances echo = %q, want pending", mon.InstanceMonitorings[0].Monitoring.State)
	}

	if got := describeMonitoringState(t, c, id); got != ec2types.MonitoringStateEnabled {
		t.Fatalf("monitoring state after MonitorInstances = %q, want enabled", got)
	}

	unmon, err := c.UnmonitorInstances(ctx, &ec2.UnmonitorInstancesInput{InstanceIds: []string{id}})
	if err != nil {
		t.Fatalf("UnmonitorInstances: %v", err)
	}

	if unmon.InstanceMonitorings[0].Monitoring.State != ec2types.MonitoringStateDisabling {
		t.Fatalf("UnmonitorInstances echo = %q, want disabling", unmon.InstanceMonitorings[0].Monitoring.State)
	}

	if got := describeMonitoringState(t, c, id); got != ec2types.MonitoringStateDisabled {
		t.Fatalf("monitoring state after UnmonitorInstances = %q, want disabled", got)
	}
}

func describeMonitoringState(t *testing.T, c *ec2.Client, id string) ec2types.MonitoringState {
	t.Helper()

	out, err := c.DescribeInstances(context.Background(), &ec2.DescribeInstancesInput{
		InstanceIds: []string{id},
	})
	if err != nil {
		t.Fatalf("DescribeInstances: %v", err)
	}

	m := out.Reservations[0].Instances[0].Monitoring
	if m == nil {
		t.Fatalf("instance %q has no monitoring block", id)
	}

	return m.State
}
