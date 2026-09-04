package costexplorer_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	ce "github.com/aws/aws-sdk-go-v2/service/costexplorer"
	cetypes "github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	cloudemu "github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// newStateAwareClients stands up one in-process AWS server serving both EC2 and
// Cost Explorer over a shared cloud, so a lifecycle change made through the real
// EC2 client is reflected in the Cost Explorer total. It launches two running
// t3.micro instances up front.
func newStateAwareClients(t *testing.T) (*ce.Client, *ec2.Client) {
	t.Helper()

	cloud := cloudemu.NewAWS()

	srv := awsserver.New(awsserver.Drivers{
		EC2:          cloud.EC2,
		CostExplorer: cloud.ResourceDiscovery,
	})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	ceClient := ce.NewFromConfig(cfg, func(o *ce.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	ec2Client := ec2.NewFromConfig(cfg, func(o *ec2.Options) { o.BaseEndpoint = aws.String(ts.URL) })

	if _, err := ec2Client.RunInstances(context.Background(), &ec2.RunInstancesInput{
		ImageId:      aws.String("ami-1"),
		InstanceType: ec2types.InstanceTypeT3Micro,
		MinCount:     aws.Int32(2),
		MaxCount:     aws.Int32(2),
	}); err != nil {
		t.Fatalf("run instances: %v", err)
	}

	return ceClient, ec2Client
}

// monthlyTotal queries the current month-to-date total UnblendedCost through the
// real Cost Explorer client.
func monthlyTotal(t *testing.T, c *ce.Client) float64 {
	t.Helper()

	out, err := c.GetCostAndUsage(context.Background(), &ce.GetCostAndUsageInput{
		Granularity: cetypes.GranularityMonthly,
		Metrics:     []string{"UnblendedCost"},
		TimePeriod:  &cetypes.DateInterval{Start: aws.String("2024-01-01"), End: aws.String("2024-02-01")},
	})
	if err != nil {
		t.Fatalf("GetCostAndUsage: %v", err)
	}

	if len(out.ResultsByTime) != 1 {
		t.Fatalf("ResultsByTime = %d, want 1", len(out.ResultsByTime))
	}

	return amount(t, out.ResultsByTime[0].Total["UnblendedCost"].Amount)
}

// instanceIDs returns the ids of every instance currently known to EC2.
func instanceIDs(t *testing.T, c *ec2.Client) []string {
	t.Helper()

	out, err := c.DescribeInstances(context.Background(), &ec2.DescribeInstancesInput{})
	if err != nil {
		t.Fatalf("DescribeInstances: %v", err)
	}

	var ids []string

	for i := range out.Reservations {
		for j := range out.Reservations[i].Instances {
			ids = append(ids, aws.ToString(out.Reservations[i].Instances[j].InstanceId))
		}
	}

	return ids
}

// TestSDKCostDropsOnTerminate proves a terminated instance is no longer billed:
// the month-to-date total falls after each TerminateInstances and reaches zero
// once both instances are gone (real AWS bills no compute for a terminated
// instance).
func TestSDKCostDropsOnTerminate(t *testing.T) {
	ctx := context.Background()
	ceClient, ec2Client := newStateAwareClients(t)

	before := monthlyTotal(t, ceClient)
	if before <= 0 {
		t.Fatalf("running estate total = %v, want > 0", before)
	}

	ids := instanceIDs(t, ec2Client)
	if len(ids) != 2 {
		t.Fatalf("instance count = %d, want 2", len(ids))
	}

	if _, err := ec2Client.TerminateInstances(ctx, &ec2.TerminateInstancesInput{InstanceIds: ids[:1]}); err != nil {
		t.Fatalf("terminate one: %v", err)
	}

	afterOne := monthlyTotal(t, ceClient)
	if afterOne >= before {
		t.Fatalf("total after terminating one = %v, want < %v", afterOne, before)
	}

	if _, err := ec2Client.TerminateInstances(ctx, &ec2.TerminateInstancesInput{InstanceIds: ids[1:]}); err != nil {
		t.Fatalf("terminate remaining: %v", err)
	}

	afterAll := monthlyTotal(t, ceClient)
	if afterAll != 0 {
		t.Fatalf("total after terminating all = %v, want 0 (terminated instances bill nothing)", afterAll)
	}
}

// TestSDKCostDropsOnStop proves a stopped instance is not billed for compute:
// stopping both instances drops the total below the running estate, but the
// root EBS volumes each instance launches with keep billing (a real stopped
// instance still pays for its EBS storage), so the total stays above zero.
func TestSDKCostDropsOnStop(t *testing.T) {
	ctx := context.Background()
	ceClient, ec2Client := newStateAwareClients(t)

	before := monthlyTotal(t, ceClient)
	if before <= 0 {
		t.Fatalf("running estate total = %v, want > 0", before)
	}

	ids := instanceIDs(t, ec2Client)

	if _, err := ec2Client.StopInstances(ctx, &ec2.StopInstancesInput{InstanceIds: ids}); err != nil {
		t.Fatalf("stop instances: %v", err)
	}

	after := monthlyTotal(t, ceClient)
	if after >= before {
		t.Fatalf("total after stopping all = %v, want < %v (compute no longer billed)", after, before)
	}

	if after <= 0 {
		t.Fatalf("total after stopping all = %v, want > 0 (root EBS volumes still billed)", after)
	}
}
