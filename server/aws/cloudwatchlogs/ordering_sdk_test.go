package cloudwatchlogs_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	cwl "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
)

// TestSDKListOrderingDeterministic locks the #259 ordering fix at the wire
// level: DescribeLogGroups must return the same sequence on every call. The
// pre-fix bug was random map iteration order, so repetition is the signal.
func TestSDKListOrderingDeterministic(t *testing.T) {
	client := newLogsClient(t)
	ctx := context.Background()

	names := []string{"zeta", "alpha", "mid", "beta", "omega"}
	for _, name := range names {
		if _, err := client.CreateLogGroup(ctx, &cwl.CreateLogGroupInput{
			LogGroupName: aws.String(name),
		}); err != nil {
			t.Fatalf("CreateLogGroup(%s): %v", name, err)
		}
	}

	list := func() []string {
		out, err := client.DescribeLogGroups(ctx, &cwl.DescribeLogGroupsInput{})
		if err != nil {
			t.Fatalf("DescribeLogGroups: %v", err)
		}

		got := make([]string, 0, len(out.LogGroups))
		for i := range out.LogGroups {
			got = append(got, aws.ToString(out.LogGroups[i].LogGroupName))
		}

		return got
	}

	first := list()
	if len(first) != len(names) {
		t.Fatalf("DescribeLogGroups returned %d groups, want %d: %v", len(first), len(names), first)
	}

	for call := 2; call <= 5; call++ {
		got := list()
		if len(got) != len(first) {
			t.Fatalf("call %d: got %d groups, want %d", call, len(got), len(first))
		}

		for i := range first {
			if got[i] != first[i] {
				t.Fatalf("call %d: order diverged at index %d: got %v, first call %v", call, i, got, first)
			}
		}
	}
}
