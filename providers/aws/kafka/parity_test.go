package kafka_test

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/cloudwatch"
	"github.com/stackshy/cloudemu/v2/providers/aws/kafka"
	"github.com/stackshy/cloudemu/v2/services/kafka/driver"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// settleWindow comfortably covers the provider's settle windows.
const settleWindow = 10 * time.Second

func newSettleMock(t *testing.T) (*kafka.Mock, *config.FakeClock) {
	t.Helper()

	fc := config.NewFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))

	return kafka.New(config.NewOptions(config.WithClock(fc), config.WithAsyncSettle())), fc
}

func createThreeAZCluster(t *testing.T, m *kafka.Mock, name string) *driver.Cluster {
	t.Helper()

	c, err := m.CreateCluster(context.Background(), driver.CreateClusterInput{
		ClusterName:         name,
		NumberOfBrokerNodes: 6,
		BrokerNodeGroupInfo: &driver.BrokerNodeGroupInfo{
			ClientSubnets: []string{"subnet-aaa", "subnet-bbb", "subnet-ccc"},
			InstanceType:  "kafka.m5.large",
		},
	})
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	return c
}

func wantException(t *testing.T, err error, ex string) {
	t.Helper()

	var apiErr *driver.APIError
	if !errors.As(err, &apiErr) || apiErr.Exception != ex {
		t.Fatalf("want %s, got %v", ex, err)
	}
}

// TestListNodesClientSubnetFromCluster asserts each broker's clientSubnet is
// one of the cluster's ClientSubnets, distributed evenly (round-robin) the way
// MSK spreads brokers across AZs, not a fabricated subnet-<brokerId>.
func TestListNodesClientSubnetFromCluster(t *testing.T) {
	m := newMock(t)
	c := createThreeAZCluster(t, m, "subnets")

	nodes, _, err := m.ListNodes(context.Background(), c.ClusterARN, driver.Page{})
	if err != nil || len(nodes) != 6 {
		t.Fatalf("ListNodes: %v len=%d", err, len(nodes))
	}

	want := []string{"subnet-aaa", "subnet-bbb", "subnet-ccc", "subnet-aaa", "subnet-bbb", "subnet-ccc"}

	for i, n := range nodes {
		var info struct {
			BrokerID     int    `json:"brokerId"`
			ClientSubnet string `json:"clientSubnet"`
		}

		if err := json.Unmarshal(n.RawOptions["brokerNodeInfo"], &info); err != nil {
			t.Fatalf("decode brokerNodeInfo: %v", err)
		}

		if info.BrokerID != i+1 || info.ClientSubnet != want[i] {
			t.Fatalf("node %d: brokerId=%d clientSubnet=%q, want %d/%q", i, info.BrokerID, info.ClientSubnet, i+1, want[i])
		}
	}
}

// TestListNodesNoSubnetsOmitsClientSubnet asserts a cluster with no recorded
// client subnets does not get a fabricated one.
func TestListNodesNoSubnetsOmitsClientSubnet(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	c, _ := m.CreateCluster(ctx, driver.CreateClusterInput{ClusterName: "nosubnet", NumberOfBrokerNodes: 2, BrokerNodeGroupInfo: bng()})

	nodes, _, err := m.ListNodes(ctx, c.ClusterARN, driver.Page{})
	if err != nil {
		t.Fatalf("ListNodes: %v", err)
	}

	if strings.Contains(string(nodes[0].RawOptions["brokerNodeInfo"]), "clientSubnet") {
		t.Fatalf("clientSubnet fabricated: %s", nodes[0].RawOptions["brokerNodeInfo"])
	}
}

// TestClusterCreatingThenActive asserts that under AsyncSettle a new cluster is
// CREATING (create response, Describe, List) and rejects updates, then settles
// to ACTIVE.
func TestClusterCreatingThenActive(t *testing.T) {
	m, fc := newSettleMock(t)
	ctx := context.Background()

	c := createThreeAZCluster(t, m, "settling")
	if c.State != driver.ClusterStateCreating {
		t.Fatalf("create state = %s, want CREATING", c.State)
	}

	d, err := m.DescribeCluster(ctx, c.ClusterARN)
	if err != nil || d.State != driver.ClusterStateCreating {
		t.Fatalf("describe = %v/%v, want CREATING", d, err)
	}

	list, _, _ := m.ListClustersV2(ctx, "", "", driver.Page{})
	if len(list) != 1 || list[0].State != driver.ClusterStateCreating {
		t.Fatalf("list = %+v, want one CREATING", list)
	}

	_, err = m.UpdateBrokerCount(ctx, c.ClusterARN, c.CurrentVersion, 9)
	wantException(t, err, driver.ExBadRequest)

	fc.Advance(settleWindow)

	d, _ = m.DescribeCluster(ctx, c.ClusterARN)
	if d.State != driver.ClusterStateActive {
		t.Fatalf("after settle state = %s, want ACTIVE", d.State)
	}

	if _, err := m.UpdateBrokerCount(ctx, c.ClusterARN, d.CurrentVersion, 9); err != nil {
		t.Fatalf("UpdateBrokerCount after ACTIVE: %v", err)
	}
}

// TestClusterActiveImmediatelyWithoutSettle pins the default behavior.
func TestClusterActiveImmediatelyWithoutSettle(t *testing.T) {
	m := newMock(t)

	c := createThreeAZCluster(t, m, "instant")
	if c.State != driver.ClusterStateActive {
		t.Fatalf("state = %s, want ACTIVE", c.State)
	}
}

// TestDeleteClusterDeletingThenGone asserts that under AsyncSettle a deleted
// cluster stays visible as DELETING (Describe and List), keeps its name, and
// is gone once the window elapses.
func TestDeleteClusterDeletingThenGone(t *testing.T) {
	m, fc := newSettleMock(t)
	ctx := context.Background()

	c := createThreeAZCluster(t, m, "doomed")
	fc.Advance(settleWindow)

	_, state, err := m.DeleteCluster(ctx, c.ClusterARN, "")
	if err != nil || state != driver.ClusterStateDeleting {
		t.Fatalf("DeleteCluster = %s/%v", state, err)
	}

	d, err := m.DescribeCluster(ctx, c.ClusterARN)
	if err != nil || d.State != driver.ClusterStateDeleting {
		t.Fatalf("describe while deleting = %+v/%v, want DELETING", d, err)
	}

	list, _, _ := m.ListClusters(ctx, "", driver.Page{})
	if len(list) != 1 || list[0].State != driver.ClusterStateDeleting {
		t.Fatalf("list while deleting = %+v", list)
	}

	// A second delete is idempotent; updates are rejected.
	if _, state, err = m.DeleteCluster(ctx, c.ClusterARN, ""); err != nil || state != driver.ClusterStateDeleting {
		t.Fatalf("second DeleteCluster = %s/%v", state, err)
	}

	_, err = m.UpdateMonitoring(ctx, c.ClusterARN, "", json.RawMessage(`{"enhancedMonitoring":"PER_BROKER"}`))
	wantException(t, err, driver.ExBadRequest)

	// The name is still held while the cluster is DELETING.
	_, err = m.CreateCluster(ctx, driver.CreateClusterInput{ClusterName: "doomed", NumberOfBrokerNodes: 3, BrokerNodeGroupInfo: bng()})
	wantException(t, err, driver.ExConflict)

	fc.Advance(settleWindow)

	_, err = m.DescribeCluster(ctx, c.ClusterARN)
	wantException(t, err, driver.ExNotFound)

	if list, _, _ = m.ListClusters(ctx, "", driver.Page{}); len(list) != 0 {
		t.Fatalf("list after delete = %+v", list)
	}

	if _, err := m.CreateCluster(ctx, driver.CreateClusterInput{ClusterName: "doomed", NumberOfBrokerNodes: 3, BrokerNodeGroupInfo: bng()}); err != nil {
		t.Fatalf("name not freed after delete: %v", err)
	}
}

// TestDeleteFreesNameForCreateWithoutRead asserts a create reclaims the name of
// a cluster whose delete window has elapsed even if nothing read it since.
func TestDeleteFreesNameForCreateWithoutRead(t *testing.T) {
	m, fc := newSettleMock(t)
	ctx := context.Background()

	c := createThreeAZCluster(t, m, "reuse")
	fc.Advance(settleWindow)

	if _, _, err := m.DeleteCluster(ctx, c.ClusterARN, ""); err != nil {
		t.Fatalf("DeleteCluster: %v", err)
	}

	fc.Advance(settleWindow)

	if _, err := m.CreateCluster(ctx, driver.CreateClusterInput{ClusterName: "reuse", NumberOfBrokerNodes: 3, BrokerNodeGroupInfo: bng()}); err != nil {
		t.Fatalf("CreateCluster reusing name: %v", err)
	}
}

// TestVpcConnectionCreatingThenAvailable asserts CREATING (the real transient
// VpcConnectionState; there is no PENDING) under AsyncSettle, then AVAILABLE.
func TestVpcConnectionCreatingThenAvailable(t *testing.T) {
	m, fc := newSettleMock(t)
	ctx := context.Background()

	c := createThreeAZCluster(t, m, "vpc-target")
	fc.Advance(settleWindow)

	body := json.RawMessage(`{"targetClusterArn":"` + c.ClusterARN +
		`","authentication":"SASL_IAM","vpcId":"vpc-1","clientSubnets":["subnet-x"],"securityGroups":["sg-1"]}`)

	v, err := m.CreateVpcConnection(ctx, body)
	if err != nil || v.State != "CREATING" {
		t.Fatalf("CreateVpcConnection = %+v/%v, want CREATING", v, err)
	}

	d, _ := m.DescribeVpcConnection(ctx, v.VpcConnectionARN)
	if d.State != "CREATING" {
		t.Fatalf("describe state = %s, want CREATING", d.State)
	}

	fc.Advance(settleWindow)

	d, _ = m.DescribeVpcConnection(ctx, v.VpcConnectionARN)
	if d.State != "AVAILABLE" {
		t.Fatalf("settled state = %s, want AVAILABLE", d.State)
	}
}

// kafkaSamples returns the per-minute SampleCount datapoints of an AWS/Kafka
// metric with exactly dims between from and to.
func kafkaSamples(
	t *testing.T, cw *cloudwatch.Mock, metric string, dims map[string]string, from, to time.Time,
) []float64 {
	t.Helper()

	res, err := cw.GetMetricData(context.Background(), mondriver.GetMetricInput{
		Namespace: "AWS/Kafka", MetricName: metric, Dimensions: dims,
		StartTime: from, EndTime: to, Period: 60, Stat: "SampleCount",
	})
	if err != nil {
		t.Fatalf("GetMetricData %s: %v", metric, err)
	}

	return res.Values
}

// TestClusterMetricsPublishedByTick asserts the AWS/Kafka metrics come from the
// scheduler, not from requests: nothing is published on create, nothing while
// the cluster is CREATING, then one sample per minute once it is ACTIVE, under
// the documented dimension names ("Cluster Name", "Broker ID"), with the topic
// counts read from the cluster's stored topics.
func TestClusterMetricsPublishedByTick(t *testing.T) {
	fc := config.NewFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithAsyncSettle())
	m := kafka.New(opts)
	cw := cloudwatch.New(opts)
	m.SetMonitoring(cw)

	ctx := context.Background()
	start := fc.Now()
	c := createThreeAZCluster(t, m, "metered")

	ids, err := cw.ListMetricsDetailed(ctx)
	if err != nil || len(ids) != 0 {
		t.Fatalf("metrics published on create: %+v/%v", ids, err)
	}

	if m.Tick(fc.Now()) {
		t.Fatal("Tick published metrics for a CREATING cluster")
	}

	fc.Advance(settleWindow)

	if _, err := m.CreateTopic(ctx, c.ClusterARN, json.RawMessage(
		`{"topicName":"orders","partitionCount":6,"replicationFactor":3}`)); err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}

	if !m.Tick(fc.Now()) {
		t.Fatal("Tick published nothing for an ACTIVE cluster")
	}

	// A second tick inside the same minute is not due.
	fc.Advance(30 * time.Second)

	if m.Tick(fc.Now()) {
		t.Fatal("Tick republished within the 1-minute interval")
	}

	fc.Advance(30 * time.Second)

	if !m.Tick(fc.Now()) {
		t.Fatal("Tick did not publish once the minute elapsed")
	}

	end := fc.Now().Add(time.Second)
	clusterDims := map[string]string{"Cluster Name": c.ClusterName}

	if got := kafkaSamples(t, cw, "ActiveControllerCount", clusterDims, start, end); len(got) != 2 {
		t.Fatalf("ActiveControllerCount samples = %v, want 2 one-minute samples", got)
	}

	topics, err := cw.GetMetricData(ctx, mondriver.GetMetricInput{
		Namespace: "AWS/Kafka", MetricName: "GlobalPartitionCount", Dimensions: clusterDims,
		StartTime: start, EndTime: end, Period: 3600, Stat: "Maximum",
	})
	if err != nil || len(topics.Values) != 1 || topics.Values[0] != 6 || topics.Unit != "Count" {
		t.Fatalf("GlobalPartitionCount = %+v/%v, want 6 Count", topics, err)
	}

	for broker := 1; broker <= 6; broker++ {
		dims := map[string]string{"Cluster Name": c.ClusterName, "Broker ID": strconv.Itoa(broker)}
		if got := kafkaSamples(t, cw, "UnderReplicatedPartitions", dims, start, end); len(got) != 2 {
			t.Fatalf("broker %d UnderReplicatedPartitions samples = %v", broker, got)
		}
	}

	// Host and traffic metrics cloudemu cannot know are not fabricated.
	ids, _ = cw.ListMetricsDetailed(ctx)
	for _, id := range ids {
		switch id.MetricName {
		case "CpuUser", "CpuIdle", "MemoryUsed", "KafkaDataLogsDiskUsed", "BytesInPerSec":
			t.Fatalf("fabricated metric published: %+v", id)
		}
	}
}

// TestTickSkipsDeletingAndServerless asserts a DELETING cluster stops
// publishing and serverless clusters (a different metric set) never publish.
func TestTickSkipsDeletingAndServerless(t *testing.T) {
	fc := config.NewFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithAsyncSettle())
	m := kafka.New(opts)
	m.SetMonitoring(cloudwatch.New(opts))

	ctx := context.Background()
	c := createThreeAZCluster(t, m, "short-lived")
	fc.Advance(settleWindow)

	if _, _, err := m.DeleteCluster(ctx, c.ClusterARN, ""); err != nil {
		t.Fatalf("DeleteCluster: %v", err)
	}

	if m.Tick(fc.Now()) {
		t.Fatal("Tick published for a DELETING cluster")
	}

	if _, err := m.CreateClusterV2(ctx, driver.CreateClusterV2Input{
		ClusterName: "sls",
		Serverless:  json.RawMessage(`{"vpcConfigs":[{"subnetIds":["subnet-1","subnet-2"]}]}`),
	}); err != nil {
		t.Fatalf("CreateClusterV2: %v", err)
	}

	fc.Advance(settleWindow)

	// This tick removes the cluster whose DELETING window has ended (a change);
	// the next one, with only the serverless cluster left, does nothing.
	if !m.Tick(fc.Now()) {
		t.Fatal("Tick did not report removing the deleted cluster")
	}

	fc.Advance(time.Minute)

	if m.Tick(fc.Now()) {
		t.Fatal("Tick published for a serverless cluster")
	}
}

func encryptionInfo(t *testing.T, c *driver.Cluster) map[string]map[string]any {
	t.Helper()

	var out map[string]map[string]any
	if err := json.Unmarshal(c.RawOptions["encryptionInfo"], &out); err != nil {
		t.Fatalf("decode encryptionInfo %s: %v", c.RawOptions["encryptionInfo"], err)
	}

	return out
}

// TestDefaultEncryptionAtRestKey asserts the default at-rest key is a KMS key
// ARN in the cluster's own account and region (one AWS managed aws/kafka key
// per account+region), not a hard-coded key/msk-default.
func TestDefaultEncryptionAtRestKey(t *testing.T) {
	m := kafka.New(config.NewOptions(config.WithRegion("eu-west-1"), config.WithAccountID("123456789012")))
	ctx := context.Background()

	a := createThreeAZCluster(t, m, "enc-a")
	b := createThreeAZCluster(t, m, "enc-b")

	keyA, _ := encryptionInfo(t, a)["encryptionAtRest"]["dataVolumeKMSKeyId"].(string)
	keyB, _ := encryptionInfo(t, b)["encryptionAtRest"]["dataVolumeKMSKeyId"].(string)

	if !strings.HasPrefix(keyA, "arn:aws:kms:eu-west-1:123456789012:key/") || strings.Contains(keyA, "msk-default") {
		t.Fatalf("default key = %q", keyA)
	}

	if keyA != keyB {
		t.Fatalf("managed key differs across clusters: %q vs %q", keyA, keyB)
	}

	if len(strings.TrimPrefix(keyA, "arn:aws:kms:eu-west-1:123456789012:key/")) != 36 {
		t.Fatalf("key id is not a UUID: %q", keyA)
	}

	// An explicit key is kept; an in-transit-only block gets the managed key.
	explicit := "arn:aws:kms:eu-west-1:123456789012:key/11111111-2222-3333-4444-555555555555"

	c, err := m.CreateCluster(ctx, driver.CreateClusterInput{
		ClusterName: "enc-explicit", NumberOfBrokerNodes: 3, BrokerNodeGroupInfo: bng(),
		RawOptions: map[string]json.RawMessage{
			"encryptionInfo": json.RawMessage(`{"encryptionAtRest":{"dataVolumeKMSKeyId":"` + explicit + `"}}`),
		},
	})
	if err != nil {
		t.Fatalf("CreateCluster explicit: %v", err)
	}

	if got := encryptionInfo(t, c)["encryptionAtRest"]["dataVolumeKMSKeyId"]; got != explicit {
		t.Fatalf("explicit key = %v, want %s", got, explicit)
	}

	d, err := m.CreateCluster(ctx, driver.CreateClusterInput{
		ClusterName: "enc-transit", NumberOfBrokerNodes: 3, BrokerNodeGroupInfo: bng(),
		RawOptions: map[string]json.RawMessage{
			"encryptionInfo": json.RawMessage(`{"encryptionInTransit":{"clientBroker":"TLS_PLAINTEXT","inCluster":true}}`),
		},
	})
	if err != nil {
		t.Fatalf("CreateCluster transit-only: %v", err)
	}

	info := encryptionInfo(t, d)
	if info["encryptionAtRest"]["dataVolumeKMSKeyId"] != keyA || info["encryptionInTransit"]["clientBroker"] != "TLS_PLAINTEXT" {
		t.Fatalf("transit-only block = %+v", info)
	}
}

// TestSnapshotSettleStates asserts the lifecycle windows survive a snapshot: a
// cluster and a VPC connection snapshotted mid-create restore still CREATING
// and settle on schedule, and a cluster snapshotted mid-delete restores
// DELETING and is gone once its window ends.
func TestSnapshotSettleStates(t *testing.T) {
	src, fc := newSettleMock(t)
	ctx := context.Background()

	target := createThreeAZCluster(t, src, "vpc-target")
	deleting := createThreeAZCluster(t, src, "mid-delete")

	fc.Advance(settleWindow)

	creating := createThreeAZCluster(t, src, "mid-create")

	if _, _, err := src.DeleteCluster(ctx, deleting.ClusterARN, ""); err != nil {
		t.Fatalf("DeleteCluster: %v", err)
	}

	vpc, err := src.CreateVpcConnection(ctx, json.RawMessage(
		`{"targetClusterArn":"`+target.ClusterARN+`","authentication":"SASL_IAM","vpcId":"vpc-1"}`))
	if err != nil {
		t.Fatalf("CreateVpcConnection: %v", err)
	}

	raw, err := src.Snapshot(ctx, true)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	dst := kafka.New(config.NewOptions(config.WithClock(fc), config.WithAsyncSettle()))
	if err := dst.Restore(ctx, raw); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	got, err := dst.DescribeCluster(ctx, creating.ClusterARN)
	if err != nil || got.State != driver.ClusterStateCreating {
		t.Fatalf("restored mid-create = %+v/%v, want CREATING", got, err)
	}

	got, err = dst.DescribeCluster(ctx, deleting.ClusterARN)
	if err != nil || got.State != driver.ClusterStateDeleting {
		t.Fatalf("restored mid-delete = %+v/%v, want DELETING", got, err)
	}

	v, err := dst.DescribeVpcConnection(ctx, vpc.VpcConnectionARN)
	if err != nil || v.State != "CREATING" {
		t.Fatalf("restored vpc connection = %+v/%v, want CREATING", v, err)
	}

	fc.Advance(settleWindow)

	if got, _ = dst.DescribeCluster(ctx, creating.ClusterARN); got.State != driver.ClusterStateActive {
		t.Fatalf("restored cluster after window = %s, want ACTIVE", got.State)
	}

	_, err = dst.DescribeCluster(ctx, deleting.ClusterARN)
	wantException(t, err, driver.ExNotFound)

	if v, _ = dst.DescribeVpcConnection(ctx, vpc.VpcConnectionARN); v.State != "AVAILABLE" {
		t.Fatalf("restored vpc connection after window = %s, want AVAILABLE", v.State)
	}
}
