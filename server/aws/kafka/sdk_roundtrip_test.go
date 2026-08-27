package kafka_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awskafka "github.com/aws/aws-sdk-go-v2/service/kafka"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/aws/smithy-go"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

func newKafkaClient(t *testing.T) *awskafka.Client {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{Kafka: cloud.Kafka})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return awskafka.NewFromConfig(cfg, func(o *awskafka.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})
}

func TestSDKClusterLifecycle(t *testing.T) {
	ctx := context.Background()
	c := newKafkaClient(t)

	create, err := c.CreateCluster(ctx, &awskafka.CreateClusterInput{
		ClusterName:         aws.String("sdk-cluster"),
		KafkaVersion:        aws.String("3.6.0"),
		NumberOfBrokerNodes: aws.Int32(3),
		BrokerNodeGroupInfo: &kafkatypes.BrokerNodeGroupInfo{
			ClientSubnets: []string{"subnet-1", "subnet-2", "subnet-3"},
			InstanceType:  aws.String("kafka.m5.large"),
		},
		Tags: map[string]string{"env": "prod"},
	})
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	arn := aws.ToString(create.ClusterArn)
	if !strings.Contains(arn, ":kafka:") {
		t.Fatalf("unexpected ARN: %s", arn)
	}

	if create.State != kafkatypes.ClusterStateActive {
		t.Fatalf("state = %s, want ACTIVE", create.State)
	}

	desc, err := c.DescribeCluster(ctx, &awskafka.DescribeClusterInput{ClusterArn: aws.String(arn)})
	if err != nil {
		t.Fatalf("DescribeCluster: %v", err)
	}

	if aws.ToInt32(desc.ClusterInfo.NumberOfBrokerNodes) != 3 {
		t.Fatalf("broker nodes = %d, want 3", aws.ToInt32(desc.ClusterInfo.NumberOfBrokerNodes))
	}

	if desc.ClusterInfo.BrokerNodeGroupInfo == nil ||
		aws.ToString(desc.ClusterInfo.BrokerNodeGroupInfo.InstanceType) != "kafka.m5.large" {
		t.Fatalf("broker node group not reflected: %+v", desc.ClusterInfo.BrokerNodeGroupInfo)
	}

	brokers, err := c.GetBootstrapBrokers(ctx, &awskafka.GetBootstrapBrokersInput{ClusterArn: aws.String(arn)})
	if err != nil {
		t.Fatalf("GetBootstrapBrokers: %v", err)
	}

	if !strings.Contains(aws.ToString(brokers.BootstrapBrokerString), ":9092") {
		t.Fatalf("unexpected bootstrap brokers: %+v", brokers)
	}

	list, err := c.ListClusters(ctx, &awskafka.ListClustersInput{})
	if err != nil {
		t.Fatalf("ListClusters: %v", err)
	}

	if len(list.ClusterInfoList) != 1 {
		t.Fatalf("want 1 cluster, got %d", len(list.ClusterInfoList))
	}

	if _, err := c.DeleteCluster(ctx, &awskafka.DeleteClusterInput{ClusterArn: aws.String(arn)}); err != nil {
		t.Fatalf("DeleteCluster: %v", err)
	}

	list, _ = c.ListClusters(ctx, &awskafka.ListClustersInput{})
	if len(list.ClusterInfoList) != 0 {
		t.Fatalf("expected no clusters after delete, got %d", len(list.ClusterInfoList))
	}
}

func TestSDKDescribeClusterEncryptionDefault(t *testing.T) {
	ctx := context.Background()
	c := newKafkaClient(t)

	create, err := c.CreateCluster(ctx, &awskafka.CreateClusterInput{
		ClusterName:         aws.String("enc-cluster"),
		KafkaVersion:        aws.String("3.6.0"),
		NumberOfBrokerNodes: aws.Int32(3),
		BrokerNodeGroupInfo: &kafkatypes.BrokerNodeGroupInfo{
			ClientSubnets: []string{"subnet-1", "subnet-2", "subnet-3"},
			InstanceType:  aws.String("kafka.m5.large"),
		},
	})
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	desc, err := c.DescribeCluster(ctx, &awskafka.DescribeClusterInput{ClusterArn: create.ClusterArn})
	if err != nil {
		t.Fatalf("DescribeCluster: %v", err)
	}

	enc := desc.ClusterInfo.EncryptionInfo
	if enc == nil {
		t.Fatalf("EncryptionInfo nil, want default block")
	}

	if enc.EncryptionInTransit == nil || enc.EncryptionInTransit.ClientBroker != kafkatypes.ClientBrokerTls {
		t.Fatalf("EncryptionInTransit.ClientBroker = %+v, want TLS", enc.EncryptionInTransit)
	}

	if enc.EncryptionInTransit.InCluster == nil || !aws.ToBool(enc.EncryptionInTransit.InCluster) {
		t.Fatalf("EncryptionInTransit.InCluster = %v, want true", enc.EncryptionInTransit.InCluster)
	}

	if enc.EncryptionAtRest == nil || aws.ToString(enc.EncryptionAtRest.DataVolumeKMSKeyId) == "" {
		t.Fatalf("EncryptionAtRest.DataVolumeKMSKeyId empty, want a KMS key ARN")
	}
}

func TestSDKConfigurationLifecycle(t *testing.T) {
	ctx := context.Background()
	c := newKafkaClient(t)

	create, err := c.CreateConfiguration(ctx, &awskafka.CreateConfigurationInput{
		Name:             aws.String("sdk-config"),
		Description:      aws.String("rev1"),
		KafkaVersions:    []string{"3.6.0"},
		ServerProperties: []byte("auto.create.topics.enable=true"),
	})
	if err != nil {
		t.Fatalf("CreateConfiguration: %v", err)
	}

	arn := aws.ToString(create.Arn)
	if aws.ToInt64(create.LatestRevision.Revision) != 1 {
		t.Fatalf("first revision = %d, want 1", aws.ToInt64(create.LatestRevision.Revision))
	}

	desc, err := c.DescribeConfiguration(ctx, &awskafka.DescribeConfigurationInput{Arn: aws.String(arn)})
	if err != nil {
		t.Fatalf("DescribeConfiguration: %v", err)
	}

	if aws.ToString(desc.Name) != "sdk-config" {
		t.Fatalf("name = %s, want sdk-config", aws.ToString(desc.Name))
	}

	upd, err := c.UpdateConfiguration(ctx, &awskafka.UpdateConfigurationInput{
		Arn:              aws.String(arn),
		Description:      aws.String("rev2"),
		ServerProperties: []byte("num.partitions=3"),
	})
	if err != nil {
		t.Fatalf("UpdateConfiguration: %v", err)
	}

	if aws.ToInt64(upd.LatestRevision.Revision) != 2 {
		t.Fatalf("updated revision = %d, want 2", aws.ToInt64(upd.LatestRevision.Revision))
	}

	revs, err := c.ListConfigurationRevisions(ctx, &awskafka.ListConfigurationRevisionsInput{Arn: aws.String(arn)})
	if err != nil || len(revs.Revisions) != 2 {
		t.Fatalf("ListConfigurationRevisions: %v len=%d", err, len(revs.Revisions))
	}

	rev1, err := c.DescribeConfigurationRevision(ctx, &awskafka.DescribeConfigurationRevisionInput{
		Arn:      aws.String(arn),
		Revision: aws.Int64(1),
	})
	if err != nil {
		t.Fatalf("DescribeConfigurationRevision: %v", err)
	}

	if string(rev1.ServerProperties) != "auto.create.topics.enable=true" {
		t.Fatalf("rev1 server properties = %q", rev1.ServerProperties)
	}

	listed, err := c.ListConfigurations(ctx, &awskafka.ListConfigurationsInput{})
	if err != nil || len(listed.Configurations) != 1 {
		t.Fatalf("ListConfigurations: %v len=%d", err, len(listed.Configurations))
	}

	if _, err := c.DeleteConfiguration(ctx, &awskafka.DeleteConfigurationInput{Arn: aws.String(arn)}); err != nil {
		t.Fatalf("DeleteConfiguration: %v", err)
	}
}

// TestSDKDescribeMissingReturnsNotFound asserts the typed NotFoundException
// deserializes on the SDK side via errors.As.
func TestSDKDescribeMissingReturnsNotFound(t *testing.T) {
	ctx := context.Background()
	c := newKafkaClient(t)

	_, err := c.DescribeCluster(ctx, &awskafka.DescribeClusterInput{
		ClusterArn: aws.String("arn:aws:kafka:us-east-1:123456789012:cluster/missing/x"),
	})
	if err == nil {
		t.Fatal("expected error for missing cluster")
	}

	var nf *kafkatypes.NotFoundException
	if !errors.As(err, &nf) {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			t.Fatalf("want NotFoundException, got %q", apiErr.ErrorCode())
		}

		t.Fatalf("want NotFoundException, got %v", err)
	}
}

// TestSDKCreateDuplicateReturnsConflict asserts a duplicate cluster name
// deserializes as the typed ConflictException.
func TestSDKCreateDuplicateReturnsConflict(t *testing.T) {
	ctx := context.Background()
	c := newKafkaClient(t)

	in := &awskafka.CreateClusterInput{
		ClusterName:         aws.String("dup-cluster"),
		KafkaVersion:        aws.String("3.6.0"),
		NumberOfBrokerNodes: aws.Int32(3),
		BrokerNodeGroupInfo: &kafkatypes.BrokerNodeGroupInfo{
			ClientSubnets: []string{"subnet-1"},
			InstanceType:  aws.String("kafka.m5.large"),
		},
	}

	if _, err := c.CreateCluster(ctx, in); err != nil {
		t.Fatalf("first create: %v", err)
	}

	_, err := c.CreateCluster(ctx, in)
	if err == nil {
		t.Fatal("expected duplicate error")
	}

	var conflict *kafkatypes.ConflictException
	if !errors.As(err, &conflict) {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			t.Fatalf("want ConflictException, got %q", apiErr.ErrorCode())
		}

		t.Fatalf("want ConflictException, got %v", err)
	}
}

// TestSDKClusterV2CrossRead asserts a v1-created cluster is describable via the
// v2 op through the SDK, rendering the PROVISIONED nested shape.
func TestSDKClusterV2CrossRead(t *testing.T) {
	ctx := context.Background()
	c := newKafkaClient(t)

	create, err := c.CreateCluster(ctx, &awskafka.CreateClusterInput{
		ClusterName:         aws.String("v2-cross"),
		KafkaVersion:        aws.String("3.6.0"),
		NumberOfBrokerNodes: aws.Int32(3),
		BrokerNodeGroupInfo: &kafkatypes.BrokerNodeGroupInfo{
			ClientSubnets: []string{"subnet-1"},
			InstanceType:  aws.String("kafka.m5.large"),
		},
	})
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	arn := aws.ToString(create.ClusterArn)

	desc, err := c.DescribeClusterV2(ctx, &awskafka.DescribeClusterV2Input{ClusterArn: aws.String(arn)})
	if err != nil {
		t.Fatalf("DescribeClusterV2: %v", err)
	}

	if desc.ClusterInfo.ClusterType != kafkatypes.ClusterTypeProvisioned {
		t.Fatalf("clusterType = %s, want PROVISIONED", desc.ClusterInfo.ClusterType)
	}

	if desc.ClusterInfo.Provisioned == nil ||
		aws.ToInt32(desc.ClusterInfo.Provisioned.NumberOfBrokerNodes) != 3 {
		t.Fatalf("provisioned block wrong: %+v", desc.ClusterInfo.Provisioned)
	}

	listed, err := c.ListClustersV2(ctx, &awskafka.ListClustersV2Input{
		ClusterTypeFilter: aws.String("PROVISIONED"),
	})
	if err != nil || len(listed.ClusterInfoList) != 1 {
		t.Fatalf("ListClustersV2: %v len=%d", err, len(listed.ClusterInfoList))
	}
}

// TestSDKCreateClusterV2Serverless asserts a serverless cluster round-trips.
func TestSDKCreateClusterV2Serverless(t *testing.T) {
	ctx := context.Background()
	c := newKafkaClient(t)

	create, err := c.CreateClusterV2(ctx, &awskafka.CreateClusterV2Input{
		ClusterName: aws.String("sdk-serverless"),
		Serverless: &kafkatypes.ServerlessRequest{
			VpcConfigs: []kafkatypes.VpcConfig{{SubnetIds: []string{"subnet-1"}}},
		},
	})
	if err != nil {
		t.Fatalf("CreateClusterV2: %v", err)
	}

	if create.ClusterType != kafkatypes.ClusterTypeServerless {
		t.Fatalf("clusterType = %s, want SERVERLESS", create.ClusterType)
	}

	desc, err := c.DescribeClusterV2(ctx, &awskafka.DescribeClusterV2Input{ClusterArn: create.ClusterArn})
	if err != nil {
		t.Fatalf("DescribeClusterV2: %v", err)
	}

	if desc.ClusterInfo.Serverless == nil {
		t.Fatalf("serverless block missing: %+v", desc.ClusterInfo)
	}
}

// TestSDKUpdateAndOperations asserts an update mutates the cluster, records an
// operation listable/describable through the SDK, and Describe reflects it.
func TestSDKUpdateAndOperations(t *testing.T) {
	ctx := context.Background()
	c := newKafkaClient(t)

	create, err := c.CreateCluster(ctx, &awskafka.CreateClusterInput{
		ClusterName:         aws.String("sdk-update"),
		KafkaVersion:        aws.String("3.6.0"),
		NumberOfBrokerNodes: aws.Int32(3),
		BrokerNodeGroupInfo: &kafkatypes.BrokerNodeGroupInfo{
			ClientSubnets: []string{"subnet-1", "subnet-2", "subnet-3"},
			InstanceType:  aws.String("kafka.m5.large"),
		},
	})
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	arn := aws.ToString(create.ClusterArn)

	desc, _ := c.DescribeCluster(ctx, &awskafka.DescribeClusterInput{ClusterArn: aws.String(arn)})
	version := aws.ToString(desc.ClusterInfo.CurrentVersion)

	upd, err := c.UpdateBrokerCount(ctx, &awskafka.UpdateBrokerCountInput{
		ClusterArn:                aws.String(arn),
		CurrentVersion:            aws.String(version),
		TargetNumberOfBrokerNodes: aws.Int32(6),
	})
	if err != nil {
		t.Fatalf("UpdateBrokerCount: %v", err)
	}

	opARN := aws.ToString(upd.ClusterOperationArn)
	if opARN == "" {
		t.Fatal("no cluster operation ARN returned")
	}

	desc, _ = c.DescribeCluster(ctx, &awskafka.DescribeClusterInput{ClusterArn: aws.String(arn)})
	if aws.ToInt32(desc.ClusterInfo.NumberOfBrokerNodes) != 6 {
		t.Fatalf("broker count not reflected: %d", aws.ToInt32(desc.ClusterInfo.NumberOfBrokerNodes))
	}

	ops, err := c.ListClusterOperations(ctx, &awskafka.ListClusterOperationsInput{ClusterArn: aws.String(arn)})
	if err != nil || len(ops.ClusterOperationInfoList) != 1 {
		t.Fatalf("ListClusterOperations: %v len=%d", err, len(ops.ClusterOperationInfoList))
	}

	got, err := c.DescribeClusterOperation(ctx, &awskafka.DescribeClusterOperationInput{
		ClusterOperationArn: aws.String(opARN),
	})
	if err != nil || aws.ToString(got.ClusterOperationInfo.OperationType) != "UPDATE_BROKER_COUNT" {
		t.Fatalf("DescribeClusterOperation: %v %+v", err, got.ClusterOperationInfo)
	}

	// Stale version → BadRequestException.
	_, err = c.UpdateBrokerCount(ctx, &awskafka.UpdateBrokerCountInput{
		ClusterArn:                aws.String(arn),
		CurrentVersion:            aws.String("stale"),
		TargetNumberOfBrokerNodes: aws.Int32(9),
	})

	var bad *kafkatypes.BadRequestException
	if !errors.As(err, &bad) {
		t.Fatalf("want BadRequestException for stale version, got %v", err)
	}
}

// TestSDKListNodes asserts ListNodes returns one broker node per broker.
func TestSDKListNodes(t *testing.T) {
	ctx := context.Background()
	c := newKafkaClient(t)

	create, err := c.CreateCluster(ctx, &awskafka.CreateClusterInput{
		ClusterName:         aws.String("sdk-nodes"),
		KafkaVersion:        aws.String("3.6.0"),
		NumberOfBrokerNodes: aws.Int32(3),
		BrokerNodeGroupInfo: &kafkatypes.BrokerNodeGroupInfo{
			ClientSubnets: []string{"subnet-1"},
			InstanceType:  aws.String("kafka.m5.large"),
		},
	})
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	nodes, err := c.ListNodes(ctx, &awskafka.ListNodesInput{ClusterArn: create.ClusterArn})
	if err != nil || len(nodes.NodeInfoList) != 3 {
		t.Fatalf("ListNodes: %v len=%d", err, len(nodes.NodeInfoList))
	}

	if nodes.NodeInfoList[0].BrokerNodeInfo == nil {
		t.Fatalf("broker node info missing: %+v", nodes.NodeInfoList[0])
	}
}

// TestSDKKafkaVersions asserts the version list and compatible-version lookup.
func TestSDKKafkaVersions(t *testing.T) {
	ctx := context.Background()
	c := newKafkaClient(t)

	vers, err := c.ListKafkaVersions(ctx, &awskafka.ListKafkaVersionsInput{})
	if err != nil || len(vers.KafkaVersions) == 0 {
		t.Fatalf("ListKafkaVersions: %v len=%d", err, len(vers.KafkaVersions))
	}

	comp, err := c.GetCompatibleKafkaVersions(ctx, &awskafka.GetCompatibleKafkaVersionsInput{})
	if err != nil || len(comp.CompatibleKafkaVersions) == 0 {
		t.Fatalf("GetCompatibleKafkaVersions: %v len=%d", err, len(comp.CompatibleKafkaVersions))
	}
}

// TestSDKTagLifecycle asserts tag → list → untag on a cluster ARN via the SDK.
func TestSDKTagLifecycle(t *testing.T) {
	ctx := context.Background()
	c := newKafkaClient(t)

	create, err := c.CreateCluster(ctx, &awskafka.CreateClusterInput{
		ClusterName:         aws.String("sdk-tags"),
		KafkaVersion:        aws.String("3.6.0"),
		NumberOfBrokerNodes: aws.Int32(3),
		BrokerNodeGroupInfo: &kafkatypes.BrokerNodeGroupInfo{
			ClientSubnets: []string{"subnet-1"},
			InstanceType:  aws.String("kafka.m5.large"),
		},
	})
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	arn := aws.ToString(create.ClusterArn)

	if _, err := c.TagResource(ctx, &awskafka.TagResourceInput{
		ResourceArn: aws.String(arn),
		Tags:        map[string]string{"team": "data"},
	}); err != nil {
		t.Fatalf("TagResource: %v", err)
	}

	tags, err := c.ListTagsForResource(ctx, &awskafka.ListTagsForResourceInput{ResourceArn: aws.String(arn)})
	if err != nil || tags.Tags["team"] != "data" {
		t.Fatalf("ListTagsForResource: %v %+v", err, tags.Tags)
	}

	if _, err := c.UntagResource(ctx, &awskafka.UntagResourceInput{
		ResourceArn: aws.String(arn),
		TagKeys:     []string{"team"},
	}); err != nil {
		t.Fatalf("UntagResource: %v", err)
	}

	tags, _ = c.ListTagsForResource(ctx, &awskafka.ListTagsForResourceInput{ResourceArn: aws.String(arn)})
	if _, ok := tags.Tags["team"]; ok {
		t.Fatalf("tag not removed: %+v", tags.Tags)
	}
}

// sdkCluster creates a cluster through the SDK and returns its ARN.
func sdkCluster(t *testing.T, ctx context.Context, c *awskafka.Client, name string) string {
	t.Helper()

	create, err := c.CreateCluster(ctx, &awskafka.CreateClusterInput{
		ClusterName:         aws.String(name),
		KafkaVersion:        aws.String("3.6.0"),
		NumberOfBrokerNodes: aws.Int32(3),
		BrokerNodeGroupInfo: &kafkatypes.BrokerNodeGroupInfo{
			ClientSubnets: []string{"subnet-1"},
			InstanceType:  aws.String("kafka.m5.large"),
		},
	})
	if err != nil {
		t.Fatalf("CreateCluster(%s): %v", name, err)
	}

	return aws.ToString(create.ClusterArn)
}

// TestSDKVpcConnectionLifecycle round-trips a VPC connection through the SDK.
func TestSDKVpcConnectionLifecycle(t *testing.T) {
	ctx := context.Background()
	c := newKafkaClient(t)
	arn := sdkCluster(t, ctx, c, "sdk-vpc")

	created, err := c.CreateVpcConnection(ctx, &awskafka.CreateVpcConnectionInput{
		TargetClusterArn: aws.String(arn),
		Authentication:   aws.String("SASL_IAM"),
		VpcId:            aws.String("vpc-1"),
		ClientSubnets:    []string{"subnet-1", "subnet-2"},
		SecurityGroups:   []string{"sg-1"},
	})
	if err != nil {
		t.Fatalf("CreateVpcConnection: %v", err)
	}

	vpcArn := aws.ToString(created.VpcConnectionArn)

	desc, err := c.DescribeVpcConnection(ctx, &awskafka.DescribeVpcConnectionInput{Arn: aws.String(vpcArn)})
	if err != nil || aws.ToString(desc.VpcId) != "vpc-1" {
		t.Fatalf("DescribeVpcConnection: %v %+v", err, desc)
	}

	clients, err := c.ListClientVpcConnections(ctx, &awskafka.ListClientVpcConnectionsInput{
		ClusterArn: aws.String(arn),
	})
	if err != nil || len(clients.ClientVpcConnections) != 1 {
		t.Fatalf("ListClientVpcConnections: %v len=%d", err, len(clients.ClientVpcConnections))
	}

	if _, err := c.RejectClientVpcConnection(ctx, &awskafka.RejectClientVpcConnectionInput{
		ClusterArn:       aws.String(arn),
		VpcConnectionArn: aws.String(vpcArn),
	}); err != nil {
		t.Fatalf("RejectClientVpcConnection: %v", err)
	}

	desc, _ = c.DescribeVpcConnection(ctx, &awskafka.DescribeVpcConnectionInput{Arn: aws.String(vpcArn)})
	if desc.State != kafkatypes.VpcConnectionStateRejected {
		t.Fatalf("state = %s, want REJECTED", desc.State)
	}

	if _, err := c.DeleteVpcConnection(ctx, &awskafka.DeleteVpcConnectionInput{Arn: aws.String(vpcArn)}); err != nil {
		t.Fatalf("DeleteVpcConnection: %v", err)
	}
}

// TestSDKTopicLifecycle round-trips topic management through the SDK.
func TestSDKTopicLifecycle(t *testing.T) {
	ctx := context.Background()
	c := newKafkaClient(t)
	arn := sdkCluster(t, ctx, c, "sdk-topic")

	if _, err := c.CreateTopic(ctx, &awskafka.CreateTopicInput{
		ClusterArn:        aws.String(arn),
		TopicName:         aws.String("events"),
		PartitionCount:    aws.Int32(6),
		ReplicationFactor: aws.Int32(3),
		Configs:           aws.String("retention.ms=1000"),
	}); err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}

	// Duplicate → Conflict.
	_, err := c.CreateTopic(ctx, &awskafka.CreateTopicInput{
		ClusterArn: aws.String(arn), TopicName: aws.String("events"),
		PartitionCount: aws.Int32(6), ReplicationFactor: aws.Int32(3),
	})

	var conflict *kafkatypes.ConflictException
	if !errors.As(err, &conflict) {
		t.Fatalf("want ConflictException for dup topic, got %v", err)
	}

	desc, err := c.DescribeTopic(ctx, &awskafka.DescribeTopicInput{
		ClusterArn: aws.String(arn), TopicName: aws.String("events"),
	})
	if err != nil || aws.ToInt32(desc.PartitionCount) != 6 {
		t.Fatalf("DescribeTopic: %v %+v", err, desc)
	}

	if aws.ToString(desc.Configs) != "retention.ms=1000" {
		t.Fatalf("configs = %q", aws.ToString(desc.Configs))
	}

	topics, err := c.ListTopics(ctx, &awskafka.ListTopicsInput{ClusterArn: aws.String(arn)})
	if err != nil || len(topics.Topics) != 1 {
		t.Fatalf("ListTopics: %v len=%d", err, len(topics.Topics))
	}

	parts, err := c.DescribeTopicPartitions(ctx, &awskafka.DescribeTopicPartitionsInput{
		ClusterArn: aws.String(arn), TopicName: aws.String("events"),
	})
	if err != nil || len(parts.Partitions) != 6 {
		t.Fatalf("DescribeTopicPartitions: %v len=%d", err, len(parts.Partitions))
	}

	if _, err := c.DeleteTopic(ctx, &awskafka.DeleteTopicInput{
		ClusterArn: aws.String(arn), TopicName: aws.String("events"),
	}); err != nil {
		t.Fatalf("DeleteTopic: %v", err)
	}
}

// TestSDKScramSecrets round-trips SCRAM secret association through the SDK.
func TestSDKScramSecrets(t *testing.T) {
	ctx := context.Background()
	c := newKafkaClient(t)
	arn := sdkCluster(t, ctx, c, "sdk-scram")

	good := "arn:aws:secretsmanager:us-east-1:1:secret:AmazonMSK_ok"

	assoc, err := c.BatchAssociateScramSecret(ctx, &awskafka.BatchAssociateScramSecretInput{
		ClusterArn:    aws.String(arn),
		SecretArnList: []string{good, "bad-arn"},
	})
	if err != nil || len(assoc.UnprocessedScramSecrets) != 1 {
		t.Fatalf("BatchAssociateScramSecret: %v unproc=%+v", err, assoc.UnprocessedScramSecrets)
	}

	if aws.ToString(assoc.UnprocessedScramSecrets[0].SecretArn) != "bad-arn" {
		t.Fatalf("unprocessed does not name the bad ARN: %+v", assoc.UnprocessedScramSecrets[0])
	}

	list, err := c.ListScramSecrets(ctx, &awskafka.ListScramSecretsInput{ClusterArn: aws.String(arn)})
	if err != nil || len(list.SecretArnList) != 1 {
		t.Fatalf("ListScramSecrets: %v %+v", err, list.SecretArnList)
	}

	if _, err := c.BatchDisassociateScramSecret(ctx, &awskafka.BatchDisassociateScramSecretInput{
		ClusterArn: aws.String(arn), SecretArnList: []string{good},
	}); err != nil {
		t.Fatalf("BatchDisassociateScramSecret: %v", err)
	}
}

// TestSDKClusterPolicy round-trips a cluster policy with a version-mismatch check.
func TestSDKClusterPolicy(t *testing.T) {
	ctx := context.Background()
	c := newKafkaClient(t)
	arn := sdkCluster(t, ctx, c, "sdk-policy")

	put, err := c.PutClusterPolicy(ctx, &awskafka.PutClusterPolicyInput{
		ClusterArn: aws.String(arn),
		Policy:     aws.String(`{"Version":"2012-10-17"}`),
	})
	if err != nil || aws.ToString(put.CurrentVersion) == "" {
		t.Fatalf("PutClusterPolicy: %v %+v", err, put)
	}

	get, err := c.GetClusterPolicy(ctx, &awskafka.GetClusterPolicyInput{ClusterArn: aws.String(arn)})
	if err != nil || aws.ToString(get.Policy) == "" {
		t.Fatalf("GetClusterPolicy: %v %+v", err, get)
	}

	// Version mismatch → BadRequest.
	_, err = c.PutClusterPolicy(ctx, &awskafka.PutClusterPolicyInput{
		ClusterArn: aws.String(arn), Policy: aws.String("{}"), CurrentVersion: aws.String("stale"),
	})

	var bad *kafkatypes.BadRequestException
	if !errors.As(err, &bad) {
		t.Fatalf("want BadRequestException for stale policy version, got %v", err)
	}

	if _, err := c.DeleteClusterPolicy(ctx, &awskafka.DeleteClusterPolicyInput{
		ClusterArn: aws.String(arn),
	}); err != nil {
		t.Fatalf("DeleteClusterPolicy: %v", err)
	}
}

// TestSDKReplicatorLifecycle round-trips a replicator through the SDK.
func TestSDKReplicatorLifecycle(t *testing.T) {
	ctx := context.Background()
	c := newKafkaClient(t)

	in := &awskafka.CreateReplicatorInput{
		ReplicatorName:          aws.String("sdk-repl"),
		ServiceExecutionRoleArn: aws.String("arn:aws:iam::1:role/r"),
		KafkaClusters: []kafkatypes.KafkaCluster{{
			AmazonMskCluster: &kafkatypes.AmazonMskCluster{MskClusterArn: aws.String("arn:a")},
			VpcConfig:        &kafkatypes.KafkaClusterClientVpcConfig{SubnetIds: []string{"subnet-1"}},
		}},
		ReplicationInfoList: []kafkatypes.ReplicationInfo{{
			SourceKafkaClusterArn:    aws.String("arn:a"),
			TargetKafkaClusterArn:    aws.String("arn:b"),
			TargetCompressionType:    kafkatypes.TargetCompressionTypeNone,
			TopicReplication:         &kafkatypes.TopicReplication{TopicsToReplicate: []string{".*"}},
			ConsumerGroupReplication: &kafkatypes.ConsumerGroupReplication{ConsumerGroupsToReplicate: []string{".*"}},
		}},
	}

	created, err := c.CreateReplicator(ctx, in)
	if err != nil || created.ReplicatorState != kafkatypes.ReplicatorStateRunning {
		t.Fatalf("CreateReplicator: %v %+v", err, created)
	}

	replArn := aws.ToString(created.ReplicatorArn)

	// Duplicate name → Conflict.
	_, err = c.CreateReplicator(ctx, in)

	var conflict *kafkatypes.ConflictException
	if !errors.As(err, &conflict) {
		t.Fatalf("want ConflictException for dup replicator, got %v", err)
	}

	desc, err := c.DescribeReplicator(ctx, &awskafka.DescribeReplicatorInput{
		ReplicatorArn: aws.String(replArn),
	})
	if err != nil || aws.ToString(desc.ReplicatorName) != "sdk-repl" {
		t.Fatalf("DescribeReplicator: %v %+v", err, desc)
	}

	list, err := c.ListReplicators(ctx, &awskafka.ListReplicatorsInput{})
	if err != nil || len(list.Replicators) != 1 {
		t.Fatalf("ListReplicators: %v len=%d", err, len(list.Replicators))
	}

	if _, err := c.DeleteReplicator(ctx, &awskafka.DeleteReplicatorInput{
		ReplicatorArn: aws.String(replArn),
	}); err != nil {
		t.Fatalf("DeleteReplicator: %v", err)
	}
}

// createStorageCluster creates a provisioned cluster whose broker EBS volume is
// sized to volumeGB, returning the SDK client and the cluster ARN.
func createStorageCluster(t *testing.T, name string, volumeGB int32) (*awskafka.Client, string) {
	t.Helper()

	ctx := context.Background()
	c := newKafkaClient(t)

	create, err := c.CreateCluster(ctx, &awskafka.CreateClusterInput{
		ClusterName:         aws.String(name),
		KafkaVersion:        aws.String("3.6.0"),
		NumberOfBrokerNodes: aws.Int32(3),
		BrokerNodeGroupInfo: &kafkatypes.BrokerNodeGroupInfo{
			ClientSubnets: []string{"subnet-1", "subnet-2", "subnet-3"},
			InstanceType:  aws.String("kafka.m5.large"),
			StorageInfo: &kafkatypes.StorageInfo{
				EbsStorageInfo: &kafkatypes.EBSStorageInfo{VolumeSize: aws.Int32(volumeGB)},
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	return c, aws.ToString(create.ClusterArn)
}

func clusterVersion(t *testing.T, c *awskafka.Client, arn string) string {
	t.Helper()

	desc, err := c.DescribeCluster(context.Background(), &awskafka.DescribeClusterInput{ClusterArn: aws.String(arn)})
	if err != nil {
		t.Fatalf("DescribeCluster: %v", err)
	}

	return aws.ToString(desc.ClusterInfo.CurrentVersion)
}

// TestSDKUpdateBrokerStorageReflectedInDescribe reproduces the Terraform
// storage-drift bug: after UpdateBrokerStorage, DescribeCluster must return the
// new EBS volume size (not the stale create-time value).
func TestSDKUpdateBrokerStorageReflectedInDescribe(t *testing.T) {
	ctx := context.Background()
	c, arn := createStorageCluster(t, "sdk-storage", 100)

	if _, err := c.UpdateBrokerStorage(ctx, &awskafka.UpdateBrokerStorageInput{
		ClusterArn:     aws.String(arn),
		CurrentVersion: aws.String(clusterVersion(t, c, arn)),
		TargetBrokerEBSVolumeInfo: []kafkatypes.BrokerEBSVolumeInfo{{
			KafkaBrokerNodeId: aws.String("ALL"),
			VolumeSizeGB:      aws.Int32(200),
		}},
	}); err != nil {
		t.Fatalf("UpdateBrokerStorage: %v", err)
	}

	desc, err := c.DescribeCluster(ctx, &awskafka.DescribeClusterInput{ClusterArn: aws.String(arn)})
	if err != nil {
		t.Fatalf("DescribeCluster: %v", err)
	}

	bng := desc.ClusterInfo.BrokerNodeGroupInfo
	if bng == nil || bng.StorageInfo == nil || bng.StorageInfo.EbsStorageInfo == nil {
		t.Fatalf("storage info missing: %+v", bng)
	}

	if got := aws.ToInt32(bng.StorageInfo.EbsStorageInfo.VolumeSize); got != 200 {
		t.Fatalf("volume size = %d, want 200 (storage drift)", got)
	}
}

// TestSDKDescribeClusterZookeeperConnect asserts a provisioned cluster exports a
// non-empty ZookeeperConnectString (+ Tls) that Terraform's aws_msk_cluster reads.
func TestSDKDescribeClusterZookeeperConnect(t *testing.T) {
	ctx := context.Background()
	c, arn := createStorageCluster(t, "sdk-zk", 100)

	desc, err := c.DescribeCluster(ctx, &awskafka.DescribeClusterInput{ClusterArn: aws.String(arn)})
	if err != nil {
		t.Fatalf("DescribeCluster: %v", err)
	}

	zk := aws.ToString(desc.ClusterInfo.ZookeeperConnectString)
	if !strings.Contains(zk, ":2181") {
		t.Fatalf("ZookeeperConnectString = %q, want a plausible :2181 endpoint", zk)
	}

	if !strings.Contains(aws.ToString(desc.ClusterInfo.ZookeeperConnectStringTls), ":2182") {
		t.Fatalf("ZookeeperConnectStringTls = %q, want :2182",
			aws.ToString(desc.ClusterInfo.ZookeeperConnectStringTls))
	}
}

// TestSDKMissingClusterBadRequest asserts GetBootstrapBrokers and
// ListClusterOperations (v1) return the SDK-typed BadRequestException for a
// well-formed but missing cluster ARN. The aws-sdk-go-v2 smithy model does NOT
// model NotFoundException for these two ops, so returning a 404 would only
// deserialize as an untyped generic error — BadRequest is the correct,
// typed-deserializable behavior. ListClusterOperationsV2 does model 404.
func TestSDKMissingClusterBadRequest(t *testing.T) {
	ctx := context.Background()
	c := newKafkaClient(t)
	missing := aws.String("arn:aws:kafka:us-east-1:123456789012:cluster/missing/x")

	_, err := c.GetBootstrapBrokers(ctx, &awskafka.GetBootstrapBrokersInput{ClusterArn: missing})

	var bad *kafkatypes.BadRequestException
	if !errors.As(err, &bad) {
		t.Fatalf("GetBootstrapBrokers missing = %v, want BadRequestException", err)
	}

	_, err = c.ListClusterOperations(ctx, &awskafka.ListClusterOperationsInput{ClusterArn: missing})
	if !errors.As(err, &bad) {
		t.Fatalf("ListClusterOperations missing = %v, want BadRequestException", err)
	}
}

// TestSDKOperationSourceTargetDeltas asserts a broker-count update and a storage
// update each record an operation carrying the before/after source/target delta.
func TestSDKOperationSourceTargetDeltas(t *testing.T) {
	ctx := context.Background()
	c, arn := createStorageCluster(t, "sdk-deltas", 100)

	if _, err := c.UpdateBrokerCount(ctx, &awskafka.UpdateBrokerCountInput{
		ClusterArn:                aws.String(arn),
		CurrentVersion:            aws.String(clusterVersion(t, c, arn)),
		TargetNumberOfBrokerNodes: aws.Int32(6),
	}); err != nil {
		t.Fatalf("UpdateBrokerCount: %v", err)
	}

	if _, err := c.UpdateBrokerStorage(ctx, &awskafka.UpdateBrokerStorageInput{
		ClusterArn:     aws.String(arn),
		CurrentVersion: aws.String(clusterVersion(t, c, arn)),
		TargetBrokerEBSVolumeInfo: []kafkatypes.BrokerEBSVolumeInfo{{
			KafkaBrokerNodeId: aws.String("ALL"),
			VolumeSizeGB:      aws.Int32(200),
		}},
	}); err != nil {
		t.Fatalf("UpdateBrokerStorage: %v", err)
	}

	ops, err := c.ListClusterOperations(ctx, &awskafka.ListClusterOperationsInput{ClusterArn: aws.String(arn)})
	if err != nil || len(ops.ClusterOperationInfoList) != 2 {
		t.Fatalf("ListClusterOperations: %v len=%d", err, len(ops.ClusterOperationInfoList))
	}

	assertBrokerCountDelta(t, ops.ClusterOperationInfoList[0])
	assertStorageDelta(t, ops.ClusterOperationInfoList[1])
}

func assertBrokerCountDelta(t *testing.T, op kafkatypes.ClusterOperationInfo) {
	t.Helper()

	if op.SourceClusterInfo == nil || aws.ToInt32(op.SourceClusterInfo.NumberOfBrokerNodes) != 3 {
		t.Fatalf("broker-count source = %+v, want 3", op.SourceClusterInfo)
	}

	if op.TargetClusterInfo == nil || aws.ToInt32(op.TargetClusterInfo.NumberOfBrokerNodes) != 6 {
		t.Fatalf("broker-count target = %+v, want 6", op.TargetClusterInfo)
	}
}

func assertStorageDelta(t *testing.T, op kafkatypes.ClusterOperationInfo) {
	t.Helper()

	if op.SourceClusterInfo == nil || volumeOf(op.SourceClusterInfo) != 100 {
		t.Fatalf("storage source volume = %+v, want 100", op.SourceClusterInfo)
	}

	if op.TargetClusterInfo == nil || volumeOf(op.TargetClusterInfo) != 200 {
		t.Fatalf("storage target volume = %+v, want 200", op.TargetClusterInfo)
	}
}

func volumeOf(mi *kafkatypes.MutableClusterInfo) int32 {
	if len(mi.BrokerEBSVolumeInfo) == 0 {
		return -1
	}

	return aws.ToInt32(mi.BrokerEBSVolumeInfo[0].VolumeSizeGB)
}
