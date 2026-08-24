package cloudtrail_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsct "github.com/aws/aws-sdk-go-v2/service/cloudtrail"
	cttypes "github.com/aws/aws-sdk-go-v2/service/cloudtrail/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// TestLookupEventsRecordsAPIActivity drives real SDK clients through the wire:
// EC2 API calls are recorded as CloudTrail management events, and LookupEvents
// returns them with EventName/EventSource/EventTime populated — the real-user
// proof that the audit trail reflects activity instead of staying empty.
func TestLookupEventsRecordsAPIActivity(t *testing.T) {
	ctx := context.Background()

	cloud := cloudemu.NewAWS()
	ts := httptest.NewServer(awsserver.New(awsserver.DriversFrom(cloud)))
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")))
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}
	cfg.BaseEndpoint = aws.String(ts.URL)

	ec2c := ec2.NewFromConfig(cfg)
	ctc := awsct.NewFromConfig(cfg)

	// Two EC2 API calls: one write, one read.
	if _, err := ec2c.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId: aws.String("ami-123"), InstanceType: ec2types.InstanceTypeT2Micro,
		MinCount: aws.Int32(1), MaxCount: aws.Int32(1),
	}); err != nil {
		t.Fatalf("RunInstances: %v", err)
	}
	if _, err := ec2c.DescribeInstances(ctx, &ec2.DescribeInstancesInput{}); err != nil {
		t.Fatalf("DescribeInstances: %v", err)
	}

	out, err := ctc.LookupEvents(ctx, &awsct.LookupEventsInput{})
	if err != nil {
		t.Fatalf("LookupEvents: %v", err)
	}
	if len(out.Events) < 2 {
		t.Fatalf("LookupEvents returned %d events, want the EC2 activity", len(out.Events))
	}

	run := findEvent(out.Events, "RunInstances")
	if run == nil {
		t.Fatal("RunInstances not recorded")
	}
	if aws.ToString(run.EventSource) != "ec2.amazonaws.com" {
		t.Errorf("RunInstances EventSource = %q, want ec2.amazonaws.com", aws.ToString(run.EventSource))
	}
	if run.EventTime == nil {
		t.Error("RunInstances EventTime is nil")
	}
	if aws.ToString(run.CloudTrailEvent) == "" {
		t.Error("RunInstances CloudTrailEvent JSON is empty")
	}

	// Attribute filter narrows to the one event.
	filtered, err := ctc.LookupEvents(ctx, &awsct.LookupEventsInput{
		LookupAttributes: []cttypes.LookupAttribute{{
			AttributeKey:   cttypes.LookupAttributeKeyEventName,
			AttributeValue: aws.String("RunInstances"),
		}},
	})
	if err != nil {
		t.Fatalf("LookupEvents(filter): %v", err)
	}
	if len(filtered.Events) != 1 || aws.ToString(filtered.Events[0].EventName) != "RunInstances" {
		t.Fatalf("EventName filter returned %d events, want exactly RunInstances", len(filtered.Events))
	}
}

func findEvent(events []cttypes.Event, name string) *cttypes.Event {
	for i := range events {
		if aws.ToString(events[i].EventName) == name {
			return &events[i]
		}
	}

	return nil
}
