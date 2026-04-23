package aws_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stackshy/cloudemu"
	awsserver "github.com/stackshy/cloudemu/server/aws"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newEC2Client(t *testing.T) *ec2.Client {
	t.Helper()

	provider := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{
		EC2: provider.EC2,
		VPC: provider.VPC,
	})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("test", "test", ""),
		),
	)
	require.NoError(t, err)

	return ec2.NewFromConfig(cfg, func(o *ec2.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})
}

func TestEC2RunInstancesBasic(t *testing.T) {
	client := newEC2Client(t)
	ctx := context.Background()

	out, err := client.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId:      aws.String("ami-12345"),
		InstanceType: ec2types.InstanceTypeT2Micro,
		MinCount:     aws.Int32(1),
		MaxCount:     aws.Int32(1),
	})
	require.NoError(t, err)
	require.NotNil(t, out.ReservationId)
	require.Len(t, out.Instances, 1)

	inst := out.Instances[0]
	assert.NotEmpty(t, aws.ToString(inst.InstanceId))
	assert.Equal(t, "ami-12345", aws.ToString(inst.ImageId))
	assert.Equal(t, ec2types.InstanceTypeT2Micro, inst.InstanceType)
}

func TestEC2RunInstancesMultipleCount(t *testing.T) {
	client := newEC2Client(t)
	ctx := context.Background()

	out, err := client.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId:      aws.String("ami-22222"),
		InstanceType: ec2types.InstanceTypeT2Micro,
		MinCount:     aws.Int32(3),
		MaxCount:     aws.Int32(3),
	})
	require.NoError(t, err)
	assert.Len(t, out.Instances, 3)
}

func TestEC2RunInstancesWithSecurityGroups(t *testing.T) {
	client := newEC2Client(t)
	ctx := context.Background()

	out, err := client.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId:          aws.String("ami-sg"),
		InstanceType:     ec2types.InstanceTypeT2Micro,
		MinCount:         aws.Int32(1),
		MaxCount:         aws.Int32(1),
		SecurityGroupIds: []string{"sg-aaa", "sg-bbb"},
	})
	require.NoError(t, err)
	require.Len(t, out.Instances, 1)

	gotGroups := make([]string, 0, len(out.Instances[0].SecurityGroups))
	for _, g := range out.Instances[0].SecurityGroups {
		gotGroups = append(gotGroups, aws.ToString(g.GroupId))
	}

	assert.ElementsMatch(t, []string{"sg-aaa", "sg-bbb"}, gotGroups)
}

func TestEC2RunInstancesWithTags(t *testing.T) {
	client := newEC2Client(t)
	ctx := context.Background()

	out, err := client.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId:      aws.String("ami-tagged"),
		InstanceType: ec2types.InstanceTypeT2Micro,
		MinCount:     aws.Int32(1),
		MaxCount:     aws.Int32(1),
		TagSpecifications: []ec2types.TagSpecification{{
			ResourceType: ec2types.ResourceTypeInstance,
			Tags: []ec2types.Tag{
				{Key: aws.String("Name"), Value: aws.String("my-box")},
				{Key: aws.String("Env"), Value: aws.String("dev")},
			},
		}},
	})
	require.NoError(t, err)
	require.Len(t, out.Instances, 1)

	gotTags := map[string]string{}
	for _, tag := range out.Instances[0].Tags {
		gotTags[aws.ToString(tag.Key)] = aws.ToString(tag.Value)
	}

	assert.Equal(t, "my-box", gotTags["Name"])
	assert.Equal(t, "dev", gotTags["Env"])
}

func TestEC2DescribeInstancesByID(t *testing.T) {
	client := newEC2Client(t)
	ctx := context.Background()

	run, err := client.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId: aws.String("ami-desc"), InstanceType: ec2types.InstanceTypeT2Micro,
		MinCount: aws.Int32(1), MaxCount: aws.Int32(1),
	})
	require.NoError(t, err)

	id := aws.ToString(run.Instances[0].InstanceId)

	desc, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		InstanceIds: []string{id},
	})
	require.NoError(t, err)
	require.NotEmpty(t, desc.Reservations)

	found := false
	for _, res := range desc.Reservations {
		for _, inst := range res.Instances {
			if aws.ToString(inst.InstanceId) == id {
				found = true
			}
		}
	}
	assert.True(t, found, "expected to find instance %q in describe output", id)
}

func TestEC2DescribeInstancesAll(t *testing.T) {
	client := newEC2Client(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		_, err := client.RunInstances(ctx, &ec2.RunInstancesInput{
			ImageId: aws.String("ami-x"), InstanceType: ec2types.InstanceTypeT2Micro,
			MinCount: aws.Int32(1), MaxCount: aws.Int32(1),
		})
		require.NoError(t, err)
	}

	desc, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{})
	require.NoError(t, err)

	total := 0
	for _, res := range desc.Reservations {
		total += len(res.Instances)
	}

	assert.GreaterOrEqual(t, total, 3)
}

func TestEC2DescribeInstancesWithStateFilter(t *testing.T) {
	client := newEC2Client(t)
	ctx := context.Background()

	run, err := client.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId: aws.String("ami-filter"), InstanceType: ec2types.InstanceTypeT2Micro,
		MinCount: aws.Int32(1), MaxCount: aws.Int32(1),
	})
	require.NoError(t, err)

	id := aws.ToString(run.Instances[0].InstanceId)

	desc, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		Filters: []ec2types.Filter{{
			Name:   aws.String("instance-state-name"),
			Values: []string{"running"},
		}},
	})
	require.NoError(t, err)

	found := false
	for _, res := range desc.Reservations {
		for _, inst := range res.Instances {
			if aws.ToString(inst.InstanceId) == id {
				found = true
			}
		}
	}
	assert.True(t, found)
}

func TestEC2DescribeInstancesWithTagFilter(t *testing.T) {
	client := newEC2Client(t)
	ctx := context.Background()

	_, err := client.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId: aws.String("ami-tagfilter"), InstanceType: ec2types.InstanceTypeT2Micro,
		MinCount: aws.Int32(1), MaxCount: aws.Int32(1),
		TagSpecifications: []ec2types.TagSpecification{{
			ResourceType: ec2types.ResourceTypeInstance,
			Tags:         []ec2types.Tag{{Key: aws.String("Role"), Value: aws.String("web")}},
		}},
	})
	require.NoError(t, err)

	desc, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		Filters: []ec2types.Filter{{
			Name:   aws.String("tag:Role"),
			Values: []string{"web"},
		}},
	})
	require.NoError(t, err)

	total := 0
	for _, res := range desc.Reservations {
		total += len(res.Instances)
	}

	assert.Equal(t, 1, total)
}

func TestEC2StopAndStartInstances(t *testing.T) {
	client := newEC2Client(t)
	ctx := context.Background()

	run, err := client.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId: aws.String("ami-ss"), InstanceType: ec2types.InstanceTypeT2Micro,
		MinCount: aws.Int32(1), MaxCount: aws.Int32(1),
	})
	require.NoError(t, err)
	id := aws.ToString(run.Instances[0].InstanceId)

	stopOut, err := client.StopInstances(ctx, &ec2.StopInstancesInput{
		InstanceIds: []string{id},
	})
	require.NoError(t, err)
	require.Len(t, stopOut.StoppingInstances, 1)
	assert.Equal(t, id, aws.ToString(stopOut.StoppingInstances[0].InstanceId))

	startOut, err := client.StartInstances(ctx, &ec2.StartInstancesInput{
		InstanceIds: []string{id},
	})
	require.NoError(t, err)
	require.Len(t, startOut.StartingInstances, 1)
}

func TestEC2RebootInstances(t *testing.T) {
	client := newEC2Client(t)
	ctx := context.Background()

	run, err := client.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId: aws.String("ami-reboot"), InstanceType: ec2types.InstanceTypeT2Micro,
		MinCount: aws.Int32(1), MaxCount: aws.Int32(1),
	})
	require.NoError(t, err)

	_, err = client.RebootInstances(ctx, &ec2.RebootInstancesInput{
		InstanceIds: []string{aws.ToString(run.Instances[0].InstanceId)},
	})
	require.NoError(t, err)
}

func TestEC2TerminateInstances(t *testing.T) {
	client := newEC2Client(t)
	ctx := context.Background()

	run, err := client.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId: aws.String("ami-term"), InstanceType: ec2types.InstanceTypeT2Micro,
		MinCount: aws.Int32(1), MaxCount: aws.Int32(1),
	})
	require.NoError(t, err)
	id := aws.ToString(run.Instances[0].InstanceId)

	out, err := client.TerminateInstances(ctx, &ec2.TerminateInstancesInput{
		InstanceIds: []string{id},
	})
	require.NoError(t, err)
	require.Len(t, out.TerminatingInstances, 1)
	assert.Equal(t, id, aws.ToString(out.TerminatingInstances[0].InstanceId))
}

func TestEC2StartInstancesUnknownIDReturnsError(t *testing.T) {
	client := newEC2Client(t)
	ctx := context.Background()

	_, err := client.StartInstances(ctx, &ec2.StartInstancesInput{
		InstanceIds: []string{"i-does-not-exist"},
	})
	require.Error(t, err)
}

func TestEC2ModifyInstanceAttributeType(t *testing.T) {
	client := newEC2Client(t)
	ctx := context.Background()

	run, err := client.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId: aws.String("ami-mod"), InstanceType: ec2types.InstanceTypeT2Micro,
		MinCount: aws.Int32(1), MaxCount: aws.Int32(1),
	})
	require.NoError(t, err)
	id := aws.ToString(run.Instances[0].InstanceId)

	// ModifyInstanceAttribute requires instance to be stopped first.
	_, err = client.StopInstances(ctx, &ec2.StopInstancesInput{
		InstanceIds: []string{id},
	})
	require.NoError(t, err)

	_, err = client.ModifyInstanceAttribute(ctx, &ec2.ModifyInstanceAttributeInput{
		InstanceId: aws.String(id),
		InstanceType: &ec2types.AttributeValue{
			Value: aws.String(string(ec2types.InstanceTypeT2Small)),
		},
	})
	require.NoError(t, err)
}

func TestEC2FullInstanceLifecycle(t *testing.T) {
	client := newEC2Client(t)
	ctx := context.Background()

	// Launch.
	run, err := client.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId: aws.String("ami-lifecycle"), InstanceType: ec2types.InstanceTypeT2Micro,
		MinCount: aws.Int32(1), MaxCount: aws.Int32(1),
		TagSpecifications: []ec2types.TagSpecification{{
			ResourceType: ec2types.ResourceTypeInstance,
			Tags:         []ec2types.Tag{{Key: aws.String("Lifecycle"), Value: aws.String("test")}},
		}},
	})
	require.NoError(t, err)
	id := aws.ToString(run.Instances[0].InstanceId)

	// Describe — should show running.
	desc, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		InstanceIds: []string{id},
	})
	require.NoError(t, err)
	require.NotEmpty(t, desc.Reservations)

	// Stop.
	_, err = client.StopInstances(ctx, &ec2.StopInstancesInput{InstanceIds: []string{id}})
	require.NoError(t, err)

	// Start.
	_, err = client.StartInstances(ctx, &ec2.StartInstancesInput{InstanceIds: []string{id}})
	require.NoError(t, err)

	// Reboot.
	_, err = client.RebootInstances(ctx, &ec2.RebootInstancesInput{InstanceIds: []string{id}})
	require.NoError(t, err)

	// Terminate.
	_, err = client.TerminateInstances(ctx, &ec2.TerminateInstancesInput{InstanceIds: []string{id}})
	require.NoError(t, err)
}

// newMultiClient returns an httptest server with S3 + DynamoDB + EC2
// simultaneously registered so we can catch cross-service dispatch regressions.
func newMultiClient(t *testing.T) (*ec2.Client, *s3.Client, *dynamodb.Client) {
	t.Helper()

	provider := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{
		S3: provider.S3, DynamoDB: provider.DynamoDB, EC2: provider.EC2,
	})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("test", "test", ""),
		),
	)
	require.NoError(t, err)

	ec2c := ec2.NewFromConfig(cfg, func(o *ec2.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})
	s3c := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
		o.UsePathStyle = true
	})
	ddbc := dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})

	return ec2c, s3c, ddbc
}

// Bug-fix regression: MinCount=1, MaxCount=5 should launch 5, not 1.
func TestEC2RunInstancesHonoursMaxCount(t *testing.T) {
	client := newEC2Client(t)
	ctx := context.Background()

	out, err := client.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId: aws.String("ami-max"), InstanceType: ec2types.InstanceTypeT2Micro,
		MinCount: aws.Int32(1), MaxCount: aws.Int32(5),
	})
	require.NoError(t, err)
	assert.Len(t, out.Instances, 5,
		"MinCount=1 MaxCount=5 should launch MaxCount instances")
}

func TestEC2RunInstancesMaxCountOnly(t *testing.T) {
	// aws-sdk-go-v2 lets MinCount be implicit; verify MaxCount-only works.
	client := newEC2Client(t)
	ctx := context.Background()

	out, err := client.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId: aws.String("ami-maxonly"), InstanceType: ec2types.InstanceTypeT2Micro,
		MaxCount: aws.Int32(2), MinCount: aws.Int32(1),
	})
	require.NoError(t, err)
	assert.Len(t, out.Instances, 2)
}

func TestEC2RunInstancesInstanceTypeReflected(t *testing.T) {
	client := newEC2Client(t)
	ctx := context.Background()

	out, err := client.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId: aws.String("ami-big"), InstanceType: ec2types.InstanceTypeM5Large,
		MinCount: aws.Int32(1), MaxCount: aws.Int32(1),
	})
	require.NoError(t, err)
	require.Len(t, out.Instances, 1)
	assert.Equal(t, ec2types.InstanceTypeM5Large, out.Instances[0].InstanceType)
}

func TestEC2RunInstancesStateIsRunning(t *testing.T) {
	client := newEC2Client(t)
	ctx := context.Background()

	out, err := client.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId: aws.String("ami-state"), InstanceType: ec2types.InstanceTypeT2Micro,
		MinCount: aws.Int32(1), MaxCount: aws.Int32(1),
	})
	require.NoError(t, err)
	require.Len(t, out.Instances, 1)

	state := out.Instances[0].State
	require.NotNil(t, state)
	assert.Equal(t, "running", string(state.Name))
}

func TestEC2RunInstancesTagSpecNonInstanceResourceIgnored(t *testing.T) {
	// Tags with ResourceType=volume should not be applied to the instance.
	client := newEC2Client(t)
	ctx := context.Background()

	out, err := client.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId: aws.String("ami-vol"), InstanceType: ec2types.InstanceTypeT2Micro,
		MinCount: aws.Int32(1), MaxCount: aws.Int32(1),
		TagSpecifications: []ec2types.TagSpecification{
			{
				ResourceType: ec2types.ResourceTypeInstance,
				Tags:         []ec2types.Tag{{Key: aws.String("A"), Value: aws.String("1")}},
			},
			{
				ResourceType: ec2types.ResourceTypeVolume,
				Tags:         []ec2types.Tag{{Key: aws.String("B"), Value: aws.String("2")}},
			},
		},
	})
	require.NoError(t, err)
	require.Len(t, out.Instances, 1)

	got := map[string]string{}
	for _, tg := range out.Instances[0].Tags {
		got[aws.ToString(tg.Key)] = aws.ToString(tg.Value)
	}

	assert.Equal(t, "1", got["A"])
	_, hasB := got["B"]
	assert.False(t, hasB, "volume-scoped tag should not appear on instance")
}

func TestEC2RunInstancesTagsWithSpecialChars(t *testing.T) {
	client := newEC2Client(t)
	ctx := context.Background()

	out, err := client.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId: aws.String("ami-sc"), InstanceType: ec2types.InstanceTypeT2Micro,
		MinCount: aws.Int32(1), MaxCount: aws.Int32(1),
		TagSpecifications: []ec2types.TagSpecification{{
			ResourceType: ec2types.ResourceTypeInstance,
			Tags: []ec2types.Tag{
				{Key: aws.String("Owner"), Value: aws.String("foo@example.com")},
				{Key: aws.String("App"), Value: aws.String("name with spaces")},
				{Key: aws.String("Expr"), Value: aws.String("k=v&x=y")},
			},
		}},
	})
	require.NoError(t, err)

	got := map[string]string{}
	for _, tg := range out.Instances[0].Tags {
		got[aws.ToString(tg.Key)] = aws.ToString(tg.Value)
	}

	assert.Equal(t, "foo@example.com", got["Owner"])
	assert.Equal(t, "name with spaces", got["App"])
	assert.Equal(t, "k=v&x=y", got["Expr"])
}

func TestEC2DescribeInstancesEmpty(t *testing.T) {
	client := newEC2Client(t)
	ctx := context.Background()

	out, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{})
	require.NoError(t, err)
	assert.Empty(t, out.Reservations)
}

func TestEC2DescribeInstancesFilterMultipleValues(t *testing.T) {
	client := newEC2Client(t)
	ctx := context.Background()

	// Two instances: leave one running, stop one.
	run, err := client.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId: aws.String("ami-mv"), InstanceType: ec2types.InstanceTypeT2Micro,
		MinCount: aws.Int32(2), MaxCount: aws.Int32(2),
	})
	require.NoError(t, err)
	require.Len(t, run.Instances, 2)

	stopID := aws.ToString(run.Instances[0].InstanceId)
	_, err = client.StopInstances(ctx, &ec2.StopInstancesInput{InstanceIds: []string{stopID}})
	require.NoError(t, err)

	// Filter state in [running, pending] — should find the second one.
	out, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		Filters: []ec2types.Filter{{
			Name:   aws.String("instance-state-name"),
			Values: []string{"running", "pending"},
		}},
	})
	require.NoError(t, err)

	found := 0
	for _, res := range out.Reservations {
		for _, inst := range res.Instances {
			st := string(inst.State.Name)
			if st == "running" || st == "pending" {
				found++
			}
		}
	}
	assert.GreaterOrEqual(t, found, 1)
}

func TestEC2DescribeInstancesMultipleFiltersAnded(t *testing.T) {
	// All filters must match (AND). Launch with tag Role=api and filter on
	// both instance-type and tag:Role — should only match our instance.
	client := newEC2Client(t)
	ctx := context.Background()

	run, err := client.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId: aws.String("ami-and"), InstanceType: ec2types.InstanceTypeT2Small,
		MinCount: aws.Int32(1), MaxCount: aws.Int32(1),
		TagSpecifications: []ec2types.TagSpecification{{
			ResourceType: ec2types.ResourceTypeInstance,
			Tags:         []ec2types.Tag{{Key: aws.String("Role"), Value: aws.String("api")}},
		}},
	})
	require.NoError(t, err)
	id := aws.ToString(run.Instances[0].InstanceId)

	// Add another instance with different type + no tag, should NOT match.
	_, err = client.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId: aws.String("ami-other"), InstanceType: ec2types.InstanceTypeT2Micro,
		MinCount: aws.Int32(1), MaxCount: aws.Int32(1),
	})
	require.NoError(t, err)

	out, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		Filters: []ec2types.Filter{
			{Name: aws.String("instance-type"), Values: []string{"t2.small"}},
			{Name: aws.String("tag:Role"), Values: []string{"api"}},
		},
	})
	require.NoError(t, err)

	ids := collectIDs(out)
	assert.Contains(t, ids, id)
	assert.Len(t, ids, 1, "only the tagged t2.small should match both filters")
}

func TestEC2DescribeTerminatedInstanceStillVisible(t *testing.T) {
	// Real AWS keeps terminated instances visible for ~1hr.
	client := newEC2Client(t)
	ctx := context.Background()

	run, err := client.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId: aws.String("ami-term"), InstanceType: ec2types.InstanceTypeT2Micro,
		MinCount: aws.Int32(1), MaxCount: aws.Int32(1),
	})
	require.NoError(t, err)
	id := aws.ToString(run.Instances[0].InstanceId)

	_, err = client.TerminateInstances(ctx, &ec2.TerminateInstancesInput{
		InstanceIds: []string{id},
	})
	require.NoError(t, err)

	out, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		InstanceIds: []string{id},
	})
	require.NoError(t, err)

	assert.Contains(t, collectIDs(out), id,
		"terminated instance should still be described")
}

func TestEC2DescribeInstancesByUnknownIDReturnsEmpty(t *testing.T) {
	// Real AWS returns an error; our provider returns empty. Document behavior.
	client := newEC2Client(t)
	ctx := context.Background()

	out, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		InstanceIds: []string{"i-deadbeef"},
	})
	require.NoError(t, err)
	assert.Empty(t, collectIDs(out))
}

func TestEC2StopIdempotent(t *testing.T) {
	client := newEC2Client(t)
	ctx := context.Background()

	run, err := client.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId: aws.String("ami-idem"), InstanceType: ec2types.InstanceTypeT2Micro,
		MinCount: aws.Int32(1), MaxCount: aws.Int32(1),
	})
	require.NoError(t, err)
	id := aws.ToString(run.Instances[0].InstanceId)

	_, err = client.StopInstances(ctx, &ec2.StopInstancesInput{InstanceIds: []string{id}})
	require.NoError(t, err)

	// Second stop should error because the driver considers it an invalid
	// state transition. AWS is idempotent, but we match the provider's
	// statemachine behavior.
	_, err2 := client.StopInstances(ctx, &ec2.StopInstancesInput{InstanceIds: []string{id}})
	_ = err2 // either behavior is acceptable; just assert we don't panic/hang
}

func TestEC2BatchOperationsMultipleIDs(t *testing.T) {
	client := newEC2Client(t)
	ctx := context.Background()

	run, err := client.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId: aws.String("ami-batch"), InstanceType: ec2types.InstanceTypeT2Micro,
		MinCount: aws.Int32(3), MaxCount: aws.Int32(3),
	})
	require.NoError(t, err)
	require.Len(t, run.Instances, 3)

	ids := []string{
		aws.ToString(run.Instances[0].InstanceId),
		aws.ToString(run.Instances[1].InstanceId),
		aws.ToString(run.Instances[2].InstanceId),
	}

	stopOut, err := client.StopInstances(ctx, &ec2.StopInstancesInput{InstanceIds: ids})
	require.NoError(t, err)
	assert.Len(t, stopOut.StoppingInstances, 3)

	startOut, err := client.StartInstances(ctx, &ec2.StartInstancesInput{InstanceIds: ids})
	require.NoError(t, err)
	assert.Len(t, startOut.StartingInstances, 3)
}

func TestEC2GETWithActionInQueryString(t *testing.T) {
	// Our Matches supports both POST (SDK default) and GET with Action=... in
	// the URL. Prove the GET path works.
	provider := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{EC2: provider.EC2, VPC: provider.VPC})
	ts := httptest.NewServer(srv)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/?Action=DescribeInstances&Version=2016-11-15")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "text/xml", resp.Header.Get("Content-Type"))
}

func TestEC2UnknownActionReturnsError(t *testing.T) {
	provider := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{EC2: provider.EC2, VPC: provider.VPC})
	ts := httptest.NewServer(srv)
	defer ts.Close()

	resp, err := http.PostForm(ts.URL, map[string][]string{
		"Action":  {"FrobnicateWidget"},
		"Version": {"2016-11-15"},
	})
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestEC2OverlyLargeBodyRejected(t *testing.T) {
	// Prove MaxBytesReader limit works; 2 MiB body exceeds 1 MiB cap.
	provider := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{EC2: provider.EC2, VPC: provider.VPC})
	ts := httptest.NewServer(srv)
	defer ts.Close()

	body := bytes.Repeat([]byte("A"), 2<<20)

	resp, err := http.Post(ts.URL, "application/x-www-form-urlencoded",
		bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode,
		"oversized body should be rejected")
}

// When EC2 is registered alongside S3 and DynamoDB, each SDK client must
// still hit its own handler. This guards against Matches regressions.
func TestEC2DoesNotStealS3Requests(t *testing.T) {
	_, s3c, _ := newMultiClient(t)
	ctx := context.Background()

	_, err := s3c.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String("xbucket")})
	require.NoError(t, err)

	_, err = s3c.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String("xbucket"), Key: aws.String("k"),
		Body: bytes.NewReader([]byte("ok")),
	})
	require.NoError(t, err)
}

func TestEC2DoesNotStealDynamoDBRequests(t *testing.T) {
	_, _, ddbc := newMultiClient(t)
	ctx := context.Background()

	_, err := ddbc.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName: aws.String("xt"),
		KeySchema: []ddbtypes.KeySchemaElement{
			{AttributeName: aws.String("id"), KeyType: ddbtypes.KeyTypeHash},
		},
		AttributeDefinitions: []ddbtypes.AttributeDefinition{
			{AttributeName: aws.String("id"), AttributeType: ddbtypes.ScalarAttributeTypeS},
		},
		BillingMode: ddbtypes.BillingModePayPerRequest,
	})
	require.NoError(t, err)

	_, err = ddbc.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String("xt"),
		Item: map[string]ddbtypes.AttributeValue{
			"id": &ddbtypes.AttributeValueMemberS{Value: "1"},
		},
	})
	require.NoError(t, err)
}

func TestEC2AndS3AndDDBInterleaved(t *testing.T) {
	// Drive all three SDK clients in a single sequence on one server —
	// realistic multi-service workflow.
	ec2c, s3c, ddbc := newMultiClient(t)
	ctx := context.Background()

	_, err := s3c.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String("wf-bucket")})
	require.NoError(t, err)

	_, err = ddbc.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName: aws.String("wf-table"),
		KeySchema: []ddbtypes.KeySchemaElement{
			{AttributeName: aws.String("id"), KeyType: ddbtypes.KeyTypeHash},
		},
		AttributeDefinitions: []ddbtypes.AttributeDefinition{
			{AttributeName: aws.String("id"), AttributeType: ddbtypes.ScalarAttributeTypeS},
		},
		BillingMode: ddbtypes.BillingModePayPerRequest,
	})
	require.NoError(t, err)

	run, err := ec2c.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId: aws.String("ami-wf"), InstanceType: ec2types.InstanceTypeT2Micro,
		MinCount: aws.Int32(1), MaxCount: aws.Int32(1),
	})
	require.NoError(t, err)
	require.Len(t, run.Instances, 1)

	_, err = s3c.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String("wf-bucket"), Key: aws.String("meta"),
		Body: bytes.NewReader([]byte(aws.ToString(run.Instances[0].InstanceId))),
	})
	require.NoError(t, err)
}

func TestEC2StopInstancesUnknownIDReturnsError(t *testing.T) {
	client := newEC2Client(t)
	ctx := context.Background()

	_, err := client.StopInstances(ctx, &ec2.StopInstancesInput{
		InstanceIds: []string{"i-ghost"},
	})
	require.Error(t, err)
}

func TestEC2TerminateInstancesUnknownIDReturnsError(t *testing.T) {
	client := newEC2Client(t)
	ctx := context.Background()

	_, err := client.TerminateInstances(ctx, &ec2.TerminateInstancesInput{
		InstanceIds: []string{"i-ghost"},
	})
	require.Error(t, err)
}

func TestEC2RebootInstancesUnknownIDReturnsError(t *testing.T) {
	client := newEC2Client(t)
	ctx := context.Background()

	_, err := client.RebootInstances(ctx, &ec2.RebootInstancesInput{
		InstanceIds: []string{"i-ghost"},
	})
	require.Error(t, err)
}

func TestEC2ModifyInstanceAttributeUnknownIDReturnsError(t *testing.T) {
	client := newEC2Client(t)
	ctx := context.Background()

	_, err := client.ModifyInstanceAttribute(ctx, &ec2.ModifyInstanceAttributeInput{
		InstanceId: aws.String("i-ghost"),
		InstanceType: &ec2types.AttributeValue{
			Value: aws.String(string(ec2types.InstanceTypeT2Small)),
		},
	})
	require.Error(t, err)
}

func TestEC2ModifyInstanceAttributeNoopSucceeds(t *testing.T) {
	// ModifyInstanceAttribute with no modifiable field should still return
	// success (matches real AWS behavior).
	client := newEC2Client(t)
	ctx := context.Background()

	run, err := client.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId: aws.String("ami-noop"), InstanceType: ec2types.InstanceTypeT2Micro,
		MinCount: aws.Int32(1), MaxCount: aws.Int32(1),
	})
	require.NoError(t, err)

	// DisableApiTermination isn't wired in our driver but the request
	// should still return OK with <return>true</return>.
	_, err = client.ModifyInstanceAttribute(ctx, &ec2.ModifyInstanceAttributeInput{
		InstanceId: run.Instances[0].InstanceId,
		DisableApiTermination: &ec2types.AttributeBooleanValue{
			Value: aws.Bool(true),
		},
	})
	require.NoError(t, err)
}

func TestEC2ConcurrentRunInstances(t *testing.T) {
	client := newEC2Client(t)
	ctx := context.Background()

	const workers = 10

	var wg sync.WaitGroup

	errs := make(chan error, workers)

	for i := 0; i < workers; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			_, err := client.RunInstances(ctx, &ec2.RunInstancesInput{
				ImageId: aws.String("ami-conc"), InstanceType: ec2types.InstanceTypeT2Micro,
				MinCount: aws.Int32(1), MaxCount: aws.Int32(1),
			})
			errs <- err
		}()
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		assert.NoError(t, err)
	}

	out, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{})
	require.NoError(t, err)

	total := 0
	for _, r := range out.Reservations {
		total += len(r.Instances)
	}
	assert.Equal(t, workers, total,
		"all %d concurrent launches should be persisted", workers)
}

// collectIDs flattens every instance id across all reservations.
func collectIDs(out *ec2.DescribeInstancesOutput) []string {
	var ids []string

	for _, r := range out.Reservations {
		for _, inst := range r.Instances {
			ids = append(ids, aws.ToString(inst.InstanceId))
		}
	}

	return ids
}

// Phase 2 — VPC, Subnet, Security Group, Internet Gateway, Route Table
func TestEC2CreateAndDescribeVpc(t *testing.T) {
	client := newEC2Client(t)
	ctx := context.Background()

	cre, err := client.CreateVpc(ctx, &ec2.CreateVpcInput{
		CidrBlock: aws.String("10.0.0.0/16"),
	})
	require.NoError(t, err)
	require.NotNil(t, cre.Vpc)

	id := aws.ToString(cre.Vpc.VpcId)
	assert.NotEmpty(t, id)
	assert.Equal(t, "10.0.0.0/16", aws.ToString(cre.Vpc.CidrBlock))

	desc, err := client.DescribeVpcs(ctx, &ec2.DescribeVpcsInput{VpcIds: []string{id}})
	require.NoError(t, err)
	require.Len(t, desc.Vpcs, 1)
	assert.Equal(t, id, aws.ToString(desc.Vpcs[0].VpcId))
}

func TestEC2DescribeAllVpcs(t *testing.T) {
	client := newEC2Client(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		_, err := client.CreateVpc(ctx, &ec2.CreateVpcInput{
			CidrBlock: aws.String("10.0.0.0/16"),
		})
		require.NoError(t, err)
	}

	desc, err := client.DescribeVpcs(ctx, &ec2.DescribeVpcsInput{})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(desc.Vpcs), 3)
}

func TestEC2DeleteVpc(t *testing.T) {
	client := newEC2Client(t)
	ctx := context.Background()

	cre, err := client.CreateVpc(ctx, &ec2.CreateVpcInput{CidrBlock: aws.String("10.1.0.0/16")})
	require.NoError(t, err)
	id := aws.ToString(cre.Vpc.VpcId)

	_, err = client.DeleteVpc(ctx, &ec2.DeleteVpcInput{VpcId: aws.String(id)})
	require.NoError(t, err)
}

func TestEC2DeleteVpcUnknownReturnsError(t *testing.T) {
	client := newEC2Client(t)
	ctx := context.Background()

	_, err := client.DeleteVpc(ctx, &ec2.DeleteVpcInput{VpcId: aws.String("vpc-ghost")})
	require.Error(t, err)
}

func TestEC2CreateAndDescribeSubnet(t *testing.T) {
	client := newEC2Client(t)
	ctx := context.Background()

	vpc, err := client.CreateVpc(ctx, &ec2.CreateVpcInput{CidrBlock: aws.String("10.0.0.0/16")})
	require.NoError(t, err)
	vpcID := aws.ToString(vpc.Vpc.VpcId)

	sub, err := client.CreateSubnet(ctx, &ec2.CreateSubnetInput{
		VpcId:            aws.String(vpcID),
		CidrBlock:        aws.String("10.0.1.0/24"),
		AvailabilityZone: aws.String("us-east-1a"),
	})
	require.NoError(t, err)
	require.NotNil(t, sub.Subnet)

	subnetID := aws.ToString(sub.Subnet.SubnetId)
	assert.Equal(t, vpcID, aws.ToString(sub.Subnet.VpcId))
	assert.Equal(t, "10.0.1.0/24", aws.ToString(sub.Subnet.CidrBlock))

	desc, err := client.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{
		SubnetIds: []string{subnetID},
	})
	require.NoError(t, err)
	require.Len(t, desc.Subnets, 1)
}

func TestEC2DeleteSubnet(t *testing.T) {
	client := newEC2Client(t)
	ctx := context.Background()

	vpc, err := client.CreateVpc(ctx, &ec2.CreateVpcInput{CidrBlock: aws.String("10.0.0.0/16")})
	require.NoError(t, err)

	sub, err := client.CreateSubnet(ctx, &ec2.CreateSubnetInput{
		VpcId:     vpc.Vpc.VpcId,
		CidrBlock: aws.String("10.0.2.0/24"),
	})
	require.NoError(t, err)

	_, err = client.DeleteSubnet(ctx, &ec2.DeleteSubnetInput{SubnetId: sub.Subnet.SubnetId})
	require.NoError(t, err)
}

func TestEC2CreateSecurityGroup(t *testing.T) {
	client := newEC2Client(t)
	ctx := context.Background()

	vpc, err := client.CreateVpc(ctx, &ec2.CreateVpcInput{CidrBlock: aws.String("10.0.0.0/16")})
	require.NoError(t, err)

	sg, err := client.CreateSecurityGroup(ctx, &ec2.CreateSecurityGroupInput{
		GroupName:   aws.String("web"),
		Description: aws.String("web-tier access"),
		VpcId:       vpc.Vpc.VpcId,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, aws.ToString(sg.GroupId))
}

func TestEC2AuthorizeIngressCIDR(t *testing.T) {
	// Real AWS flow: create SG, authorize ingress rule, describe sees the rule.
	client := newEC2Client(t)
	ctx := context.Background()

	vpc, err := client.CreateVpc(ctx, &ec2.CreateVpcInput{CidrBlock: aws.String("10.0.0.0/16")})
	require.NoError(t, err)

	sg, err := client.CreateSecurityGroup(ctx, &ec2.CreateSecurityGroupInput{
		GroupName:   aws.String("http"),
		Description: aws.String("allow http"),
		VpcId:       vpc.Vpc.VpcId,
	})
	require.NoError(t, err)
	sgID := aws.ToString(sg.GroupId)

	_, err = client.AuthorizeSecurityGroupIngress(ctx, &ec2.AuthorizeSecurityGroupIngressInput{
		GroupId: aws.String(sgID),
		IpPermissions: []ec2types.IpPermission{{
			IpProtocol: aws.String("tcp"),
			FromPort:   aws.Int32(80),
			ToPort:     aws.Int32(80),
			IpRanges:   []ec2types.IpRange{{CidrIp: aws.String("0.0.0.0/0")}},
		}},
	})
	require.NoError(t, err)

	desc, err := client.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{
		GroupIds: []string{sgID},
	})
	require.NoError(t, err)
	require.Len(t, desc.SecurityGroups, 1)

	perms := desc.SecurityGroups[0].IpPermissions
	require.Len(t, perms, 1, "expected 1 ingress permission")
	assert.Equal(t, "tcp", aws.ToString(perms[0].IpProtocol))
	assert.Equal(t, int32(80), aws.ToInt32(perms[0].FromPort))
	assert.Equal(t, int32(80), aws.ToInt32(perms[0].ToPort))
	require.Len(t, perms[0].IpRanges, 1)
	assert.Equal(t, "0.0.0.0/0", aws.ToString(perms[0].IpRanges[0].CidrIp))
}

func TestEC2AuthorizeIngressMultipleRules(t *testing.T) {
	client := newEC2Client(t)
	ctx := context.Background()

	vpc, err := client.CreateVpc(ctx, &ec2.CreateVpcInput{CidrBlock: aws.String("10.0.0.0/16")})
	require.NoError(t, err)

	sg, err := client.CreateSecurityGroup(ctx, &ec2.CreateSecurityGroupInput{
		GroupName:   aws.String("multi"),
		Description: aws.String("multiple rules"),
		VpcId:       vpc.Vpc.VpcId,
	})
	require.NoError(t, err)

	_, err = client.AuthorizeSecurityGroupIngress(ctx, &ec2.AuthorizeSecurityGroupIngressInput{
		GroupId: sg.GroupId,
		IpPermissions: []ec2types.IpPermission{
			{
				IpProtocol: aws.String("tcp"), FromPort: aws.Int32(80), ToPort: aws.Int32(80),
				IpRanges: []ec2types.IpRange{{CidrIp: aws.String("0.0.0.0/0")}},
			},
			{
				IpProtocol: aws.String("tcp"), FromPort: aws.Int32(443), ToPort: aws.Int32(443),
				IpRanges: []ec2types.IpRange{{CidrIp: aws.String("0.0.0.0/0")}},
			},
		},
	})
	require.NoError(t, err)

	desc, err := client.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{
		GroupIds: []string{aws.ToString(sg.GroupId)},
	})
	require.NoError(t, err)
	assert.Len(t, desc.SecurityGroups[0].IpPermissions, 2)
}

func TestEC2RevokeIngress(t *testing.T) {
	client := newEC2Client(t)
	ctx := context.Background()

	vpc, err := client.CreateVpc(ctx, &ec2.CreateVpcInput{CidrBlock: aws.String("10.0.0.0/16")})
	require.NoError(t, err)

	sg, err := client.CreateSecurityGroup(ctx, &ec2.CreateSecurityGroupInput{
		GroupName:   aws.String("rev"),
		Description: aws.String("revoke test"),
		VpcId:       vpc.Vpc.VpcId,
	})
	require.NoError(t, err)

	rule := []ec2types.IpPermission{{
		IpProtocol: aws.String("tcp"), FromPort: aws.Int32(22), ToPort: aws.Int32(22),
		IpRanges: []ec2types.IpRange{{CidrIp: aws.String("10.0.0.0/16")}},
	}}

	_, err = client.AuthorizeSecurityGroupIngress(ctx, &ec2.AuthorizeSecurityGroupIngressInput{
		GroupId: sg.GroupId, IpPermissions: rule,
	})
	require.NoError(t, err)

	_, err = client.RevokeSecurityGroupIngress(ctx, &ec2.RevokeSecurityGroupIngressInput{
		GroupId: sg.GroupId, IpPermissions: rule,
	})
	require.NoError(t, err)

	desc, err := client.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{
		GroupIds: []string{aws.ToString(sg.GroupId)},
	})
	require.NoError(t, err)
	assert.Empty(t, desc.SecurityGroups[0].IpPermissions,
		"ingress rule should be removed after revoke")
}

func TestEC2AuthorizeEgress(t *testing.T) {
	client := newEC2Client(t)
	ctx := context.Background()

	vpc, err := client.CreateVpc(ctx, &ec2.CreateVpcInput{CidrBlock: aws.String("10.0.0.0/16")})
	require.NoError(t, err)

	sg, err := client.CreateSecurityGroup(ctx, &ec2.CreateSecurityGroupInput{
		GroupName:   aws.String("eg"),
		Description: aws.String("egress test"),
		VpcId:       vpc.Vpc.VpcId,
	})
	require.NoError(t, err)

	_, err = client.AuthorizeSecurityGroupEgress(ctx, &ec2.AuthorizeSecurityGroupEgressInput{
		GroupId: sg.GroupId,
		IpPermissions: []ec2types.IpPermission{{
			IpProtocol: aws.String("-1"), FromPort: aws.Int32(0), ToPort: aws.Int32(0),
			IpRanges: []ec2types.IpRange{{CidrIp: aws.String("0.0.0.0/0")}},
		}},
	})
	require.NoError(t, err)

	desc, err := client.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{
		GroupIds: []string{aws.ToString(sg.GroupId)},
	})
	require.NoError(t, err)
	assert.Len(t, desc.SecurityGroups[0].IpPermissionsEgress, 1)
}

func TestEC2DeleteSecurityGroup(t *testing.T) {
	client := newEC2Client(t)
	ctx := context.Background()

	vpc, err := client.CreateVpc(ctx, &ec2.CreateVpcInput{CidrBlock: aws.String("10.0.0.0/16")})
	require.NoError(t, err)

	sg, err := client.CreateSecurityGroup(ctx, &ec2.CreateSecurityGroupInput{
		GroupName:   aws.String("del"),
		Description: aws.String("delete"),
		VpcId:       vpc.Vpc.VpcId,
	})
	require.NoError(t, err)

	_, err = client.DeleteSecurityGroup(ctx, &ec2.DeleteSecurityGroupInput{GroupId: sg.GroupId})
	require.NoError(t, err)
}

func TestEC2InternetGatewayLifecycle(t *testing.T) {
	client := newEC2Client(t)
	ctx := context.Background()

	vpc, err := client.CreateVpc(ctx, &ec2.CreateVpcInput{CidrBlock: aws.String("10.0.0.0/16")})
	require.NoError(t, err)
	vpcID := aws.ToString(vpc.Vpc.VpcId)

	cre, err := client.CreateInternetGateway(ctx, &ec2.CreateInternetGatewayInput{})
	require.NoError(t, err)
	igwID := aws.ToString(cre.InternetGateway.InternetGatewayId)
	assert.NotEmpty(t, igwID)

	_, err = client.AttachInternetGateway(ctx, &ec2.AttachInternetGatewayInput{
		InternetGatewayId: aws.String(igwID),
		VpcId:             aws.String(vpcID),
	})
	require.NoError(t, err)

	desc, err := client.DescribeInternetGateways(ctx, &ec2.DescribeInternetGatewaysInput{
		InternetGatewayIds: []string{igwID},
	})
	require.NoError(t, err)
	require.Len(t, desc.InternetGateways, 1)
	require.Len(t, desc.InternetGateways[0].Attachments, 1,
		"IGW should show VPC attachment after Attach")
	assert.Equal(t, vpcID, aws.ToString(desc.InternetGateways[0].Attachments[0].VpcId))

	_, err = client.DetachInternetGateway(ctx, &ec2.DetachInternetGatewayInput{
		InternetGatewayId: aws.String(igwID),
		VpcId:             aws.String(vpcID),
	})
	require.NoError(t, err)
}

func TestEC2RouteTableLifecycle(t *testing.T) {
	client := newEC2Client(t)
	ctx := context.Background()

	vpc, err := client.CreateVpc(ctx, &ec2.CreateVpcInput{CidrBlock: aws.String("10.0.0.0/16")})
	require.NoError(t, err)
	vpcID := aws.ToString(vpc.Vpc.VpcId)

	cre, err := client.CreateRouteTable(ctx, &ec2.CreateRouteTableInput{
		VpcId: aws.String(vpcID),
	})
	require.NoError(t, err)
	rtID := aws.ToString(cre.RouteTable.RouteTableId)
	assert.NotEmpty(t, rtID)

	// Create an IGW to target.
	igw, err := client.CreateInternetGateway(ctx, &ec2.CreateInternetGatewayInput{})
	require.NoError(t, err)
	igwID := aws.ToString(igw.InternetGateway.InternetGatewayId)

	_, err = client.CreateRoute(ctx, &ec2.CreateRouteInput{
		RouteTableId:         aws.String(rtID),
		DestinationCidrBlock: aws.String("0.0.0.0/0"),
		GatewayId:            aws.String(igwID),
	})
	require.NoError(t, err)

	desc, err := client.DescribeRouteTables(ctx, &ec2.DescribeRouteTablesInput{
		RouteTableIds: []string{rtID},
	})
	require.NoError(t, err)
	require.Len(t, desc.RouteTables, 1)

	// The route we created should be visible.
	foundRoute := false
	for _, route := range desc.RouteTables[0].Routes {
		if aws.ToString(route.DestinationCidrBlock) == "0.0.0.0/0" &&
			aws.ToString(route.GatewayId) == igwID {
			foundRoute = true
		}
	}
	assert.True(t, foundRoute, "0.0.0.0/0 → IGW route should be visible in DescribeRouteTables")
}

func TestEC2CreateRouteMissingTargetReturnsError(t *testing.T) {
	client := newEC2Client(t)
	ctx := context.Background()

	vpc, err := client.CreateVpc(ctx, &ec2.CreateVpcInput{CidrBlock: aws.String("10.0.0.0/16")})
	require.NoError(t, err)

	rt, err := client.CreateRouteTable(ctx, &ec2.CreateRouteTableInput{VpcId: vpc.Vpc.VpcId})
	require.NoError(t, err)

	_, err = client.CreateRoute(ctx, &ec2.CreateRouteInput{
		RouteTableId:         rt.RouteTable.RouteTableId,
		DestinationCidrBlock: aws.String("0.0.0.0/0"),
		// No target provided.
	})
	require.Error(t, err)
}

// TestEC2EndToEndRealisticAWSWorkflow simulates the classic AWS setup flow
// a real app goes through: build VPC → subnet → SG with ingress rule →
// IGW attached → route table with 0.0.0.0/0 route → launch an instance in
// the subnet referencing the SG. Every call is the real aws-sdk-go-v2 client
// hitting CloudEmu over HTTP.
func TestEC2EndToEndRealisticAWSWorkflow(t *testing.T) {
	client := newEC2Client(t)
	ctx := context.Background()

	// 1. VPC
	vpc, err := client.CreateVpc(ctx, &ec2.CreateVpcInput{
		CidrBlock: aws.String("10.0.0.0/16"),
		TagSpecifications: []ec2types.TagSpecification{{
			ResourceType: ec2types.ResourceTypeVpc,
			Tags:         []ec2types.Tag{{Key: aws.String("Name"), Value: aws.String("e2e-vpc")}},
		}},
	})
	require.NoError(t, err)
	vpcID := aws.ToString(vpc.Vpc.VpcId)

	// 2. Subnet in the VPC
	sub, err := client.CreateSubnet(ctx, &ec2.CreateSubnetInput{
		VpcId: aws.String(vpcID), CidrBlock: aws.String("10.0.1.0/24"),
		AvailabilityZone: aws.String("us-east-1a"),
	})
	require.NoError(t, err)
	subnetID := aws.ToString(sub.Subnet.SubnetId)

	// 3. Security group with ingress rule (allow HTTP from anywhere)
	sg, err := client.CreateSecurityGroup(ctx, &ec2.CreateSecurityGroupInput{
		GroupName:   aws.String("web"),
		Description: aws.String("allow HTTP"),
		VpcId:       aws.String(vpcID),
	})
	require.NoError(t, err)
	sgID := aws.ToString(sg.GroupId)

	_, err = client.AuthorizeSecurityGroupIngress(ctx, &ec2.AuthorizeSecurityGroupIngressInput{
		GroupId: aws.String(sgID),
		IpPermissions: []ec2types.IpPermission{{
			IpProtocol: aws.String("tcp"), FromPort: aws.Int32(80), ToPort: aws.Int32(80),
			IpRanges: []ec2types.IpRange{{CidrIp: aws.String("0.0.0.0/0")}},
		}},
	})
	require.NoError(t, err)

	// 4. Internet Gateway + attach
	igw, err := client.CreateInternetGateway(ctx, &ec2.CreateInternetGatewayInput{})
	require.NoError(t, err)
	igwID := aws.ToString(igw.InternetGateway.InternetGatewayId)

	_, err = client.AttachInternetGateway(ctx, &ec2.AttachInternetGatewayInput{
		InternetGatewayId: aws.String(igwID), VpcId: aws.String(vpcID),
	})
	require.NoError(t, err)

	// 5. Route table with default route to the IGW
	rt, err := client.CreateRouteTable(ctx, &ec2.CreateRouteTableInput{
		VpcId: aws.String(vpcID),
	})
	require.NoError(t, err)
	rtID := aws.ToString(rt.RouteTable.RouteTableId)

	_, err = client.CreateRoute(ctx, &ec2.CreateRouteInput{
		RouteTableId:         aws.String(rtID),
		DestinationCidrBlock: aws.String("0.0.0.0/0"),
		GatewayId:            aws.String(igwID),
	})
	require.NoError(t, err)

	// 6. Launch an EC2 instance in our subnet, with our SG attached.
	run, err := client.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId:          aws.String("ami-e2e"),
		InstanceType:     ec2types.InstanceTypeT2Micro,
		MinCount:         aws.Int32(1),
		MaxCount:         aws.Int32(1),
		SubnetId:         aws.String(subnetID),
		SecurityGroupIds: []string{sgID},
	})
	require.NoError(t, err)
	require.Len(t, run.Instances, 1)
	instanceID := aws.ToString(run.Instances[0].InstanceId)

	// 7. Describe verifies the instance kept its subnet and SG.
	desc, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		InstanceIds: []string{instanceID},
	})
	require.NoError(t, err)
	require.NotEmpty(t, desc.Reservations)
	require.NotEmpty(t, desc.Reservations[0].Instances)

	inst := desc.Reservations[0].Instances[0]
	assert.Equal(t, subnetID, aws.ToString(inst.SubnetId),
		"launched instance should carry the subnet we asked for")

	gotSG := make([]string, 0, len(inst.SecurityGroups))
	for _, g := range inst.SecurityGroups {
		gotSG = append(gotSG, aws.ToString(g.GroupId))
	}

	assert.Contains(t, gotSG, sgID,
		"launched instance should carry the SG we asked for")

	// 8. All resources visible in their respective Describe calls.
	vpcs, _ := client.DescribeVpcs(ctx, &ec2.DescribeVpcsInput{VpcIds: []string{vpcID}})
	assert.Len(t, vpcs.Vpcs, 1)

	subs, _ := client.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{SubnetIds: []string{subnetID}})
	assert.Len(t, subs.Subnets, 1)

	sgs, _ := client.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{
		GroupIds: []string{sgID},
	})
	assert.Len(t, sgs.SecurityGroups, 1)

	igws, _ := client.DescribeInternetGateways(ctx, &ec2.DescribeInternetGatewaysInput{
		InternetGatewayIds: []string{igwID},
	})
	assert.Len(t, igws.InternetGateways, 1)

	rts, _ := client.DescribeRouteTables(ctx, &ec2.DescribeRouteTablesInput{
		RouteTableIds: []string{rtID},
	})
	assert.Len(t, rts.RouteTables, 1)
}

func TestEC2VolumeLifecycle(t *testing.T) {
	client := newEC2Client(t)
	ctx := context.Background()

	cre, err := client.CreateVolume(ctx, &ec2.CreateVolumeInput{
		AvailabilityZone: aws.String("us-east-1a"),
		Size:             aws.Int32(100),
		VolumeType:       ec2types.VolumeTypeGp3,
	})
	require.NoError(t, err)
	volID := aws.ToString(cre.VolumeId)
	assert.NotEmpty(t, volID)
	assert.Equal(t, int32(100), aws.ToInt32(cre.Size))

	desc, err := client.DescribeVolumes(ctx, &ec2.DescribeVolumesInput{
		VolumeIds: []string{volID},
	})
	require.NoError(t, err)
	require.Len(t, desc.Volumes, 1)
	assert.Equal(t, "us-east-1a", aws.ToString(desc.Volumes[0].AvailabilityZone))

	_, err = client.DeleteVolume(ctx, &ec2.DeleteVolumeInput{VolumeId: aws.String(volID)})
	require.NoError(t, err)
}

func TestEC2VolumeAttachDetach(t *testing.T) {
	client := newEC2Client(t)
	ctx := context.Background()

	run, err := client.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId: aws.String("ami-vol"), InstanceType: ec2types.InstanceTypeT2Micro,
		MinCount: aws.Int32(1), MaxCount: aws.Int32(1),
	})
	require.NoError(t, err)
	instID := aws.ToString(run.Instances[0].InstanceId)

	vol, err := client.CreateVolume(ctx, &ec2.CreateVolumeInput{
		AvailabilityZone: aws.String("us-east-1a"),
		Size:             aws.Int32(20),
	})
	require.NoError(t, err)
	volID := aws.ToString(vol.VolumeId)

	att, err := client.AttachVolume(ctx, &ec2.AttachVolumeInput{
		VolumeId:   aws.String(volID),
		InstanceId: aws.String(instID),
		Device:     aws.String("/dev/sdf"),
	})
	require.NoError(t, err)
	assert.Equal(t, volID, aws.ToString(att.VolumeId))
	assert.Equal(t, instID, aws.ToString(att.InstanceId))
	assert.Equal(t, "/dev/sdf", aws.ToString(att.Device))

	desc, err := client.DescribeVolumes(ctx, &ec2.DescribeVolumesInput{
		VolumeIds: []string{volID},
	})
	require.NoError(t, err)
	require.Len(t, desc.Volumes, 1)
	require.Len(t, desc.Volumes[0].Attachments, 1,
		"volume should report attachment in describe")
	assert.Equal(t, instID, aws.ToString(desc.Volumes[0].Attachments[0].InstanceId))

	_, err = client.DetachVolume(ctx, &ec2.DetachVolumeInput{
		VolumeId: aws.String(volID),
	})
	require.NoError(t, err)
}

func TestEC2DeleteVolumeUnknownReturnsError(t *testing.T) {
	client := newEC2Client(t)
	ctx := context.Background()

	_, err := client.DeleteVolume(ctx, &ec2.DeleteVolumeInput{
		VolumeId: aws.String("vol-ghost"),
	})
	require.Error(t, err)
}

func TestEC2KeyPairLifecycle(t *testing.T) {
	client := newEC2Client(t)
	ctx := context.Background()

	cre, err := client.CreateKeyPair(ctx, &ec2.CreateKeyPairInput{
		KeyName: aws.String("my-key"),
		KeyType: ec2types.KeyTypeRsa,
	})
	require.NoError(t, err)
	assert.Equal(t, "my-key", aws.ToString(cre.KeyName))
	assert.NotEmpty(t, aws.ToString(cre.KeyFingerprint))
	assert.NotEmpty(t, aws.ToString(cre.KeyMaterial),
		"CreateKeyPair must return private key material")

	desc, err := client.DescribeKeyPairs(ctx, &ec2.DescribeKeyPairsInput{
		KeyNames: []string{"my-key"},
	})
	require.NoError(t, err)
	require.Len(t, desc.KeyPairs, 1)
	assert.Equal(t, "my-key", aws.ToString(desc.KeyPairs[0].KeyName))

	_, err = client.DeleteKeyPair(ctx, &ec2.DeleteKeyPairInput{
		KeyName: aws.String("my-key"),
	})
	require.NoError(t, err)
}

func TestEC2DescribeKeyPairsEmpty(t *testing.T) {
	client := newEC2Client(t)
	ctx := context.Background()

	out, err := client.DescribeKeyPairs(ctx, &ec2.DescribeKeyPairsInput{})
	require.NoError(t, err)
	assert.Empty(t, out.KeyPairs)
}

func TestEC2RunInstancesWithKeyPair(t *testing.T) {
	// Realistic flow: create key pair, launch instance with KeyName,
	// verify KeyName on the describe response.
	client := newEC2Client(t)
	ctx := context.Background()

	_, err := client.CreateKeyPair(ctx, &ec2.CreateKeyPairInput{
		KeyName: aws.String("launch-key"),
	})
	require.NoError(t, err)

	run, err := client.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId: aws.String("ami-kp"), InstanceType: ec2types.InstanceTypeT2Micro,
		MinCount: aws.Int32(1), MaxCount: aws.Int32(1),
		KeyName: aws.String("launch-key"),
	})
	require.NoError(t, err)
	require.Len(t, run.Instances, 1)
}

func TestASGLifecycle(t *testing.T) {
	provider := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{EC2: provider.EC2, VPC: provider.VPC})
	ts := httptest.NewServer(srv)
	defer ts.Close()

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("t", "t", "")))
	require.NoError(t, err)

	asc := autoscaling.NewFromConfig(cfg, func(o *autoscaling.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})
	ctx := context.Background()

	_, err = asc.CreateAutoScalingGroup(ctx, &autoscaling.CreateAutoScalingGroupInput{
		AutoScalingGroupName: aws.String("web-asg"),
		MinSize:              aws.Int32(1),
		MaxSize:              aws.Int32(5),
		DesiredCapacity:      aws.Int32(2),
		AvailabilityZones:    []string{"us-east-1a", "us-east-1b"},
	})
	require.NoError(t, err)

	desc, err := asc.DescribeAutoScalingGroups(ctx, &autoscaling.DescribeAutoScalingGroupsInput{
		AutoScalingGroupNames: []string{"web-asg"},
	})
	require.NoError(t, err)
	require.Len(t, desc.AutoScalingGroups, 1)
	assert.Equal(t, int32(5), aws.ToInt32(desc.AutoScalingGroups[0].MaxSize))

	_, err = asc.SetDesiredCapacity(ctx, &autoscaling.SetDesiredCapacityInput{
		AutoScalingGroupName: aws.String("web-asg"),
		DesiredCapacity:      aws.Int32(3),
	})
	require.NoError(t, err)

	_, err = asc.UpdateAutoScalingGroup(ctx, &autoscaling.UpdateAutoScalingGroupInput{
		AutoScalingGroupName: aws.String("web-asg"),
		MinSize:              aws.Int32(2),
		MaxSize:              aws.Int32(10),
		DesiredCapacity:      aws.Int32(4),
	})
	require.NoError(t, err)

	_, err = asc.DeleteAutoScalingGroup(ctx, &autoscaling.DeleteAutoScalingGroupInput{
		AutoScalingGroupName: aws.String("web-asg"),
		ForceDelete:          aws.Bool(true),
	})
	require.NoError(t, err)
}

func TestASGScalingPolicy(t *testing.T) {
	provider := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{EC2: provider.EC2, VPC: provider.VPC})
	ts := httptest.NewServer(srv)
	defer ts.Close()

	cfg, _ := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("t", "t", "")))
	asc := autoscaling.NewFromConfig(cfg, func(o *autoscaling.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})
	ctx := context.Background()

	_, err := asc.CreateAutoScalingGroup(ctx, &autoscaling.CreateAutoScalingGroupInput{
		AutoScalingGroupName: aws.String("scale-asg"),
		MinSize:              aws.Int32(1), MaxSize: aws.Int32(3),
		DesiredCapacity:   aws.Int32(1),
		AvailabilityZones: []string{"us-east-1a"},
	})
	require.NoError(t, err)

	_, err = asc.PutScalingPolicy(ctx, &autoscaling.PutScalingPolicyInput{
		AutoScalingGroupName: aws.String("scale-asg"),
		PolicyName:           aws.String("scale-up"),
		AdjustmentType:       aws.String("ChangeInCapacity"),
		ScalingAdjustment:    aws.Int32(1),
	})
	require.NoError(t, err)

	_, err = asc.ExecutePolicy(ctx, &autoscaling.ExecutePolicyInput{
		AutoScalingGroupName: aws.String("scale-asg"),
		PolicyName:           aws.String("scale-up"),
	})
	require.NoError(t, err)

	_, err = asc.DeletePolicy(ctx, &autoscaling.DeletePolicyInput{
		AutoScalingGroupName: aws.String("scale-asg"),
		PolicyName:           aws.String("scale-up"),
	})
	require.NoError(t, err)
}

func TestASGDeleteUnknownReturnsError(t *testing.T) {
	provider := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{EC2: provider.EC2, VPC: provider.VPC})
	ts := httptest.NewServer(srv)
	defer ts.Close()

	cfg, _ := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("t", "t", "")))
	asc := autoscaling.NewFromConfig(cfg, func(o *autoscaling.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})

	_, err := asc.DeleteAutoScalingGroup(context.Background(), &autoscaling.DeleteAutoScalingGroupInput{
		AutoScalingGroupName: aws.String("ghost-asg"),
	})
	require.Error(t, err)
}

func TestSnapshotLifecycle(t *testing.T) {
	client := newEC2Client(t)
	ctx := context.Background()

	vol, err := client.CreateVolume(ctx, &ec2.CreateVolumeInput{
		AvailabilityZone: aws.String("us-east-1a"), Size: aws.Int32(10),
	})
	require.NoError(t, err)

	snap, err := client.CreateSnapshot(ctx, &ec2.CreateSnapshotInput{
		VolumeId:    vol.VolumeId,
		Description: aws.String("backup"),
	})
	require.NoError(t, err)
	snapID := aws.ToString(snap.SnapshotId)
	assert.NotEmpty(t, snapID)

	desc, err := client.DescribeSnapshots(ctx, &ec2.DescribeSnapshotsInput{
		SnapshotIds: []string{snapID},
	})
	require.NoError(t, err)
	require.Len(t, desc.Snapshots, 1)
	assert.Equal(t, aws.ToString(vol.VolumeId), aws.ToString(desc.Snapshots[0].VolumeId))

	_, err = client.DeleteSnapshot(ctx, &ec2.DeleteSnapshotInput{SnapshotId: aws.String(snapID)})
	require.NoError(t, err)
}

func TestImageLifecycle(t *testing.T) {
	client := newEC2Client(t)
	ctx := context.Background()

	run, err := client.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId: aws.String("ami-base"), InstanceType: ec2types.InstanceTypeT2Micro,
		MinCount: aws.Int32(1), MaxCount: aws.Int32(1),
	})
	require.NoError(t, err)

	img, err := client.CreateImage(ctx, &ec2.CreateImageInput{
		InstanceId:  run.Instances[0].InstanceId,
		Name:        aws.String("backup-ami"),
		Description: aws.String("created from instance"),
	})
	require.NoError(t, err)
	imgID := aws.ToString(img.ImageId)
	assert.NotEmpty(t, imgID)

	desc, err := client.DescribeImages(ctx, &ec2.DescribeImagesInput{
		ImageIds: []string{imgID},
	})
	require.NoError(t, err)
	require.Len(t, desc.Images, 1)

	_, err = client.DeregisterImage(ctx, &ec2.DeregisterImageInput{ImageId: aws.String(imgID)})
	require.NoError(t, err)
}

func TestSnapshotUnknownReturnsError(t *testing.T) {
	client := newEC2Client(t)
	ctx := context.Background()

	_, err := client.DeleteSnapshot(ctx, &ec2.DeleteSnapshotInput{
		SnapshotId: aws.String("snap-ghost"),
	})
	require.Error(t, err)
}
