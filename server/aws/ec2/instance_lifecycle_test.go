package ec2_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// TestLifecyclePreviousStateDerived pins that Stop/Start report the real prior
// state in previousState rather than a hardcoded value: stopping a running
// instance reports previousState=running, and starting the now-stopped instance
// reports previousState=stopped.
func TestLifecyclePreviousStateDerived(t *testing.T) {
	ctx := context.Background()
	c := newEC2Client(t)
	id := runOneInstance(t, c)

	stop, err := c.StopInstances(ctx, &ec2.StopInstancesInput{InstanceIds: []string{id}})
	if err != nil {
		t.Fatalf("StopInstances: %v", err)
	}

	if len(stop.StoppingInstances) != 1 {
		t.Fatalf("StoppingInstances len=%d", len(stop.StoppingInstances))
	}

	if got := stop.StoppingInstances[0].PreviousState.Name; got != ec2types.InstanceStateNameRunning {
		t.Fatalf("stop previousState = %q, want running", got)
	}

	if got := stop.StoppingInstances[0].CurrentState.Name; got != ec2types.InstanceStateNameStopping {
		t.Fatalf("stop currentState = %q, want stopping", got)
	}

	// Stopping the already-stopped instance is idempotent; the derived
	// previousState must be the actual prior state (stopped), not the old
	// hardcoded "running".
	stop2, err := c.StopInstances(ctx, &ec2.StopInstancesInput{InstanceIds: []string{id}})
	if err != nil {
		t.Fatalf("StopInstances (idempotent): %v", err)
	}

	if got := stop2.StoppingInstances[0].PreviousState.Name; got != ec2types.InstanceStateNameStopped {
		t.Fatalf("idempotent stop previousState = %q, want stopped", got)
	}

	start, err := c.StartInstances(ctx, &ec2.StartInstancesInput{InstanceIds: []string{id}})
	if err != nil {
		t.Fatalf("StartInstances: %v", err)
	}

	if got := start.StartingInstances[0].PreviousState.Name; got != ec2types.InstanceStateNameStopped {
		t.Fatalf("start previousState = %q, want stopped", got)
	}
}

// TestTerminatePreviousStateDerived pins that TerminateInstances reports the
// real prior state (stopped, here) in previousState rather than a hardcoded
// "running".
func TestTerminatePreviousStateDerived(t *testing.T) {
	ctx := context.Background()
	c := newEC2Client(t)
	id := runOneInstance(t, c)

	if _, err := c.StopInstances(ctx, &ec2.StopInstancesInput{InstanceIds: []string{id}}); err != nil {
		t.Fatalf("StopInstances: %v", err)
	}

	term, err := c.TerminateInstances(ctx, &ec2.TerminateInstancesInput{InstanceIds: []string{id}})
	if err != nil {
		t.Fatalf("TerminateInstances: %v", err)
	}

	if got := term.TerminatingInstances[0].PreviousState.Name; got != ec2types.InstanceStateNameStopped {
		t.Fatalf("terminate previousState = %q, want stopped", got)
	}
}
