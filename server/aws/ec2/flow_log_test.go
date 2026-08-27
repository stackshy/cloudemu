package ec2_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// TestDeleteFlowLogsIsIdempotent pins that DeleteFlowLogs returns HTTP 200 with
// unknown ids reported in the Unsuccessful set rather than a top-level error, so
// a destroy that re-runs over an already-gone flow log still succeeds.
func TestDeleteFlowLogsIsIdempotent(t *testing.T) {
	ctx := context.Background()
	client := newEC2(t)

	out, err := client.DeleteFlowLogs(ctx, &ec2.DeleteFlowLogsInput{
		FlowLogIds: []string{"fl-00000000000000000"},
	})
	if err != nil {
		t.Fatalf("DeleteFlowLogs(unknown) returned top-level error: %v", err)
	}
	if len(out.Unsuccessful) != 1 {
		t.Fatalf("Unsuccessful = %d, want 1", len(out.Unsuccessful))
	}
	if code := aws.ToString(out.Unsuccessful[0].Error.Code); code != "InvalidFlowLogId.NotFound" {
		t.Errorf("Unsuccessful error code = %q, want InvalidFlowLogId.NotFound", code)
	}
	if rid := aws.ToString(out.Unsuccessful[0].ResourceId); rid != "fl-00000000000000000" {
		t.Errorf("Unsuccessful resourceId = %q, want fl-00000000000000000", rid)
	}
}

// TestDeleteFlowLogsRealThenReDelete pins that deleting a real flow log succeeds
// with no Unsuccessful entries, and a second delete of the same id is reported
// as unsuccessful rather than erroring.
func TestDeleteFlowLogsRealThenReDelete(t *testing.T) {
	ctx := context.Background()
	client := newEC2(t)

	vpc, err := client.CreateVpc(ctx, &ec2.CreateVpcInput{CidrBlock: aws.String("10.0.0.0/16")})
	if err != nil {
		t.Fatalf("CreateVpc: %v", err)
	}

	fl, err := client.CreateFlowLogs(ctx, &ec2.CreateFlowLogsInput{
		ResourceIds:  []string{aws.ToString(vpc.Vpc.VpcId)},
		ResourceType: ec2types.FlowLogsResourceTypeVpc,
		TrafficType:  ec2types.TrafficTypeAll,
	})
	if err != nil {
		t.Fatalf("CreateFlowLogs: %v", err)
	}
	if len(fl.FlowLogIds) != 1 {
		t.Fatalf("CreateFlowLogs ids = %d, want 1", len(fl.FlowLogIds))
	}
	id := fl.FlowLogIds[0]

	first, err := client.DeleteFlowLogs(ctx, &ec2.DeleteFlowLogsInput{FlowLogIds: []string{id}})
	if err != nil {
		t.Fatalf("DeleteFlowLogs: %v", err)
	}
	if len(first.Unsuccessful) != 0 {
		t.Fatalf("first delete Unsuccessful = %d, want 0", len(first.Unsuccessful))
	}

	second, err := client.DeleteFlowLogs(ctx, &ec2.DeleteFlowLogsInput{FlowLogIds: []string{id}})
	if err != nil {
		t.Fatalf("second DeleteFlowLogs returned top-level error: %v", err)
	}
	if len(second.Unsuccessful) != 1 {
		t.Fatalf("second delete Unsuccessful = %d, want 1", len(second.Unsuccessful))
	}
}
