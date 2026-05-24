package resourcediscovery

import (
	"context"
	"strings"
	"testing"

	cerrors "github.com/stackshy/cloudemu/errors"
	netdriver "github.com/stackshy/cloudemu/networking/driver"
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
		{name: "vpc", arn: "arn:aws:ec2:us-east-1:111:vpc/vpc-abc", wantSvc: "ec2", wantType: "vpc", wantID: "vpc-abc"},
		{name: "subnet", arn: "arn:aws:ec2:us-east-1:111:subnet/subnet-abc", wantSvc: "ec2", wantType: "subnet", wantID: "subnet-abc"},
		{name: "security-group", arn: "arn:aws:ec2:us-east-1:111:security-group/sg-abc", wantSvc: "ec2", wantType: "security-group", wantID: "sg-abc"},
		{name: "instance unsupported", arn: "arn:aws:ec2:us-east-1:111:instance/i-abc", wantErr: true, errSubstr: "not yet supported"},
		{name: "lambda unsupported", arn: "arn:aws:lambda:us-east-1:111:function:fn", wantErr: true, errSubstr: "not yet supported"},
		{name: "non-aws partition", arn: "arn:azure:s3:::x", wantErr: true, errSubstr: "only AWS ARNs"},
		{name: "malformed", arn: "not-an-arn", wantErr: true, errSubstr: "only AWS ARNs"},
		{name: "missing parts", arn: "arn:aws:s3", wantErr: true, errSubstr: "malformed ARN"},
		{name: "s3 object rejected", arn: "arn:aws:s3:::bkt/obj.txt", wantErr: true, errSubstr: "object ARNs are not yet supported"},
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

	err := f.engine.TagResourceByARN(ctx, "arn:aws:lambda:us-east-1:111:function:fn",
		map[string]string{"k": "v"})
	require.Error(t, err)
	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))
	assert.Contains(t, err.Error(), "not yet supported")
}

func TestTagResourceByARN_MissingDriverReturnsFailedPrecondition(t *testing.T) {
	ctx := context.Background()
	// Engine with no drivers wired.
	eng := New(ProviderAWS, "111", "us-east-1", nil)

	err := eng.TagResourceByARN(ctx, "arn:aws:s3:::x", map[string]string{"k": "v"})
	require.Error(t, err)
	assert.Equal(t, cerrors.FailedPrecondition, cerrors.GetCode(err))
}
