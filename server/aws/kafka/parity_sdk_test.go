package kafka_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awscw "github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	awskafka "github.com/aws/aws-sdk-go-v2/service/kafka"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"

	"github.com/stackshy/cloudemu/v2"
	cloudconfig "github.com/stackshy/cloudemu/v2/config"
	awsprovider "github.com/stackshy/cloudemu/v2/providers/aws"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// parityEnv is an in-process AWS server (Kafka + CloudWatch from one provider,
// so the AWS/Kafka metrics land in the CloudWatch the client reads) with real
// SDK clients, the FakeClock driving settle windows, and the provider whose
// Tickables the serve scheduler would run.
type parityEnv struct {
	kafka *awskafka.Client
	cw    *awscw.Client
	clock *cloudconfig.FakeClock
	cloud *awsprovider.Provider
}

// tick runs one round of the serve scheduler at the current FakeClock time.
func (e *parityEnv) tick() {
	for _, tk := range e.cloud.Tickables() {
		tk.Tick(e.clock.Now())
	}
}

func newParityEnv(t *testing.T, opts ...cloudconfig.Option) *parityEnv {
	t.Helper()

	c, cw, fc, cloud := newParityClientsFull(t, opts...)

	return &parityEnv{kafka: c, cw: cw, clock: fc, cloud: cloud}
}

func newParityClients(t *testing.T, opts ...cloudconfig.Option) (*awskafka.Client, *awscw.Client, *cloudconfig.FakeClock) {
	t.Helper()

	c, cw, fc, _ := newParityClientsFull(t, opts...)

	return c, cw, fc
}

func newParityClientsFull(
	t *testing.T, opts ...cloudconfig.Option,
) (*awskafka.Client, *awscw.Client, *cloudconfig.FakeClock, *awsprovider.Provider) {
	t.Helper()

	fc := cloudconfig.NewFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	cloud := cloudemu.NewAWS(append([]cloudconfig.Option{cloudconfig.WithClock(fc)}, opts...)...)
	srv := awsserver.New(awsserver.Drivers{Kafka: cloud.Kafka, CloudWatch: cloud.CloudWatch})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	cfg.BaseEndpoint = aws.String(ts.URL)

	return awskafka.NewFromConfig(cfg), awscw.NewFromConfig(cfg), fc, cloud
}

func createSDKCluster(ctx context.Context, t *testing.T, c *awskafka.Client, name string) string {
	t.Helper()

	out, err := c.CreateCluster(ctx, &awskafka.CreateClusterInput{
		ClusterName:         aws.String(name),
		KafkaVersion:        aws.String("3.6.0"),
		NumberOfBrokerNodes: aws.Int32(3),
		BrokerNodeGroupInfo: &kafkatypes.BrokerNodeGroupInfo{
			ClientSubnets: []string{"subnet-0a1", "subnet-0b2", "subnet-0c3"},
			InstanceType:  aws.String("kafka.m5.large"),
		},
	})
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	return aws.ToString(out.ClusterArn)
}

// TestSDKListNodesClientSubnet asserts ListNodes reports the cluster's own
// client subnets through the wire.
func TestSDKListNodesClientSubnet(t *testing.T) {
	ctx := context.Background()
	c, _, _ := newParityClients(t)
	arn := createSDKCluster(ctx, t, c, "sdk-subnets")

	out, err := c.ListNodes(ctx, &awskafka.ListNodesInput{ClusterArn: aws.String(arn)})
	if err != nil {
		t.Fatalf("ListNodes: %v", err)
	}

	want := []string{"subnet-0a1", "subnet-0b2", "subnet-0c3"}
	for i, n := range out.NodeInfoList {
		if got := aws.ToString(n.BrokerNodeInfo.ClientSubnet); got != want[i] {
			t.Fatalf("node %d clientSubnet = %q, want %q", i, got, want[i])
		}
	}
}

// TestSDKAsyncClusterLifecycle drives CREATING -> ACTIVE -> DELETING -> gone
// through the real SDK under AsyncSettle.
func TestSDKAsyncClusterLifecycle(t *testing.T) {
	ctx := context.Background()
	c, _, fc := newParityClients(t, cloudconfig.WithAsyncSettle())
	arn := createSDKCluster(ctx, t, c, "sdk-async")

	desc, err := c.DescribeCluster(ctx, &awskafka.DescribeClusterInput{ClusterArn: aws.String(arn)})
	if err != nil || desc.ClusterInfo.State != kafkatypes.ClusterStateCreating {
		t.Fatalf("describe while creating = %v/%v", desc, err)
	}

	_, err = c.UpdateMonitoring(ctx, &awskafka.UpdateMonitoringInput{
		ClusterArn: aws.String(arn), CurrentVersion: desc.ClusterInfo.CurrentVersion,
		EnhancedMonitoring: kafkatypes.EnhancedMonitoringPerBroker,
	})

	var br *kafkatypes.BadRequestException
	if !errors.As(err, &br) {
		t.Fatalf("update while CREATING = %v, want BadRequestException", err)
	}

	fc.Advance(10 * time.Second)

	v2, err := c.DescribeClusterV2(ctx, &awskafka.DescribeClusterV2Input{ClusterArn: aws.String(arn)})
	if err != nil || v2.ClusterInfo.State != kafkatypes.ClusterStateActive {
		t.Fatalf("describe v2 after settle = %v/%v", v2, err)
	}

	del, err := c.DeleteCluster(ctx, &awskafka.DeleteClusterInput{ClusterArn: aws.String(arn)})
	if err != nil || del.State != kafkatypes.ClusterStateDeleting {
		t.Fatalf("DeleteCluster = %v/%v", del, err)
	}

	desc, err = c.DescribeCluster(ctx, &awskafka.DescribeClusterInput{ClusterArn: aws.String(arn)})
	if err != nil || desc.ClusterInfo.State != kafkatypes.ClusterStateDeleting {
		t.Fatalf("describe while deleting = %v/%v", desc, err)
	}

	fc.Advance(10 * time.Second)

	_, err = c.DescribeCluster(ctx, &awskafka.DescribeClusterInput{ClusterArn: aws.String(arn)})

	var nf *kafkatypes.NotFoundException
	if !errors.As(err, &nf) {
		t.Fatalf("describe after delete = %v, want NotFoundException", err)
	}
}

// TestSDKAsyncVpcConnectionCreating asserts a VPC connection reports CREATING
// then AVAILABLE through the wire under AsyncSettle.
func TestSDKAsyncVpcConnectionCreating(t *testing.T) {
	ctx := context.Background()
	c, _, fc := newParityClients(t, cloudconfig.WithAsyncSettle())
	arn := createSDKCluster(ctx, t, c, "sdk-vpc")

	fc.Advance(10 * time.Second)

	v, err := c.CreateVpcConnection(ctx, &awskafka.CreateVpcConnectionInput{
		TargetClusterArn: aws.String(arn), Authentication: aws.String("SASL_IAM"),
		VpcId: aws.String("vpc-123"), ClientSubnets: []string{"subnet-x"}, SecurityGroups: []string{"sg-1"},
	})
	if err != nil || v.State != kafkatypes.VpcConnectionStateCreating {
		t.Fatalf("CreateVpcConnection = %v/%v, want CREATING", v, err)
	}

	fc.Advance(10 * time.Second)

	d, err := c.DescribeVpcConnection(ctx, &awskafka.DescribeVpcConnectionInput{Arn: v.VpcConnectionArn})
	if err != nil || d.State != kafkatypes.VpcConnectionStateAvailable {
		t.Fatalf("DescribeVpcConnection = %v/%v, want AVAILABLE", d, err)
	}
}

// TestSDKKafkaMetricsAndDefaultKey asserts the AWS/Kafka metrics are published
// by the serve scheduler (none on create, one sample per minute after),
// readable through the CloudWatch API under the documented dimensions, and the
// default at-rest key is a real-shaped KMS key ARN in the v1 and v2 shapes.
func TestSDKKafkaMetricsAndDefaultKey(t *testing.T) {
	ctx := context.Background()
	env := newParityEnv(t)
	c, cw := env.kafka, env.cw
	start := env.clock.Now()
	arn := createSDKCluster(ctx, t, c, "sdk-metrics")

	listed := func() []cwtypes.Metric {
		out, err := cw.ListMetrics(ctx, &awscw.ListMetricsInput{Namespace: aws.String("AWS/Kafka")})
		if err != nil {
			t.Fatalf("ListMetrics: %v", err)
		}

		return out.Metrics
	}

	if got := listed(); len(got) != 0 {
		t.Fatalf("metrics published by a request: %+v", got)
	}

	env.tick()
	env.clock.Advance(time.Minute)
	env.tick()

	var sawController, sawBroker bool

	for _, m := range listed() {
		dims := map[string]string{}
		for _, d := range m.Dimensions {
			dims[aws.ToString(d.Name)] = aws.ToString(d.Value)
		}

		if dims["Cluster Name"] != "sdk-metrics" {
			continue
		}

		switch aws.ToString(m.MetricName) {
		case "ActiveControllerCount":
			sawController = len(dims) == 1
		case "UnderReplicatedPartitions":
			sawBroker = sawBroker || dims["Broker ID"] != ""
		}
	}

	if !sawController || !sawBroker {
		t.Fatalf("AWS/Kafka metrics missing (controller=%v broker=%v)", sawController, sawBroker)
	}

	stats, err := cw.GetMetricStatistics(ctx, &awscw.GetMetricStatisticsInput{
		Namespace:  aws.String("AWS/Kafka"),
		MetricName: aws.String("ActiveControllerCount"),
		Dimensions: []cwtypes.Dimension{{Name: aws.String("Cluster Name"), Value: aws.String("sdk-metrics")}},
		StartTime:  aws.Time(start),
		EndTime:    aws.Time(env.clock.Now().Add(time.Minute)),
		Period:     aws.Int32(60),
		Statistics: []cwtypes.Statistic{cwtypes.StatisticSampleCount, cwtypes.StatisticMaximum},
	})
	if err != nil {
		t.Fatalf("GetMetricStatistics: %v", err)
	}

	if len(stats.Datapoints) != 2 {
		t.Fatalf("ActiveControllerCount datapoints = %+v, want 2 one-minute samples", stats.Datapoints)
	}

	for _, dp := range stats.Datapoints {
		if aws.ToFloat64(dp.SampleCount) != 1 || aws.ToFloat64(dp.Maximum) != 1 || dp.Unit != cwtypes.StandardUnitCount {
			t.Fatalf("datapoint = %+v, want one sample of 1 Count", dp)
		}
	}

	desc, err := c.DescribeCluster(ctx, &awskafka.DescribeClusterInput{ClusterArn: aws.String(arn)})
	if err != nil {
		t.Fatalf("DescribeCluster: %v", err)
	}

	key := aws.ToString(desc.ClusterInfo.EncryptionInfo.EncryptionAtRest.DataVolumeKMSKeyId)
	if !strings.HasPrefix(key, "arn:aws:kms:us-east-1:") || strings.Contains(key, "msk-default") {
		t.Fatalf("default key = %q", key)
	}

	v2, err := c.DescribeClusterV2(ctx, &awskafka.DescribeClusterV2Input{ClusterArn: aws.String(arn)})
	if err != nil {
		t.Fatalf("DescribeClusterV2: %v", err)
	}

	enc := v2.ClusterInfo.Provisioned.EncryptionInfo
	if enc == nil || aws.ToString(enc.EncryptionAtRest.DataVolumeKMSKeyId) != key {
		t.Fatalf("v2 provisioned encryptionInfo = %+v, want key %s", enc, key)
	}
}
