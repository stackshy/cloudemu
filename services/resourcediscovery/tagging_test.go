package resourcediscovery

import (
	"context"
	"strings"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseSupportedARN(t *testing.T) {
	tests := []struct {
		name      string
		arn       string
		wantSvc   string
		wantType  string
		wantID    string
		wantErr   bool
		errSubstr string
	}{
		{name: "s3 bucket", arn: "arn:aws:s3:::my-bucket", wantSvc: "s3", wantID: "my-bucket"},
		{name: "dynamodb table", arn: "arn:aws:dynamodb:us-east-1:111:table/MyTable", wantSvc: "dynamodb", wantType: "table", wantID: "MyTable"},
		{name: "ec2 instance", arn: "arn:aws:ec2:us-east-1:111:instance/i-abc", wantSvc: "ec2", wantType: "instance", wantID: "i-abc"},
		{name: "ec2 volume", arn: "arn:aws:ec2:us-east-1:111:volume/vol-abc", wantSvc: "ec2", wantType: "volume", wantID: "vol-abc"},
		{name: "ec2 snapshot", arn: "arn:aws:ec2:us-east-1:111:snapshot/snap-abc", wantSvc: "ec2", wantType: "snapshot", wantID: "snap-abc"},
		{name: "ec2 image", arn: "arn:aws:ec2:us-east-1:111:image/ami-abc", wantSvc: "ec2", wantType: "image", wantID: "ami-abc"},
		{name: "vpc", arn: "arn:aws:ec2:us-east-1:111:vpc/vpc-abc", wantSvc: "ec2", wantType: "vpc", wantID: "vpc-abc"},
		{name: "subnet", arn: "arn:aws:ec2:us-east-1:111:subnet/subnet-abc", wantSvc: "ec2", wantType: "subnet", wantID: "subnet-abc"},
		{name: "security-group", arn: "arn:aws:ec2:us-east-1:111:security-group/sg-abc", wantSvc: "ec2", wantType: "security-group", wantID: "sg-abc"},
		{name: "lambda function", arn: "arn:aws:lambda:us-east-1:111:function:fn", wantSvc: "lambda", wantType: "function", wantID: "fn"},
		{name: "lambda function versioned", arn: "arn:aws:lambda:us-east-1:111:function:fn:2", wantSvc: "lambda", wantType: "function", wantID: "fn"},
		{name: "secrets manager", arn: "arn:aws:secretsmanager:us-east-1:111:secret:db-pw", wantSvc: "secretsmanager", wantType: "secret", wantID: "db-pw"},
		{name: "sns topic", arn: "arn:aws:sns:us-east-1:111:my-topic", wantSvc: "sns", wantID: "my-topic"},
		{name: "sqs queue", arn: "arn:aws:sqs:us-east-1:111:my-queue", wantSvc: "sqs", wantID: "my-queue"},
		{name: "ec2 unsupported type", arn: "arn:aws:ec2:us-east-1:111:natgateway/nat-abc", wantErr: true, errSubstr: "is not supported"},
		{name: "sns subscription rejected", arn: "arn:aws:sns:us-east-1:111:subscription/uuid", wantErr: true, errSubstr: "expected SNS topic"},
		{name: "kms unsupported service", arn: "arn:aws:kms:us-east-1:111:key/abc", wantErr: true, errSubstr: "not yet supported"},
		{name: "non-aws partition", arn: "arn:azure:s3:::x", wantErr: true, errSubstr: "only AWS ARNs"},
		{name: "malformed", arn: "not-an-arn", wantErr: true, errSubstr: "only AWS ARNs"},
		{name: "missing parts", arn: "arn:aws:s3", wantErr: true, errSubstr: "malformed ARN"},
		{name: "s3 object rejected", arn: "arn:aws:s3:::bkt/obj.txt", wantErr: true, errSubstr: "object ARNs are not supported"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseSupportedARN(tt.arn)
			if tt.wantErr {
				require.Error(t, err)
				assert.True(t, strings.Contains(err.Error(), tt.errSubstr),
					"error %q did not contain %q", err.Error(), tt.errSubstr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantSvc, got.service)
			assert.Equal(t, tt.wantType, got.resourceType)
			assert.Equal(t, tt.wantID, got.id)
		})
	}
}

func TestTagResourceByARN_S3(t *testing.T) {
	ctx := context.Background()
	f := newAWSFixture(t)

	require.NoError(t, f.s3.CreateBucket(ctx, "tag-me"))
	require.NoError(t, f.engine.TagResourceByARN(ctx, "arn:aws:s3:::tag-me",
		map[string]string{"env": "prod", "team": "platform"}))

	got, err := f.s3.GetBucketTagging(ctx, "tag-me")
	require.NoError(t, err)
	assert.Equal(t, "prod", got["env"])
	assert.Equal(t, "platform", got["team"])

	// Merge semantics: existing keys preserved, overlapping overwritten.
	require.NoError(t, f.engine.TagResourceByARN(ctx, "arn:aws:s3:::tag-me",
		map[string]string{"env": "stage", "new": "x"}))
	got, err = f.s3.GetBucketTagging(ctx, "tag-me")
	require.NoError(t, err)
	assert.Equal(t, "stage", got["env"])
	assert.Equal(t, "platform", got["team"], "non-overlapping key should survive merge")
	assert.Equal(t, "x", got["new"])

	require.NoError(t, f.engine.UntagResourceByARN(ctx, "arn:aws:s3:::tag-me", []string{"env", "missing"}))
	got, err = f.s3.GetBucketTagging(ctx, "tag-me")
	require.NoError(t, err)
	_, has := got["env"]
	assert.False(t, has)
	assert.Equal(t, "platform", got["team"])
}

func TestTagResourceByARN_DynamoDB(t *testing.T) {
	ctx := context.Background()
	f := newAWSFixture(t)

	seedDDB(t, f, "tag-tbl", nil)

	arn := "arn:aws:dynamodb:us-east-1:123456789012:table/tag-tbl"
	require.NoError(t, f.engine.TagResourceByARN(ctx, arn, map[string]string{"env": "prod"}))

	got, err := f.ddb.ListTagsOfResource(ctx, "tag-tbl")
	require.NoError(t, err)
	assert.Equal(t, "prod", got["env"])

	require.NoError(t, f.engine.UntagResourceByARN(ctx, arn, []string{"env"}))
	got, err = f.ddb.ListTagsOfResource(ctx, "tag-tbl")
	require.NoError(t, err)
	_, has := got["env"]
	assert.False(t, has)
}

func TestTagResourceByARN_VPC(t *testing.T) {
	ctx := context.Background()
	f := newAWSFixture(t)

	v, err := f.vpc.CreateVPC(ctx, netdriver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)

	arn := "arn:aws:ec2:us-east-1:123456789012:vpc/" + v.ID
	require.NoError(t, f.engine.TagResourceByARN(ctx, arn, map[string]string{"env": "prod"}))

	got, err := f.vpc.DescribeVPCs(ctx, []string{v.ID})
	require.NoError(t, err)
	assert.Equal(t, "prod", got[0].Tags["env"])

	require.NoError(t, f.engine.UntagResourceByARN(ctx, arn, []string{"env"}))
	got, err = f.vpc.DescribeVPCs(ctx, []string{v.ID})
	require.NoError(t, err)
	_, has := got[0].Tags["env"]
	assert.False(t, has)
}

func TestTagResourceByARN_Subnet(t *testing.T) {
	ctx := context.Background()
	f := newAWSFixture(t)

	v, err := f.vpc.CreateVPC(ctx, netdriver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)
	s, err := f.vpc.CreateSubnet(ctx, netdriver.SubnetConfig{VPCID: v.ID, CIDRBlock: "10.0.1.0/24"})
	require.NoError(t, err)

	arn := "arn:aws:ec2:us-east-1:123456789012:subnet/" + s.ID
	require.NoError(t, f.engine.TagResourceByARN(ctx, arn, map[string]string{"tier": "private"}))

	got, err := f.vpc.DescribeSubnets(ctx, []string{s.ID})
	require.NoError(t, err)
	assert.Equal(t, "private", got[0].Tags["tier"])
}

func TestTagResourceByARN_SecurityGroup(t *testing.T) {
	ctx := context.Background()
	f := newAWSFixture(t)

	v, err := f.vpc.CreateVPC(ctx, netdriver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)
	sg, err := f.vpc.CreateSecurityGroup(ctx, netdriver.SecurityGroupConfig{Name: "web", VPCID: v.ID})
	require.NoError(t, err)

	arn := "arn:aws:ec2:us-east-1:123456789012:security-group/" + sg.ID
	require.NoError(t, f.engine.TagResourceByARN(ctx, arn, map[string]string{"role": "web"}))

	got, err := f.vpc.DescribeSecurityGroups(ctx, []string{sg.ID})
	require.NoError(t, err)
	assert.Equal(t, "web", got[0].Tags["role"])
}

func TestTagResourceByARN_UnsupportedReturnsInvalidArgument(t *testing.T) {
	ctx := context.Background()
	f := newAWSFixture(t)

	// KMS has no tag store wired through the RGT dispatcher.
	err := f.engine.TagResourceByARN(ctx, "arn:aws:kms:us-east-1:111:key/abc",
		map[string]string{"k": "v"})
	require.Error(t, err)
	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))
	assert.Contains(t, err.Error(), "not yet supported")
}

func TestTagResourceByARN_EC2Instance(t *testing.T) {
	ctx := context.Background()
	f := newAWSFixture(t)

	insts, err := f.ec2.RunInstances(ctx, computedriver.InstanceConfig{
		ImageID: "ami-1", InstanceType: "t3.micro",
	}, 1)
	require.NoError(t, err)
	id := insts[0].ID

	arn := "arn:aws:ec2:us-east-1:123456789012:instance/" + id
	require.NoError(t, f.engine.TagResourceByARN(ctx, arn, map[string]string{"Environment": "prod", "team": "core"}))

	got, err := f.ec2.DescribeInstances(ctx, []string{id}, nil, computedriver.DescribeInstancesOptions{})
	require.NoError(t, err)
	assert.Equal(t, "prod", got[0].Tags["Environment"])
	assert.Equal(t, "core", got[0].Tags["team"])

	// Merge is additive: a second tag call keeps the earlier keys.
	require.NoError(t, f.engine.TagResourceByARN(ctx, arn, map[string]string{"Environment": "stage", "new": "x"}))
	got, err = f.ec2.DescribeInstances(ctx, []string{id}, nil, computedriver.DescribeInstancesOptions{})
	require.NoError(t, err)
	assert.Equal(t, "stage", got[0].Tags["Environment"])
	assert.Equal(t, "core", got[0].Tags["team"], "non-overlapping key should survive merge")
	assert.Equal(t, "x", got[0].Tags["new"])

	require.NoError(t, f.engine.UntagResourceByARN(ctx, arn, []string{"Environment", "missing"}))
	got, err = f.ec2.DescribeInstances(ctx, []string{id}, nil, computedriver.DescribeInstancesOptions{})
	require.NoError(t, err)
	_, has := got[0].Tags["Environment"]
	assert.False(t, has)
	assert.Equal(t, "core", got[0].Tags["team"])
}

func TestTagResourceByARN_EC2Volume(t *testing.T) {
	ctx := context.Background()
	f := newAWSFixture(t)

	vol, err := f.ec2.CreateVolume(ctx, computedriver.VolumeConfig{Size: 8, VolumeType: "gp3", AvailabilityZone: "us-east-1a"})
	require.NoError(t, err)

	arn := "arn:aws:ec2:us-east-1:123456789012:volume/" + vol.ID
	require.NoError(t, f.engine.TagResourceByARN(ctx, arn, map[string]string{"backup": "daily"}))

	got, err := f.ec2.DescribeVolumes(ctx, []string{vol.ID})
	require.NoError(t, err)
	assert.Equal(t, "daily", got[0].Tags["backup"])

	require.NoError(t, f.engine.UntagResourceByARN(ctx, arn, []string{"backup"}))
	got, err = f.ec2.DescribeVolumes(ctx, []string{vol.ID})
	require.NoError(t, err)
	_, has := got[0].Tags["backup"]
	assert.False(t, has)
}

func TestTagResourceByARN_EC2Snapshot(t *testing.T) {
	ctx := context.Background()
	f := newAWSFixture(t)

	vol, err := f.ec2.CreateVolume(ctx, computedriver.VolumeConfig{Size: 8, VolumeType: "gp3", AvailabilityZone: "us-east-1a"})
	require.NoError(t, err)
	snap, err := f.ec2.CreateSnapshot(ctx, computedriver.SnapshotConfig{VolumeID: vol.ID})
	require.NoError(t, err)

	arn := "arn:aws:ec2:us-east-1:123456789012:snapshot/" + snap.ID
	require.NoError(t, f.engine.TagResourceByARN(ctx, arn, map[string]string{"keep": "30d"}))

	got, err := f.ec2.DescribeSnapshots(ctx, []string{snap.ID})
	require.NoError(t, err)
	assert.Equal(t, "30d", got[0].Tags["keep"])
}

func TestTagResourceByARN_Lambda(t *testing.T) {
	ctx := context.Background()
	f := newAWSFixture(t)

	seedLambda(t, f, "handler", nil)

	arn := "arn:aws:lambda:us-east-1:123456789012:function:handler"
	require.NoError(t, f.engine.TagResourceByARN(ctx, arn, map[string]string{"env": "prod"}))

	got, err := f.lambda.GetFunction(ctx, "handler")
	require.NoError(t, err)
	assert.Equal(t, "prod", got.Tags["env"])

	// A version-qualified ARN resolves to the same function's tags.
	require.NoError(t, f.engine.TagResourceByARN(ctx, arn+":3", map[string]string{"team": "data"}))
	got, err = f.lambda.GetFunction(ctx, "handler")
	require.NoError(t, err)
	assert.Equal(t, "data", got.Tags["team"])

	require.NoError(t, f.engine.UntagResourceByARN(ctx, arn, []string{"env"}))
	got, err = f.lambda.GetFunction(ctx, "handler")
	require.NoError(t, err)
	_, has := got.Tags["env"]
	assert.False(t, has)
}

func TestTagResourceByARN_Secret(t *testing.T) {
	ctx := context.Background()
	f := newAWSFixture(t)

	seedSecret(t, f, "db-password", nil)

	arn := "arn:aws:secretsmanager:us-east-1:123456789012:secret:db-password"
	require.NoError(t, f.engine.TagResourceByARN(ctx, arn, map[string]string{"rotation": "30d"}))

	got, err := f.secrets.GetSecret(ctx, "db-password")
	require.NoError(t, err)
	assert.Equal(t, "30d", got.Tags["rotation"])

	require.NoError(t, f.engine.UntagResourceByARN(ctx, arn, []string{"rotation"}))
	got, err = f.secrets.GetSecret(ctx, "db-password")
	require.NoError(t, err)
	_, has := got.Tags["rotation"]
	assert.False(t, has)
}

func TestTagResourceByARN_SNSTopic(t *testing.T) {
	ctx := context.Background()
	f := newAWSFixture(t)

	seedSNS(t, f, "alerts", nil)

	arn := "arn:aws:sns:us-east-1:123456789012:alerts"
	require.NoError(t, f.engine.TagResourceByARN(ctx, arn, map[string]string{"severity": "high"}))

	assert.Equal(t, "high", snsTopicTags(t, f, "alerts")["severity"])

	require.NoError(t, f.engine.UntagResourceByARN(ctx, arn, []string{"severity"}))
	_, has := snsTopicTags(t, f, "alerts")["severity"]
	assert.False(t, has)
}

func TestTagResourceByARN_SQSQueue(t *testing.T) {
	ctx := context.Background()
	f := newAWSFixture(t)

	seedSQS(t, f, "jobs", nil)

	arn := "arn:aws:sqs:us-east-1:123456789012:jobs"
	require.NoError(t, f.engine.TagResourceByARN(ctx, arn, map[string]string{"pipeline": "etl"}))

	assert.Equal(t, "etl", sqsQueueTags(t, f, "jobs")["pipeline"])

	require.NoError(t, f.engine.UntagResourceByARN(ctx, arn, []string{"pipeline"}))
	_, has := sqsQueueTags(t, f, "jobs")["pipeline"]
	assert.False(t, has)
}

func TestTagResourceByARN_NotFound(t *testing.T) {
	ctx := context.Background()
	f := newAWSFixture(t)

	cases := map[string]string{
		"instance": "arn:aws:ec2:us-east-1:123456789012:instance/i-missing",
		"lambda":   "arn:aws:lambda:us-east-1:123456789012:function:ghost",
		"secret":   "arn:aws:secretsmanager:us-east-1:123456789012:secret:ghost",
		"sns":      "arn:aws:sns:us-east-1:123456789012:ghost",
		"sqs":      "arn:aws:sqs:us-east-1:123456789012:ghost",
	}

	for name, arn := range cases {
		t.Run(name, func(t *testing.T) {
			err := f.engine.TagResourceByARN(ctx, arn, map[string]string{"k": "v"})
			require.Error(t, err)
			assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err), "expected NotFound for %s", arn)
		})
	}
}

// snsTopicTags reads a topic's tags back through the SNS list surface.
func snsTopicTags(t *testing.T, f *fixture, name string) map[string]string {
	t.Helper()
	topics, err := f.sns.ListTopics(context.Background(), scope.Scope{})
	require.NoError(t, err)
	for i := range topics {
		if topics[i].Name == name {
			return topics[i].Tags
		}
	}
	t.Fatalf("topic %q not found", name)
	return nil
}

// sqsQueueTags reads a queue's tags back through the SQS list surface.
func sqsQueueTags(t *testing.T, f *fixture, name string) map[string]string {
	t.Helper()
	queues, err := f.sqs.ListQueues(context.Background(), "")
	require.NoError(t, err)
	for i := range queues {
		if queues[i].Name == name {
			return queues[i].Tags
		}
	}
	t.Fatalf("queue %q not found", name)
	return nil
}

func TestTagResourceByARN_MissingDriverReturnsFailedPrecondition(t *testing.T) {
	ctx := context.Background()
	// Engine with no drivers wired.
	eng := New(ProviderAWS, "111", "us-east-1", nil)

	err := eng.TagResourceByARN(ctx, "arn:aws:s3:::x", map[string]string{"k": "v"})
	require.Error(t, err)
	assert.Equal(t, cerrors.FailedPrecondition, cerrors.GetCode(err))
}
