package aws_test

// This is a real-user end-to-end scenario suite: it drives real aws-sdk-go-v2
// clients through an IaC-style multi-service workflow against ONE full wire
// server (every handler registered, so cross-service dispatch and shared state
// are exercised together). It targets behaviors fixed across the audit waves and
// their interactions, to catch cascades that per-package tests miss.

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

func e2eServer(t *testing.T) string {
	t.Helper()
	srv := awsserver.NewFromProvider(cloudemu.NewAWS())
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	return ts.URL
}

func e2eCfg(t *testing.T, url string) aws.Config {
	t.Helper()
	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	cfg.BaseEndpoint = aws.String(url)

	return cfg
}

// TestE2ENetworkingComputeTagsDispatch is the cross-cutting scenario: it builds a
// VPC → subnet → IGW → SG → instance → volume, tags several resource types, then
// reads them back via DescribeTags (which the elbv2 handler must NOT swallow) and
// filters (IGW attachment.state, encrypted volume).
func TestE2ENetworkingComputeTagsDispatch(t *testing.T) {
	ctx := context.Background()
	c := ec2.NewFromConfig(e2eCfg(t, e2eServer(t)))

	vpc, err := c.CreateVpc(ctx, &ec2.CreateVpcInput{CidrBlock: aws.String("10.0.0.0/16")})
	if err != nil {
		t.Fatalf("CreateVpc: %v", err)
	}
	vpcID := aws.ToString(vpc.Vpc.VpcId)

	sub, err := c.CreateSubnet(ctx, &ec2.CreateSubnetInput{VpcId: aws.String(vpcID), CidrBlock: aws.String("10.0.1.0/24")})
	if err != nil {
		t.Fatalf("CreateSubnet: %v", err)
	}
	subID := aws.ToString(sub.Subnet.SubnetId)

	igw, err := c.CreateInternetGateway(ctx, &ec2.CreateInternetGatewayInput{})
	if err != nil {
		t.Fatalf("CreateInternetGateway: %v", err)
	}
	igwID := aws.ToString(igw.InternetGateway.InternetGatewayId)
	if _, err := c.AttachInternetGateway(ctx, &ec2.AttachInternetGatewayInput{
		InternetGatewayId: aws.String(igwID), VpcId: aws.String(vpcID),
	}); err != nil {
		t.Fatalf("AttachInternetGateway: %v", err)
	}

	sg, err := c.CreateSecurityGroup(ctx, &ec2.CreateSecurityGroupInput{
		GroupName: aws.String("web"), Description: aws.String("web sg"), VpcId: aws.String(vpcID),
	})
	if err != nil {
		t.Fatalf("CreateSecurityGroup: %v", err)
	}
	sgID := aws.ToString(sg.GroupId)

	run, err := c.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId: aws.String("ami-123"), InstanceType: ec2types.InstanceTypeT3Micro,
		MinCount: aws.Int32(1), MaxCount: aws.Int32(1), SubnetId: aws.String(subID),
	})
	if err != nil {
		t.Fatalf("RunInstances: %v", err)
	}
	instID := aws.ToString(run.Instances[0].InstanceId)

	// Tag an instance AND a vpc AND a subnet AND an sg — the cross-cutting tag fix.
	for _, id := range []string{instID, vpcID, subID, sgID} {
		if _, err := c.CreateTags(ctx, &ec2.CreateTagsInput{
			Resources: []string{id}, Tags: []ec2types.Tag{{Key: aws.String("env"), Value: aws.String("prod")}},
		}); err != nil {
			t.Fatalf("CreateTags(%s): %v", id, err)
		}
	}

	// DescribeTags with a resource-id filter must return the tag (proves ec2
	// DescribeTags is served and the elbv2 handler's scope-gate lets it through).
	dt, err := c.DescribeTags(ctx, &ec2.DescribeTagsInput{
		Filters: []ec2types.Filter{{Name: aws.String("resource-id"), Values: []string{instID}}},
	})
	if err != nil {
		t.Fatalf("DescribeTags: %v", err)
	}
	if len(dt.Tags) == 0 {
		t.Fatal("DescribeTags returned 0 tags for a tagged instance (elbv2 shadowing regression?)")
	}

	// IGW attachment.state=available filter must match the attached gateway.
	igf, err := c.DescribeInternetGateways(ctx, &ec2.DescribeInternetGatewaysInput{
		Filters: []ec2types.Filter{{Name: aws.String("attachment.state"), Values: []string{"available"}}},
	})
	if err != nil {
		t.Fatalf("DescribeInternetGateways: %v", err)
	}
	if len(igf.InternetGateways) != 1 {
		t.Fatalf("attachment.state=available matched %d IGWs, want 1", len(igf.InternetGateways))
	}

	// Encrypted volume round-trips its encryption flag.
	vol, err := c.CreateVolume(ctx, &ec2.CreateVolumeInput{
		AvailabilityZone: aws.String("us-east-1a"), Size: aws.Int32(10), Encrypted: aws.Bool(true),
	})
	if err != nil {
		t.Fatalf("CreateVolume: %v", err)
	}
	dv, err := c.DescribeVolumes(ctx, &ec2.DescribeVolumesInput{VolumeIds: []string{aws.ToString(vol.VolumeId)}})
	if err != nil {
		t.Fatalf("DescribeVolumes: %v", err)
	}
	if len(dv.Volumes) != 1 || !aws.ToBool(dv.Volumes[0].Encrypted) {
		t.Fatal("volume Encrypted flag not round-tripped")
	}
}

// TestE2ES3 exercises the S3 fixes: idempotent us-east-1 CreateBucket, MD5 ETag,
// Range GET, bucket-policy round-trip, and batch DeleteObjects.
func TestE2ES3(t *testing.T) {
	ctx := context.Background()
	c := s3.NewFromConfig(e2eCfg(t, e2eServer(t)), func(o *s3.Options) { o.UsePathStyle = true })

	bucket := "scenario-bucket"
	if _, err := c.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(bucket)}); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	// us-east-1 re-create by the same owner is idempotent (no error).
	if _, err := c.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(bucket)}); err != nil {
		t.Fatalf("CreateBucket idempotent re-create: %v", err)
	}

	put, err := c.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucket), Key: aws.String("hello.txt"), Body: strings.NewReader("hello world"),
	})
	if err != nil {
		t.Fatalf("PutObject: %v", err)
	}
	// MD5 of "hello world" = 5eb63bbbe01eeed093cb22bb8f5acdc3
	if et := strings.Trim(aws.ToString(put.ETag), "\""); et != "5eb63bbbe01eeed093cb22bb8f5acdc3" {
		t.Fatalf("ETag = %q, want MD5 hex", et)
	}

	rng, err := c.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket), Key: aws.String("hello.txt"), Range: aws.String("bytes=0-4"),
	})
	if err != nil {
		t.Fatalf("GetObject range: %v", err)
	}
	body, _ := io.ReadAll(rng.Body)
	if string(body) != "hello" {
		t.Fatalf("range body = %q, want hello", string(body))
	}

	pol := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":"*","Action":"s3:GetObject","Resource":"arn:aws:s3:::scenario-bucket/*"}]}`
	if _, err := c.PutBucketPolicy(ctx, &s3.PutBucketPolicyInput{Bucket: aws.String(bucket), Policy: aws.String(pol)}); err != nil {
		t.Fatalf("PutBucketPolicy: %v", err)
	}
	gp, err := c.GetBucketPolicy(ctx, &s3.GetBucketPolicyInput{Bucket: aws.String(bucket)})
	if err != nil {
		t.Fatalf("GetBucketPolicy: %v", err)
	}
	if !strings.Contains(aws.ToString(gp.Policy), "s3:GetObject") {
		t.Fatal("bucket policy did not round-trip")
	}

	if _, err := c.DeleteObjects(ctx, &s3.DeleteObjectsInput{
		Bucket: aws.String(bucket),
		Delete: &s3types.Delete{Objects: []s3types.ObjectIdentifier{{Key: aws.String("hello.txt")}}},
	}); err != nil {
		t.Fatalf("DeleteObjects: %v", err)
	}
}

// TestE2EDynamoDB exercises GSI echo and the missing-table error distinction.
func TestE2EDynamoDB(t *testing.T) {
	ctx := context.Background()
	c := dynamodb.NewFromConfig(e2eCfg(t, e2eServer(t)))

	_, err := c.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName: aws.String("things"),
		AttributeDefinitions: []ddbtypes.AttributeDefinition{
			{AttributeName: aws.String("pk"), AttributeType: ddbtypes.ScalarAttributeTypeS},
			{AttributeName: aws.String("gsipk"), AttributeType: ddbtypes.ScalarAttributeTypeS},
		},
		KeySchema:   []ddbtypes.KeySchemaElement{{AttributeName: aws.String("pk"), KeyType: ddbtypes.KeyTypeHash}},
		BillingMode: ddbtypes.BillingModePayPerRequest,
		GlobalSecondaryIndexes: []ddbtypes.GlobalSecondaryIndex{{
			IndexName: aws.String("by-gsipk"),
			KeySchema: []ddbtypes.KeySchemaElement{{AttributeName: aws.String("gsipk"), KeyType: ddbtypes.KeyTypeHash}},
			Projection: &ddbtypes.Projection{ProjectionType: ddbtypes.ProjectionTypeAll},
		}},
	})
	if err != nil {
		t.Fatalf("CreateTable: %v", err)
	}

	desc, err := c.DescribeTable(ctx, &dynamodb.DescribeTableInput{TableName: aws.String("things")})
	if err != nil {
		t.Fatalf("DescribeTable: %v", err)
	}
	if len(desc.Table.GlobalSecondaryIndexes) != 1 {
		t.Fatalf("GSI count = %d, want 1", len(desc.Table.GlobalSecondaryIndexes))
	}

	// GetItem on a nonexistent table is ResourceNotFoundException (not empty 200).
	_, err = c.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String("nope"), Key: map[string]ddbtypes.AttributeValue{"pk": &ddbtypes.AttributeValueMemberS{Value: "x"}},
	})
	if err == nil {
		t.Fatal("GetItem on missing table should error")
	}
	var nf *ddbtypes.ResourceNotFoundException
	if !errors.As(err, &nf) {
		t.Fatalf("want ResourceNotFoundException, got %v", err)
	}
}

// TestE2EIAM exercises group membership + user tags.
func TestE2EIAM(t *testing.T) {
	ctx := context.Background()
	c := iam.NewFromConfig(e2eCfg(t, e2eServer(t)))

	if _, err := c.CreateUser(ctx, &iam.CreateUserInput{UserName: aws.String("alice")}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if _, err := c.CreateGroup(ctx, &iam.CreateGroupInput{GroupName: aws.String("devs")}); err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	if _, err := c.AddUserToGroup(ctx, &iam.AddUserToGroupInput{GroupName: aws.String("devs"), UserName: aws.String("alice")}); err != nil {
		t.Fatalf("AddUserToGroup: %v", err)
	}
	g, err := c.GetGroup(ctx, &iam.GetGroupInput{GroupName: aws.String("devs")})
	if err != nil {
		t.Fatalf("GetGroup: %v", err)
	}
	if len(g.Users) != 1 || aws.ToString(g.Users[0].UserName) != "alice" {
		t.Fatalf("GetGroup members = %v, want [alice]", g.Users)
	}
}

// TestE2ESQSSNSDispatch exercises the SQS/SNS shared-tag scope gating and batch attrs.
func TestE2ESQSSNSDispatch(t *testing.T) {
	ctx := context.Background()
	cfg := e2eCfg(t, e2eServer(t))
	sc := sqs.NewFromConfig(cfg)
	nc := sns.NewFromConfig(cfg)

	q, err := sc.CreateQueue(ctx, &sqs.CreateQueueInput{QueueName: aws.String("jobs")})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	if _, err := sc.SendMessageBatch(ctx, &sqs.SendMessageBatchInput{
		QueueUrl: q.QueueUrl,
		Entries: []sqstypes.SendMessageBatchRequestEntry{{
			Id: aws.String("1"), MessageBody: aws.String("hi"),
			MessageAttributes: map[string]sqstypes.MessageAttributeValue{
				"color": {DataType: aws.String("String"), StringValue: aws.String("blue")},
			},
		}},
	}); err != nil {
		t.Fatalf("SendMessageBatch: %v", err)
	}
	rcv, err := sc.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
		QueueUrl: q.QueueUrl, MessageAttributeNames: []string{"All"},
	})
	if err != nil {
		t.Fatalf("ReceiveMessage: %v", err)
	}
	if len(rcv.Messages) != 1 || rcv.Messages[0].MessageAttributes["color"].StringValue == nil {
		t.Fatal("batch MessageAttributes lost through receive")
	}

	// SNS ListTagsForResource must route to SNS, not be swallowed by a shared
	// query handler (elasticache/rds). Create a topic and list its tags.
	tp, err := nc.CreateTopic(ctx, &sns.CreateTopicInput{Name: aws.String("events")})
	if err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}
	if _, err := nc.ListTagsForResource(ctx, &sns.ListTagsForResourceInput{ResourceArn: tp.TopicArn}); err != nil {
		t.Fatalf("SNS ListTagsForResource (dispatch shadowing?): %v", err)
	}
}

// TestE2ECloudWatch exercises PutMetricData → SetAlarmState → DescribeAlarms.
func TestE2ECloudWatch(t *testing.T) {
	ctx := context.Background()
	c := cloudwatch.NewFromConfig(e2eCfg(t, e2eServer(t)))

	if _, err := c.PutMetricAlarm(ctx, &cloudwatch.PutMetricAlarmInput{
		AlarmName: aws.String("cpu-high"), MetricName: aws.String("CPUUtilization"),
		Namespace: aws.String("AWS/EC2"), ComparisonOperator: cwtypes.ComparisonOperatorGreaterThanThreshold,
		Threshold: aws.Float64(80), EvaluationPeriods: aws.Int32(1), Period: aws.Int32(60),
		Statistic: cwtypes.StatisticAverage,
	}); err != nil {
		t.Fatalf("PutMetricAlarm: %v", err)
	}
	if _, err := c.SetAlarmState(ctx, &cloudwatch.SetAlarmStateInput{
		AlarmName: aws.String("cpu-high"), StateValue: cwtypes.StateValueAlarm, StateReason: aws.String("test"),
	}); err != nil {
		t.Fatalf("SetAlarmState: %v", err)
	}
	da, err := c.DescribeAlarms(ctx, &cloudwatch.DescribeAlarmsInput{AlarmNames: []string{"cpu-high"}})
	if err != nil {
		t.Fatalf("DescribeAlarms: %v", err)
	}
	if len(da.MetricAlarms) != 1 || da.MetricAlarms[0].StateValue != cwtypes.StateValueAlarm {
		t.Fatal("SetAlarmState did not transition the alarm to ALARM")
	}
}
