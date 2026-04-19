package aws_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/stackshy/cloudemu"
	awsserver "github.com/stackshy/cloudemu/server/aws"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newEC2Client(t *testing.T) *ec2.Client {
	t.Helper()

	provider := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{EC2: provider.EC2})
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
