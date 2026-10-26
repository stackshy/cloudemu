package kafka_test

import (
	"context"
	"encoding/json"
	"strconv"
	"sync"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/cloudwatch"
	"github.com/stackshy/cloudemu/v2/providers/aws/kafka"
	"github.com/stackshy/cloudemu/v2/services/kafka/driver"
)

// TestSnapshotConcurrentWithMutations runs Snapshot while topics, SCRAM
// secrets, tags and broker counts change on a cluster, VPC connection,
// configuration and replicator. Snapshot must deep-copy each record under its
// lock, so the JSON encoding never reads a map or slice a writer is changing
// (run with -race). The final snapshot then restores every record.
func TestSnapshotConcurrentWithMutations(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	c := createThreeAZCluster(t, m, "busy")

	vpc, err := m.CreateVpcConnection(ctx, json.RawMessage(
		`{"targetClusterArn":"`+c.ClusterARN+`","authentication":"SASL_IAM","vpcId":"vpc-1"}`))
	if err != nil {
		t.Fatalf("CreateVpcConnection: %v", err)
	}

	cfg, err := m.CreateConfiguration(ctx, driver.CreateConfigurationInput{
		Name: "busy-cfg", KafkaVersions: []string{"3.6.0"}, ServerProperties: []byte("a=b"),
	})
	if err != nil {
		t.Fatalf("CreateConfiguration: %v", err)
	}

	repl, err := m.CreateReplicator(ctx, json.RawMessage(`{"replicatorName":"busy-rep","kafkaClusters":[],`+
		`"replicationInfoList":[],"serviceExecutionRoleArn":"arn:aws:iam::123456789012:role/r"}`))
	if err != nil {
		t.Fatalf("CreateReplicator: %v", err)
	}

	const n = 50

	var wg sync.WaitGroup

	wg.Add(2)

	go func() {
		defer wg.Done()

		for i := range n {
			name := "t" + strconv.Itoa(i)
			_, _ = m.CreateTopic(ctx, c.ClusterARN, json.RawMessage(`{"topicName":"`+name+`","partitionCount":1}`))
			_, _ = m.UpdateTopic(ctx, c.ClusterARN, name, json.RawMessage(`{"partitionCount":2}`))
			_, _ = m.BatchAssociateScramSecret(ctx, c.ClusterARN,
				[]string{"arn:aws:secretsmanager:us-east-1:123456789012:secret:AmazonMSK_" + name})
			_ = m.TagResource(ctx, c.ClusterARN, map[string]string{name: "v"})
			_ = m.TagResource(ctx, vpc.VpcConnectionARN, map[string]string{name: "v"})
			_ = m.TagResource(ctx, cfg.ARN, map[string]string{name: "v"})
			_ = m.TagResource(ctx, repl.ReplicatorARN, map[string]string{name: "v"})
			_, _ = m.UpdateBrokerCount(ctx, c.ClusterARN, "", int32(3+i%3))
		}
	}()

	go func() {
		defer wg.Done()

		for range n {
			if _, err := m.Snapshot(ctx, false); err != nil {
				t.Errorf("Snapshot: %v", err)

				return
			}
		}
	}()

	wg.Wait()

	raw, err := m.Snapshot(ctx, false)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	dst := newMock(t)
	if err := dst.Restore(ctx, raw); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	if topics, _, err := dst.ListTopics(ctx, c.ClusterARN, driver.Page{MaxResults: 100}); err != nil || len(topics) != n {
		t.Fatalf("restored topics = %d/%v, want %d", len(topics), err, n)
	}

	for _, arn := range []string{vpc.VpcConnectionARN, cfg.ARN, repl.ReplicatorARN} {
		if tags, err := dst.ListTagsForResource(ctx, arn); err != nil || len(tags) < n {
			t.Fatalf("restored tags on %s = %d/%v", arn, len(tags), err)
		}
	}
}

// TestDefaultEncryptionInTransitCompleted asserts a partial encryptionInfo gets
// MSK's documented in-transit defaults (ClientBroker TLS, InCluster true) for
// any field it leaves out, while explicit values, including InCluster false,
// are kept.
func TestDefaultEncryptionInTransitCompleted(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	cases := []struct {
		name, block, client string
		inCluster           bool
	}{
		{"at-rest-only", `{"encryptionAtRest":{"dataVolumeKMSKeyId":"arn:aws:kms:us-east-1:123456789012:key/mine"}}`, "TLS", true},
		{"client-only", `{"encryptionInTransit":{"clientBroker":"TLS_PLAINTEXT"}}`, "TLS_PLAINTEXT", true},
		{"in-cluster-only", `{"encryptionInTransit":{"inCluster":false}}`, "TLS", false},
		{"empty-block", `{}`, "TLS", true},
	}

	for _, tc := range cases {
		c, err := m.CreateCluster(ctx, driver.CreateClusterInput{
			ClusterName: "enc-" + tc.name, NumberOfBrokerNodes: 3, BrokerNodeGroupInfo: bng(),
			RawOptions: map[string]json.RawMessage{"encryptionInfo": json.RawMessage(tc.block)},
		})
		if err != nil {
			t.Fatalf("%s: CreateCluster: %v", tc.name, err)
		}

		info := encryptionInfo(t, c)
		if info["encryptionInTransit"]["clientBroker"] != tc.client ||
			info["encryptionInTransit"]["inCluster"] != tc.inCluster {
			t.Fatalf("%s: encryptionInTransit = %+v, want %s/%v",
				tc.name, info["encryptionInTransit"], tc.client, tc.inCluster)
		}

		if key, _ := info["encryptionAtRest"]["dataVolumeKMSKeyId"].(string); key == "" {
			t.Fatalf("%s: no at-rest key: %+v", tc.name, info)
		}
	}
}

// TestTickUsesItsNow asserts Tick judges a cluster's state by the time it is
// given, not the options clock: a Tick at a time past the CREATING window
// publishes even though the options clock has not moved.
func TestTickUsesItsNow(t *testing.T) {
	m, fc := newSettleMock(t)
	m.SetMonitoring(cloudwatch.New(config.NewOptions(config.WithClock(fc))))

	createThreeAZCluster(t, m, "ahead")

	if !m.Tick(fc.Now().Add(settleWindow)) {
		t.Fatal("Tick at a time past the CREATING window published nothing")
	}

	if m.Tick(fc.Now()) {
		t.Fatal("Tick before the CREATING window ends published metrics")
	}
}

// TestTickReapsFinishedDelete asserts Tick removes a cluster whose DELETING
// window has ended and reports the change (so serve persists it), even with no
// monitoring backend wired and no read in between.
func TestTickReapsFinishedDelete(t *testing.T) {
	m, fc := newSettleMock(t)
	ctx := context.Background()

	c := createThreeAZCluster(t, m, "reaped")
	fc.Advance(settleWindow)

	if m.Tick(fc.Now()) {
		t.Fatal("Tick reported a change with nothing to do")
	}

	if _, _, err := m.DeleteCluster(ctx, c.ClusterARN, ""); err != nil {
		t.Fatalf("DeleteCluster: %v", err)
	}

	if m.Tick(fc.Now()) {
		t.Fatal("Tick reaped a cluster inside its DELETING window")
	}

	fc.Advance(settleWindow)

	if !m.Tick(fc.Now()) {
		t.Fatal("Tick did not report reaping a finished delete")
	}

	raw, err := m.Snapshot(ctx, false)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	var snap struct {
		Clusters     map[string]json.RawMessage `json:"clusters"`
		ClusterNames map[string]string          `json:"clusterNames"`
	}
	if err := json.Unmarshal(raw, &snap); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}

	if len(snap.Clusters) != 0 || len(snap.ClusterNames) != 0 {
		t.Fatalf("snapshot after reap still holds the cluster: %s", raw)
	}

	if m.Tick(fc.Now()) {
		t.Fatal("Tick reported a change after the cluster was gone")
	}
}

// TestClusterWritesRequireActive asserts topic, SCRAM secret and cluster
// policy writes are rejected while a cluster is CREATING or DELETING (real MSK
// accepts no API operations on it in those states) and accepted once ACTIVE.
func TestClusterWritesRequireActive(t *testing.T) {
	m, fc := newSettleMock(t)
	ctx := context.Background()

	c := createThreeAZCluster(t, m, "guarded")
	secret := []string{"arn:aws:secretsmanager:us-east-1:123456789012:secret:AmazonMSK_s"}

	writes := map[string]func() error{
		"CreateTopic": func() error {
			_, err := m.CreateTopic(ctx, c.ClusterARN, json.RawMessage(`{"topicName":"t","partitionCount":1}`))

			return err
		},
		"UpdateTopic": func() error {
			_, err := m.UpdateTopic(ctx, c.ClusterARN, "t", json.RawMessage(`{"partitionCount":2}`))

			return err
		},
		"BatchAssociateScramSecret": func() error {
			_, err := m.BatchAssociateScramSecret(ctx, c.ClusterARN, secret)

			return err
		},
		"BatchDisassociateScramSecret": func() error {
			_, err := m.BatchDisassociateScramSecret(ctx, c.ClusterARN, secret)

			return err
		},
		"PutClusterPolicy": func() error {
			_, err := m.PutClusterPolicy(ctx, c.ClusterARN, json.RawMessage(`{"policy":"{}"}`))

			return err
		},
		"DeleteClusterPolicy": func() error { return m.DeleteClusterPolicy(ctx, c.ClusterARN) },
		"DeleteTopic":         func() error { return m.DeleteTopic(ctx, c.ClusterARN, "t") },
	}
	order := []string{
		"CreateTopic", "UpdateTopic", "BatchAssociateScramSecret", "BatchDisassociateScramSecret",
		"PutClusterPolicy", "DeleteClusterPolicy", "DeleteTopic",
	}

	for _, op := range order {
		wantException(t, writes[op](), driver.ExBadRequest)
	}

	fc.Advance(settleWindow)

	for _, op := range order {
		if err := writes[op](); err != nil {
			t.Fatalf("%s on ACTIVE cluster: %v", op, err)
		}
	}

	if _, _, err := m.DeleteCluster(ctx, c.ClusterARN, ""); err != nil {
		t.Fatalf("DeleteCluster: %v", err)
	}

	for _, op := range order {
		wantException(t, writes[op](), driver.ExBadRequest)
	}
}

// TestCompatibleVersionsAfterDeleteWindow asserts GetCompatibleKafkaVersions
// stops answering for a cluster once its DELETING window has ended.
func TestCompatibleVersionsAfterDeleteWindow(t *testing.T) {
	m, fc := newSettleMock(t)
	ctx := context.Background()

	c := createThreeAZCluster(t, m, "versioned")
	fc.Advance(settleWindow)

	if _, _, err := m.DeleteCluster(ctx, c.ClusterARN, ""); err != nil {
		t.Fatalf("DeleteCluster: %v", err)
	}

	if _, err := m.GetCompatibleKafkaVersions(ctx, c.ClusterARN); err != nil {
		t.Fatalf("GetCompatibleKafkaVersions while DELETING: %v", err)
	}

	fc.Advance(settleWindow)

	_, err := m.GetCompatibleKafkaVersions(ctx, c.ClusterARN)
	wantException(t, err, driver.ExNotFound)
}

// TestCreateReclaimsDanglingName asserts a create succeeds when the name index
// still points at a cluster ARN that is no longer stored (a snapshot taken
// between the two removals).
func TestCreateReclaimsDanglingName(t *testing.T) {
	m := kafka.New(config.NewOptions())
	ctx := context.Background()

	snap := `{"clusterNames":{"orphan":"arn:aws:kafka:us-east-1:123456789012:cluster/orphan/gone-1"}}`
	if err := m.Restore(ctx, json.RawMessage(snap)); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	if _, err := m.CreateCluster(ctx, driver.CreateClusterInput{
		ClusterName: "orphan", NumberOfBrokerNodes: 3, BrokerNodeGroupInfo: bng(),
	}); err != nil {
		t.Fatalf("CreateCluster over a dangling name: %v", err)
	}
}
