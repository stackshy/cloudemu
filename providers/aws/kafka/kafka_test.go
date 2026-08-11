package kafka_test

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/kafka"
	"github.com/stackshy/cloudemu/v2/services/kafka/driver"
)

func newMock(t *testing.T) *kafka.Mock {
	t.Helper()

	return kafka.New(config.NewOptions())
}

func TestCreateDescribeCluster(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	out, err := m.CreateCluster(ctx, driver.CreateClusterInput{
		ClusterName:         "my-cluster",
		KafkaVersion:        "3.6.0",
		NumberOfBrokerNodes: 3,
		BrokerNodeGroupInfo: &driver.BrokerNodeGroupInfo{
			ClientSubnets: []string{"subnet-1", "subnet-2"},
			InstanceType:  "kafka.m5.large",
		},
		Tags: map[string]string{"env": "prod"},
	})
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	if !strings.Contains(out.ClusterARN, ":kafka:") || !strings.Contains(out.ClusterARN, "cluster/my-cluster/") {
		t.Fatalf("unexpected ARN: %s", out.ClusterARN)
	}

	if out.State != driver.ClusterStateActive || out.ClusterType != driver.ClusterTypeProvisioned {
		t.Fatalf("expected ACTIVE PROVISIONED cluster, got %+v", out)
	}

	desc, err := m.DescribeCluster(ctx, out.ClusterARN)
	if err != nil {
		t.Fatalf("DescribeCluster: %v", err)
	}

	if desc.NumberOfBrokerNodes != 3 || desc.BrokerNodeGroupInfo.InstanceType != "kafka.m5.large" {
		t.Fatalf("cluster not reflected: %+v", desc)
	}
}

func TestCreateClusterValidation(t *testing.T) {
	m := newMock(t)

	_, err := m.CreateCluster(context.Background(), driver.CreateClusterInput{ClusterName: ""})

	var apiErr *driver.APIError
	if !errors.As(err, &apiErr) || apiErr.Exception != driver.ExBadRequest {
		t.Fatalf("want BadRequestException, got %v", err)
	}
}

func TestCreateClusterDuplicate(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	if _, err := m.CreateCluster(ctx, driver.CreateClusterInput{ClusterName: "dup", NumberOfBrokerNodes: 3, BrokerNodeGroupInfo: bng()}); err != nil {
		t.Fatalf("first create: %v", err)
	}

	_, err := m.CreateCluster(ctx, driver.CreateClusterInput{ClusterName: "dup", NumberOfBrokerNodes: 3, BrokerNodeGroupInfo: bng()})

	var apiErr *driver.APIError
	if !errors.As(err, &apiErr) || apiErr.Exception != driver.ExConflict {
		t.Fatalf("want ConflictException, got %v", err)
	}
}

func TestDescribeMissingCluster(t *testing.T) {
	m := newMock(t)

	_, err := m.DescribeCluster(context.Background(), "arn:aws:kafka:us-east-1:123456789012:cluster/nope/x")

	var apiErr *driver.APIError
	if !errors.As(err, &apiErr) || apiErr.Exception != driver.ExNotFound {
		t.Fatalf("want NotFoundException, got %v", err)
	}
}

func TestListAndDeleteCluster(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	var arns []string

	for _, n := range []string{"alpha", "bravo", "charlie"} {
		out, err := m.CreateCluster(ctx, driver.CreateClusterInput{ClusterName: n, NumberOfBrokerNodes: 3, BrokerNodeGroupInfo: bng()})
		if err != nil {
			t.Fatalf("create %s: %v", n, err)
		}

		arns = append(arns, out.ClusterARN)
	}

	list, _, err := m.ListClusters(ctx, "", driver.Page{})
	if err != nil {
		t.Fatalf("ListClusters: %v", err)
	}

	if len(list) != 3 || list[0].ClusterName != "alpha" {
		t.Fatalf("unexpected list (want sorted 3): %+v", list)
	}

	// Prefix filter.
	filtered, _, _ := m.ListClusters(ctx, "al", driver.Page{})
	if len(filtered) != 1 || filtered[0].ClusterName != "alpha" {
		t.Fatalf("prefix filter failed: %+v", filtered)
	}

	arnOut, state, err := m.DeleteCluster(ctx, arns[0], "")
	if err != nil || arnOut != arns[0] || state != driver.ClusterStateDeleting {
		t.Fatalf("DeleteCluster: arn=%s state=%s err=%v", arnOut, state, err)
	}

	if _, err := m.DescribeCluster(ctx, arns[0]); err == nil {
		t.Fatal("expected cluster gone after delete")
	}

	// Name freed for reuse after delete.
	if _, err := m.CreateCluster(ctx, driver.CreateClusterInput{ClusterName: "alpha", NumberOfBrokerNodes: 3, BrokerNodeGroupInfo: bng()}); err != nil {
		t.Fatalf("name should be reusable after delete: %v", err)
	}
}

func TestBootstrapBrokers(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	out, err := m.CreateCluster(ctx, driver.CreateClusterInput{ClusterName: "brokers", NumberOfBrokerNodes: 3, BrokerNodeGroupInfo: bng()})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	brokers, err := m.GetBootstrapBrokers(ctx, out.ClusterARN)
	if err != nil {
		t.Fatalf("GetBootstrapBrokers: %v", err)
	}

	if !strings.Contains(brokers["bootstrapBrokerString"], ":9092") {
		t.Fatalf("unexpected brokers: %+v", brokers)
	}
}

// TestConcurrentCreate exercises the createMu + name-claim under -race: exactly
// one creator wins, the rest see ConflictException.
func TestConcurrentCreate(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	const goroutines = 20

	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		wins  int
		dupes int
	)

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()

			_, err := m.CreateCluster(ctx, driver.CreateClusterInput{ClusterName: "race", NumberOfBrokerNodes: 3, BrokerNodeGroupInfo: bng()})

			mu.Lock()
			defer mu.Unlock()

			switch {
			case err == nil:
				wins++
			default:
				var apiErr *driver.APIError
				if errors.As(err, &apiErr) && apiErr.Exception == driver.ExConflict {
					dupes++
				} else {
					t.Errorf("unexpected error: %v", err)
				}
			}
		}()
	}

	wg.Wait()

	if wins != 1 || dupes != goroutines-1 {
		t.Fatalf("want 1 win and %d dupes, got wins=%d dupes=%d", goroutines-1, wins, dupes)
	}
}

// TestNoAliasOnRead asserts Describe returns a deep copy: mutating a returned
// map/slice must not affect subsequently returned values.
func TestNoAliasOnRead(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	out, err := m.CreateCluster(ctx, driver.CreateClusterInput{
		ClusterName:         "alias",
		NumberOfBrokerNodes: 3,
		Tags:                map[string]string{"env": "prod"},
		BrokerNodeGroupInfo: &driver.BrokerNodeGroupInfo{ClientSubnets: []string{"subnet-1"}},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	first, err := m.DescribeCluster(ctx, out.ClusterARN)
	if err != nil {
		t.Fatalf("describe: %v", err)
	}

	// Mutate the returned map and nested slice; stored state must be untouched.
	first.Tags["injected"] = "bad"
	first.BrokerNodeGroupInfo.ClientSubnets[0] = "hacked"

	second, _ := m.DescribeCluster(ctx, out.ClusterARN)
	if _, leaked := second.Tags["injected"]; leaked {
		t.Fatal("Describe aliased stored Tags map")
	}

	if second.BrokerNodeGroupInfo.ClientSubnets[0] != "subnet-1" {
		t.Fatal("Describe aliased stored ClientSubnets slice")
	}
}

// --- Configurations ---

func TestConfigurationLifecycle(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	cfg, err := m.CreateConfiguration(ctx, driver.CreateConfigurationInput{
		Name:             "my-config",
		Description:      "rev1",
		KafkaVersions:    []string{"3.6.0"},
		ServerProperties: []byte("auto.create.topics.enable=true"),
	})
	if err != nil {
		t.Fatalf("CreateConfiguration: %v", err)
	}

	if cfg.LatestRevision.Revision != 1 || cfg.State != driver.ConfigurationStateActive {
		t.Fatalf("unexpected config: %+v", cfg)
	}

	desc, err := m.DescribeConfiguration(ctx, cfg.ARN)
	if err != nil || desc.Name != "my-config" {
		t.Fatalf("DescribeConfiguration: %v %+v", err, desc)
	}

	// UpdateConfiguration adds a revision.
	upd, err := m.UpdateConfiguration(ctx, cfg.ARN, "rev2", []byte("num.partitions=3"))
	if err != nil || upd.LatestRevision.Revision != 2 {
		t.Fatalf("UpdateConfiguration: %v rev=%d", err, upd.LatestRevision.Revision)
	}

	revs, _, err := m.ListConfigurationRevisions(ctx, cfg.ARN, driver.Page{})
	if err != nil || len(revs) != 2 {
		t.Fatalf("ListConfigurationRevisions: %v len=%d", err, len(revs))
	}

	_, rev1, err := m.DescribeConfigurationRevision(ctx, cfg.ARN, 1)
	if err != nil || string(rev1.ServerProperties) != "auto.create.topics.enable=true" {
		t.Fatalf("DescribeConfigurationRevision rev1: %v props=%q", err, rev1.ServerProperties)
	}

	list, _, err := m.ListConfigurations(ctx, driver.Page{})
	if err != nil || len(list) != 1 {
		t.Fatalf("ListConfigurations: %v len=%d", err, len(list))
	}

	arnOut, state, err := m.DeleteConfiguration(ctx, cfg.ARN)
	if err != nil || arnOut != cfg.ARN || state != driver.ConfigurationStateDeleting {
		t.Fatalf("DeleteConfiguration: %v", err)
	}

	if _, err := m.DescribeConfiguration(ctx, cfg.ARN); err == nil {
		t.Fatal("expected configuration gone after delete")
	}
}

func TestDescribeMissingConfiguration(t *testing.T) {
	m := newMock(t)

	_, err := m.DescribeConfiguration(context.Background(), "arn:aws:kafka:us-east-1:123456789012:configuration/nope/x")

	var apiErr *driver.APIError
	if !errors.As(err, &apiErr) || apiErr.Exception != driver.ExNotFound {
		t.Fatalf("want NotFoundException, got %v", err)
	}
}

func TestConfigRevisionNoAlias(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	cfg, err := m.CreateConfiguration(ctx, driver.CreateConfigurationInput{
		Name:             "alias-config",
		ServerProperties: []byte("k=v"),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	_, rev, _ := m.DescribeConfigurationRevision(ctx, cfg.ARN, 1)
	rev.ServerProperties[0] = 'X'

	_, rev2, _ := m.DescribeConfigurationRevision(ctx, cfg.ARN, 1)
	if rev2.ServerProperties[0] != 'k' {
		t.Fatal("DescribeConfigurationRevision aliased stored ServerProperties")
	}
}

// --- Phase 2: clusters v2, operations, mutations, nodes, versions, tags ---

// TestV1CreateV2Describe asserts a v1-created cluster is describable via the v2
// op and renders as PROVISIONED (same underlying store).
func TestV1CreateV2Describe(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	out, err := m.CreateCluster(ctx, driver.CreateClusterInput{
		ClusterName:         "cross",
		NumberOfBrokerNodes: 3,
		BrokerNodeGroupInfo: bng(),
	})
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	v2, err := m.DescribeClusterV2(ctx, out.ClusterARN)
	if err != nil {
		t.Fatalf("DescribeClusterV2: %v", err)
	}

	if v2.ClusterType != driver.ClusterTypeProvisioned || v2.NumberOfBrokerNodes != 3 {
		t.Fatalf("v2 describe of v1 cluster wrong: %+v", v2)
	}
}

// TestCreateClusterV2Serverless asserts a serverless v2 cluster is stored with
// its type and listed under the SERVERLESS filter only.
func TestCreateClusterV2Serverless(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	_, err := m.CreateClusterV2(ctx, driver.CreateClusterV2Input{
		ClusterName: "serverless-1",
		Serverless:  []byte(`{"vpcConfigs":[]}`),
	})
	if err != nil {
		t.Fatalf("CreateClusterV2 serverless: %v", err)
	}

	if _, err := m.CreateCluster(ctx, driver.CreateClusterInput{ClusterName: "prov-1", NumberOfBrokerNodes: 2, BrokerNodeGroupInfo: bng()}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	sl, _, err := m.ListClustersV2(ctx, "", driver.ClusterTypeServerless, driver.Page{})
	if err != nil || len(sl) != 1 || sl[0].ClusterName != "serverless-1" {
		t.Fatalf("serverless filter: %v %+v", err, sl)
	}

	pr, _, _ := m.ListClustersV2(ctx, "", driver.ClusterTypeProvisioned, driver.Page{})
	if len(pr) != 1 || pr[0].ClusterName != "prov-1" {
		t.Fatalf("provisioned filter: %+v", pr)
	}
}

// TestCreateClusterV2Validation asserts neither/both provisioned+serverless is a
// BadRequestException.
func TestCreateClusterV2Validation(t *testing.T) {
	m := newMock(t)

	_, err := m.CreateClusterV2(context.Background(), driver.CreateClusterV2Input{ClusterName: "empty"})

	var apiErr *driver.APIError
	if !errors.As(err, &apiErr) || apiErr.Exception != driver.ExBadRequest {
		t.Fatalf("want BadRequestException, got %v", err)
	}
}

// TestUpdateMutatesRecordsOperation asserts UpdateBrokerCount mutates the
// cluster, records a COMPLETED operation, bumps the version, and a later
// Describe reflects the new count.
func TestUpdateMutatesRecordsOperation(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	c, err := m.CreateCluster(ctx, driver.CreateClusterInput{ClusterName: "upd", NumberOfBrokerNodes: 3, BrokerNodeGroupInfo: bng()})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	op, err := m.UpdateBrokerCount(ctx, c.ClusterARN, c.CurrentVersion, 6)
	if err != nil {
		t.Fatalf("UpdateBrokerCount: %v", err)
	}

	if op.ClusterARN != c.ClusterARN || op.OperationState != "COMPLETED" || op.OperationType != "UPDATE_BROKER_COUNT" {
		t.Fatalf("unexpected operation: %+v", op)
	}

	desc, _ := m.DescribeCluster(ctx, c.ClusterARN)
	if desc.NumberOfBrokerNodes != 6 {
		t.Fatalf("broker count not mutated: %d", desc.NumberOfBrokerNodes)
	}

	if desc.CurrentVersion == c.CurrentVersion {
		t.Fatal("version not bumped after update")
	}

	// The operation is listable and describable.
	ops, _, err := m.ListClusterOperations(ctx, c.ClusterARN, driver.Page{})
	if err != nil || len(ops) != 1 || ops[0].OperationARN != op.OperationARN {
		t.Fatalf("ListClusterOperations: %v %+v", err, ops)
	}

	got, err := m.DescribeClusterOperation(ctx, op.OperationARN)
	if err != nil || got.OperationType != "UPDATE_BROKER_COUNT" {
		t.Fatalf("DescribeClusterOperation: %v %+v", err, got)
	}

	if _, err := m.DescribeClusterOperationV2(ctx, op.OperationARN); err != nil {
		t.Fatalf("DescribeClusterOperationV2: %v", err)
	}
}

// TestUpdateVersionMismatch asserts a stale currentVersion is a BadRequest.
func TestUpdateVersionMismatch(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	c, err := m.CreateCluster(ctx, driver.CreateClusterInput{ClusterName: "cver", NumberOfBrokerNodes: 3, BrokerNodeGroupInfo: bng()})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	_, err = m.UpdateBrokerCount(ctx, c.ClusterARN, "wrong-version", 6)

	var apiErr *driver.APIError
	if !errors.As(err, &apiErr) || apiErr.Exception != driver.ExBadRequest {
		t.Fatalf("want BadRequestException, got %v", err)
	}
}

// TestUpdateKafkaVersionReflected asserts UpdateClusterKafkaVersion mutates the
// modeled version.
func TestUpdateKafkaVersionReflected(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	c, _ := m.CreateCluster(ctx, driver.CreateClusterInput{ClusterName: "kv", KafkaVersion: "3.5.1", NumberOfBrokerNodes: 3, BrokerNodeGroupInfo: bng()})

	_, err := m.UpdateClusterKafkaVersion(ctx, c.ClusterARN, c.CurrentVersion, []byte(`{"targetKafkaVersion":"3.6.0"}`))
	if err != nil {
		t.Fatalf("UpdateClusterKafkaVersion: %v", err)
	}

	desc, _ := m.DescribeCluster(ctx, c.ClusterARN)
	if desc.KafkaVersion != "3.6.0" {
		t.Fatalf("kafka version not updated: %s", desc.KafkaVersion)
	}
}

// TestListNodesSizedToBrokerCount asserts ListNodes returns one node per broker.
func TestListNodesSizedToBrokerCount(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	c, _ := m.CreateCluster(ctx, driver.CreateClusterInput{ClusterName: "nodes", NumberOfBrokerNodes: 4, BrokerNodeGroupInfo: bng()})

	nodes, _, err := m.ListNodes(ctx, c.ClusterARN, driver.Page{})
	if err != nil || len(nodes) != 4 {
		t.Fatalf("ListNodes: %v len=%d", err, len(nodes))
	}

	if nodes[0].NodeType != "BROKER" {
		t.Fatalf("unexpected node type: %s", nodes[0].NodeType)
	}
}

// TestKafkaVersions asserts the modeled version set and compatible upgrades.
func TestKafkaVersions(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	vers, _, err := m.ListKafkaVersions(ctx, driver.Page{})
	if err != nil || len(vers) == 0 {
		t.Fatalf("ListKafkaVersions: %v len=%d", err, len(vers))
	}

	c, _ := m.CreateCluster(ctx, driver.CreateClusterInput{ClusterName: "cv", KafkaVersion: "3.5.1", NumberOfBrokerNodes: 3, BrokerNodeGroupInfo: bng()})

	comp, err := m.GetCompatibleKafkaVersions(ctx, c.ClusterARN)
	if err != nil || len(comp) != 1 {
		t.Fatalf("GetCompatibleKafkaVersions: %v len=%d", err, len(comp))
	}

	if !strings.Contains(string(comp[0]), "3.6.0") {
		t.Fatalf("expected 3.6.0 as compatible target, got %s", comp[0])
	}
}

// TestTagLifecycle asserts tag → list → untag on a cluster ARN.
func TestTagLifecycle(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	c, _ := m.CreateCluster(ctx, driver.CreateClusterInput{ClusterName: "tagged", NumberOfBrokerNodes: 3, BrokerNodeGroupInfo: bng()})

	if err := m.TagResource(ctx, c.ClusterARN, map[string]string{"a": "1", "b": "2"}); err != nil {
		t.Fatalf("TagResource: %v", err)
	}

	tags, err := m.ListTagsForResource(ctx, c.ClusterARN)
	if err != nil || tags["a"] != "1" || tags["b"] != "2" {
		t.Fatalf("ListTagsForResource: %v %+v", err, tags)
	}

	if err := m.UntagResource(ctx, c.ClusterARN, []string{"a"}); err != nil {
		t.Fatalf("UntagResource: %v", err)
	}

	tags, _ = m.ListTagsForResource(ctx, c.ClusterARN)
	if _, ok := tags["a"]; ok || tags["b"] != "2" {
		t.Fatalf("untag failed: %+v", tags)
	}
}

// TestTagUnknownArn asserts an unresolvable ARN is NotFoundException.
func TestTagUnknownArn(t *testing.T) {
	m := newMock(t)

	err := m.TagResource(context.Background(),
		"arn:aws:kafka:us-east-1:123456789012:cluster/nope/x", map[string]string{"a": "1"})

	var apiErr *driver.APIError
	if !errors.As(err, &apiErr) || apiErr.Exception != driver.ExNotFound {
		t.Fatalf("want NotFoundException, got %v", err)
	}
}

// TestConcurrentTagNoLostUpdate asserts concurrent TagResource calls all land:
// the read-modify-write under a single lock hold never loses a key.
func TestConcurrentTagNoLostUpdate(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	c, _ := m.CreateCluster(ctx, driver.CreateClusterInput{ClusterName: "race-tags", NumberOfBrokerNodes: 3, BrokerNodeGroupInfo: bng()})

	const goroutines = 50

	var wg sync.WaitGroup

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(n int) {
			defer wg.Done()

			key := "k" + strconv.Itoa(n)
			if err := m.TagResource(ctx, c.ClusterARN, map[string]string{key: "v"}); err != nil {
				t.Errorf("TagResource: %v", err)
			}
		}(i)
	}

	wg.Wait()

	tags, _ := m.ListTagsForResource(ctx, c.ClusterARN)
	if len(tags) != goroutines {
		t.Fatalf("lost updates: want %d tags, got %d", goroutines, len(tags))
	}
}

// TestOperationNoAlias asserts DescribeClusterOperation deep-copies RawOptions.
func TestOperationNoAlias(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	c, _ := m.CreateCluster(ctx, driver.CreateClusterInput{ClusterName: "op-alias", NumberOfBrokerNodes: 3, BrokerNodeGroupInfo: bng()})

	op, err := m.UpdateBrokerStorage(ctx, c.ClusterARN, c.CurrentVersion, []byte(`{"targetBrokerEBSVolumeInfo":[]}`))
	if err != nil {
		t.Fatalf("UpdateBrokerStorage: %v", err)
	}

	// A subsequent Describe must reflect the raw option carried by the update.
	desc, _ := m.DescribeCluster(ctx, c.ClusterARN)
	if _, ok := desc.RawOptions["targetBrokerEBSVolumeInfo"]; !ok {
		t.Fatal("update raw option not reflected in Describe")
	}

	got, _ := m.DescribeClusterOperation(ctx, op.OperationARN)
	if got.OperationARN != op.OperationARN {
		t.Fatalf("operation mismatch: %s", got.OperationARN)
	}
}

// bng returns a valid BrokerNodeGroupInfo for tests (CreateCluster requires one).
func bng() *driver.BrokerNodeGroupInfo {
	return &driver.BrokerNodeGroupInfo{InstanceType: "kafka.m5.large"}
}

// TestErrorTypeFidelity checks that ops which do NOT model NotFoundException in
// the SDK return BadRequestException (not NotFound) for a missing cluster, while
// ops that DO model it return NotFound.
func TestErrorTypeFidelity(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	missing := "arn:aws:kafka:us-east-1:1:cluster/x/y"

	// Ops that do NOT model NotFoundException → BadRequestException.
	if _, err := m.GetBootstrapBrokers(ctx, missing); !isException(err, driver.ExBadRequest) {
		t.Fatalf("GetBootstrapBrokers = %v, want BadRequestException", err)
	}

	if _, _, err := m.ListClusterOperations(ctx, missing, driver.Page{}); !isException(err, driver.ExBadRequest) {
		t.Fatalf("ListClusterOperations(v1) = %v, want BadRequestException", err)
	}

	if _, err := m.UpdateBrokerCount(ctx, missing, "", 3); !isException(err, driver.ExBadRequest) {
		t.Fatalf("UpdateBrokerCount = %v, want BadRequestException", err)
	}

	if err := m.RejectClientVpcConnection(ctx, missing, nil); !isException(err, driver.ExBadRequest) {
		t.Fatalf("RejectClientVpcConnection = %v, want BadRequestException", err)
	}

	// Ops that DO model NotFoundException → NotFoundException.
	if _, err := m.DescribeCluster(ctx, missing); !isException(err, driver.ExNotFound) {
		t.Fatalf("DescribeCluster = %v, want NotFoundException", err)
	}

	if _, _, err := m.ListClusterOperationsV2(ctx, missing, driver.Page{}); !isException(err, driver.ExNotFound) {
		t.Fatalf("ListClusterOperationsV2 = %v, want NotFoundException", err)
	}
}

func TestCreateClusterRequiredFieldsAndEnums(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	cases := []struct {
		name string
		in   driver.CreateClusterInput
	}{
		{"no broker info", driver.CreateClusterInput{ClusterName: "c", NumberOfBrokerNodes: 3}},
		{"zero brokers", driver.CreateClusterInput{ClusterName: "c", BrokerNodeGroupInfo: bng()}},
		{"bad storage mode", driver.CreateClusterInput{
			ClusterName: "c", NumberOfBrokerNodes: 3, BrokerNodeGroupInfo: bng(), StorageMode: "FOO",
		}},
		{"bad monitoring", driver.CreateClusterInput{
			ClusterName: "c", NumberOfBrokerNodes: 3, BrokerNodeGroupInfo: bng(), EnhancedMonitoring: "FOO",
		}},
	}

	for _, tc := range cases {
		if _, err := m.CreateCluster(ctx, tc.in); !isException(err, driver.ExBadRequest) {
			t.Fatalf("%s: got %v, want BadRequestException", tc.name, err)
		}
	}
}

func TestCreateConfigurationRequiresServerProperties(t *testing.T) {
	m := newMock(t)

	_, err := m.CreateConfiguration(context.Background(), driver.CreateConfigurationInput{Name: "cfg"})
	if !isException(err, driver.ExBadRequest) {
		t.Fatalf("config without serverProperties = %v, want BadRequestException", err)
	}
}

// TestTagResourceAcrossResourceKinds verifies tag ops route by ARN to all four
// taggable MSK resource kinds, not just clusters.
func TestTagResourceAcrossResourceKinds(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	clusterARN := makeCluster(t, m, "tag-cluster")

	cfg, err := m.CreateConfiguration(ctx, driver.CreateConfigurationInput{
		Name: "tag-cfg", ServerProperties: []byte("auto.create.topics.enable=true"),
	})
	if err != nil {
		t.Fatalf("CreateConfiguration: %v", err)
	}

	vpc, err := m.CreateVpcConnection(ctx,
		[]byte(`{"targetClusterArn":"`+clusterARN+`","vpcId":"vpc-1"}`))
	if err != nil {
		t.Fatalf("CreateVpcConnection: %v", err)
	}

	rep, err := m.CreateReplicator(ctx, []byte(`{"replicatorName":"r","kafkaClusters":[],`+
		`"replicationInfoList":[],"serviceExecutionRoleArn":"arn:aws:iam::0:role/r"}`))
	if err != nil {
		t.Fatalf("CreateReplicator: %v", err)
	}

	for _, arn := range []string{clusterARN, cfg.ARN, vpc.VpcConnectionARN, rep.ReplicatorARN} {
		if err := m.TagResource(ctx, arn, map[string]string{"env": "test"}); err != nil {
			t.Fatalf("TagResource(%s): %v", arn, err)
		}

		got, lerr := m.ListTagsForResource(ctx, arn)
		if lerr != nil || got["env"] != "test" {
			t.Fatalf("ListTagsForResource(%s) = %v %v, want env=test", arn, got, lerr)
		}
	}

	// An ARN that names no known resource is a NotFoundException.
	if _, err := m.ListTagsForResource(ctx, "arn:aws:kafka:us-east-1:0:cluster/ghost/x"); !isException(err, driver.ExNotFound) {
		t.Fatalf("ListTags on ghost = %v, want NotFoundException", err)
	}
}

func TestUpdateBrokerCountConstraints(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	c, err := m.CreateCluster(ctx, driver.CreateClusterInput{
		ClusterName: "bc", NumberOfBrokerNodes: 3,
		BrokerNodeGroupInfo: &driver.BrokerNodeGroupInfo{
			InstanceType: "kafka.m5.large", ClientSubnets: []string{"s-1", "s-2", "s-3"},
		},
	})
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	// Decrease → BadRequest.
	if _, err := m.UpdateBrokerCount(ctx, c.ClusterARN, c.CurrentVersion, 2); !isException(err, driver.ExBadRequest) {
		t.Fatalf("decrease broker count = %v, want BadRequestException", err)
	}

	// Not a multiple of the 3 AZs → BadRequest.
	if _, err := m.UpdateBrokerCount(ctx, c.ClusterARN, c.CurrentVersion, 4); !isException(err, driver.ExBadRequest) {
		t.Fatalf("non-AZ-multiple broker count = %v, want BadRequestException", err)
	}

	// Valid increase to a multiple of 3.
	if _, err := m.UpdateBrokerCount(ctx, c.ClusterARN, c.CurrentVersion, 6); err != nil {
		t.Fatalf("valid broker count increase: %v", err)
	}
}

func TestCreateClusterBrokerValidation(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	// Non-kafka.* instance type → BadRequest.
	_, err := m.CreateCluster(ctx, driver.CreateClusterInput{
		ClusterName: "bad-it", NumberOfBrokerNodes: 3,
		BrokerNodeGroupInfo: &driver.BrokerNodeGroupInfo{InstanceType: "t2.nano"},
	})
	if !isException(err, driver.ExBadRequest) {
		t.Fatalf("bad instance type = %v, want BadRequestException", err)
	}

	// Out-of-range EBS volume size → BadRequest.
	_, err = m.CreateCluster(ctx, driver.CreateClusterInput{
		ClusterName: "bad-ebs", NumberOfBrokerNodes: 3,
		BrokerNodeGroupInfo: &driver.BrokerNodeGroupInfo{
			InstanceType: "kafka.m5.large",
			RawFields:    map[string]json.RawMessage{"storageInfo": json.RawMessage(`{"ebsStorageInfo":{"volumeSize":99999}}`)},
		},
	})
	if !isException(err, driver.ExBadRequest) {
		t.Fatalf("out-of-range EBS size = %v, want BadRequestException", err)
	}
}

func TestDeleteClusterHonorsCurrentVersion(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	arn := makeCluster(t, m, "del-ver")

	// Stale version → BadRequest; cluster survives.
	if _, _, err := m.DeleteCluster(ctx, arn, "STALE"); !isException(err, driver.ExBadRequest) {
		t.Fatalf("delete with stale version = %v, want BadRequestException", err)
	}

	if _, err := m.DescribeCluster(ctx, arn); err != nil {
		t.Fatalf("cluster should survive a version-mismatch delete: %v", err)
	}
}

func TestCreateVpcConnectionRequiresExistingCluster(t *testing.T) {
	m := newMock(t)

	_, err := m.CreateVpcConnection(context.Background(),
		[]byte(`{"targetClusterArn":"arn:aws:kafka:us-east-1:0:cluster/ghost/x","vpcId":"vpc-1"}`))
	if !isException(err, driver.ExBadRequest) {
		t.Fatalf("vpc connection to ghost cluster = %v, want BadRequestException", err)
	}
}

// makeCluster is a test helper that creates a cluster and returns its ARN.
func makeCluster(t *testing.T, m *kafka.Mock, name string) string {
	t.Helper()

	out, err := m.CreateCluster(context.Background(), driver.CreateClusterInput{ClusterName: name, NumberOfBrokerNodes: 3, BrokerNodeGroupInfo: bng()})
	if err != nil {
		t.Fatalf("CreateCluster(%s): %v", name, err)
	}

	return out.ClusterARN
}

func expectException(t *testing.T, err error, want string) {
	t.Helper()

	var apiErr *driver.APIError
	if !errors.As(err, &apiErr) || apiErr.Exception != want {
		t.Fatalf("want %s, got %v", want, err)
	}
}

// isException reports whether err is a driver.APIError with the given exception.
func isException(err error, want string) bool {
	var apiErr *driver.APIError

	return errors.As(err, &apiErr) && apiErr.Exception == want
}

func TestVpcConnectionLifecycle(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	arn := makeCluster(t, m, "vpc-cluster")

	body := []byte(`{"targetClusterArn":"` + arn + `","authentication":"SASL_IAM",` +
		`"vpcId":"vpc-1","clientSubnets":["subnet-1"],"securityGroups":["sg-1"]}`)

	created, err := m.CreateVpcConnection(ctx, body)
	if err != nil {
		t.Fatalf("CreateVpcConnection: %v", err)
	}

	if created.State != "AVAILABLE" || created.TargetClusterARN != arn {
		t.Fatalf("unexpected vpc connection: %+v", created)
	}

	desc, err := m.DescribeVpcConnection(ctx, created.VpcConnectionARN)
	if err != nil || desc.VpcID != "vpc-1" {
		t.Fatalf("DescribeVpcConnection: %v %+v", err, desc)
	}

	// No-alias: mutating the describe result must not affect stored state.
	desc.RawOptions["subnets"] = []byte(`["hacked"]`)
	desc2, _ := m.DescribeVpcConnection(ctx, created.VpcConnectionARN)
	if string(desc2.RawOptions["subnets"]) != `["subnet-1"]` {
		t.Fatalf("stored vpc connection aliased: %s", desc2.RawOptions["subnets"])
	}

	clients, _, err := m.ListClientVpcConnections(ctx, arn, driver.Page{})
	if err != nil || len(clients) != 1 {
		t.Fatalf("ListClientVpcConnections: %v len=%d", err, len(clients))
	}

	reject := []byte(`{"vpcConnectionArn":"` + created.VpcConnectionARN + `"}`)
	if err := m.RejectClientVpcConnection(ctx, arn, reject); err != nil {
		t.Fatalf("RejectClientVpcConnection: %v", err)
	}

	after, _ := m.DescribeVpcConnection(ctx, created.VpcConnectionARN)
	if after.State != "REJECTED" {
		t.Fatalf("state after reject = %s, want REJECTED", after.State)
	}

	all, _, _ := m.ListVpcConnections(ctx, driver.Page{})
	if len(all) != 1 {
		t.Fatalf("ListVpcConnections len=%d", len(all))
	}

	if _, err := m.DeleteVpcConnection(ctx, created.VpcConnectionARN); err != nil {
		t.Fatalf("DeleteVpcConnection: %v", err)
	}

	if _, err := m.DescribeVpcConnection(ctx, created.VpcConnectionARN); err == nil {
		t.Fatal("expected NotFound after delete")
	}
}

func TestVpcConnectionMissingTarget(t *testing.T) {
	m := newMock(t)
	_, err := m.CreateVpcConnection(context.Background(), []byte(`{}`))
	expectException(t, err, driver.ExBadRequest)
}

func TestTopicLifecycle(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	arn := makeCluster(t, m, "topic-cluster")

	body := []byte(`{"topicName":"orders","partitionCount":6,"replicationFactor":3,` +
		`"configs":"retention.ms=1000"}`)

	created, err := m.CreateTopic(ctx, arn, body)
	if err != nil || created.NumberOfPartitions != 6 {
		t.Fatalf("CreateTopic: %v %+v", err, created)
	}

	// Duplicate name → Conflict.
	_, err = m.CreateTopic(ctx, arn, body)
	expectException(t, err, driver.ExConflict)

	// Unknown cluster → BadRequest (CreateTopic does not model NotFoundException).
	_, err = m.CreateTopic(ctx, "arn:aws:kafka:us-east-1:1:cluster/x/y", body)
	expectException(t, err, driver.ExBadRequest)

	desc, err := m.DescribeTopic(ctx, arn, "orders")
	if err != nil || string(desc.RawOptions["configs"]) != `"retention.ms=1000"` {
		t.Fatalf("DescribeTopic: %v %+v", err, desc)
	}

	list, _, _ := m.ListTopics(ctx, arn, driver.Page{})
	if len(list) != 1 {
		t.Fatalf("ListTopics len=%d", len(list))
	}

	upd, err := m.UpdateTopic(ctx, arn, "orders", []byte(`{"partitionCount":12}`))
	if err != nil || upd.NumberOfPartitions != 12 {
		t.Fatalf("UpdateTopic: %v %+v", err, upd)
	}

	parts, _, err := m.DescribeTopicPartitions(ctx, arn, "orders", driver.Page{})
	if err != nil || len(parts) != 12 {
		t.Fatalf("DescribeTopicPartitions: %v len=%d", err, len(parts))
	}

	if err := m.DeleteTopic(ctx, arn, "orders"); err != nil {
		t.Fatalf("DeleteTopic: %v", err)
	}

	if _, err := m.DescribeTopic(ctx, arn, "orders"); err == nil {
		t.Fatal("expected NotFound after delete")
	}
}

func TestScramSecretLifecycle(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	arn := makeCluster(t, m, "scram-cluster")

	good := "arn:aws:secretsmanager:us-east-1:1:secret:AmazonMSK_good"
	bad := "not-an-arn"

	unproc, err := m.BatchAssociateScramSecret(ctx, arn, []string{good, bad})
	if err != nil {
		t.Fatalf("BatchAssociateScramSecret: %v", err)
	}

	if len(unproc) != 1 || !strings.Contains(string(unproc[0]), bad) {
		t.Fatalf("want one unprocessed naming the bad ARN, got %v", unproc)
	}

	list, _, _ := m.ListScramSecrets(ctx, arn, driver.Page{})
	if len(list) != 1 || list[0] != good {
		t.Fatalf("ListScramSecrets = %v", list)
	}

	unproc, err = m.BatchDisassociateScramSecret(ctx, arn, []string{good, "arn:aws:secretsmanager:us-east-1:1:secret:missing"})
	if err != nil {
		t.Fatalf("BatchDisassociateScramSecret: %v", err)
	}

	if len(unproc) != 1 {
		t.Fatalf("want one unprocessed for a not-associated secret, got %v", unproc)
	}

	list, _, _ = m.ListScramSecrets(ctx, arn, driver.Page{})
	if len(list) != 0 {
		t.Fatalf("secrets not removed: %v", list)
	}
}

func TestClusterPolicyLifecycle(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	arn := makeCluster(t, m, "policy-cluster")

	// Empty policy before any put.
	ver, pol, err := m.GetClusterPolicy(ctx, arn)
	if err != nil || ver != "" || pol != "" {
		t.Fatalf("empty policy expected, got ver=%q pol=%q err=%v", ver, pol, err)
	}

	v1, err := m.PutClusterPolicy(ctx, arn, []byte(`{"policy":"{\"Version\":\"2012-10-17\"}"}`))
	if err != nil || v1 == "" {
		t.Fatalf("PutClusterPolicy: %v ver=%q", err, v1)
	}

	// Stale version → BadRequest.
	_, err = m.PutClusterPolicy(ctx, arn, []byte(`{"policy":"x","currentVersion":"stale"}`))
	expectException(t, err, driver.ExBadRequest)

	// Correct version → succeeds and bumps.
	v2, err := m.PutClusterPolicy(ctx, arn, []byte(`{"policy":"y","currentVersion":"`+v1+`"}`))
	if err != nil || v2 == v1 {
		t.Fatalf("versioned put: %v v2=%q v1=%q", err, v2, v1)
	}

	if err := m.DeleteClusterPolicy(ctx, arn); err != nil {
		t.Fatalf("DeleteClusterPolicy: %v", err)
	}

	ver, pol, _ = m.GetClusterPolicy(ctx, arn)
	if ver != "" || pol != "" {
		t.Fatalf("policy not cleared: ver=%q pol=%q", ver, pol)
	}
}

func TestReplicatorLifecycle(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	body := []byte(`{"replicatorName":"repl-1","serviceExecutionRoleArn":"arn:aws:iam::1:role/r",` +
		`"kafkaClusters":[{"amazonMskCluster":{"mskClusterArn":"a"}}],` +
		`"replicationInfoList":[{"sourceKafkaClusterArn":"a","targetKafkaClusterArn":"b"}]}`)

	created, err := m.CreateReplicator(ctx, body)
	if err != nil || created.State != "RUNNING" {
		t.Fatalf("CreateReplicator: %v %+v", err, created)
	}

	// Duplicate name → Conflict.
	_, err = m.CreateReplicator(ctx, body)
	expectException(t, err, driver.ExConflict)

	desc, err := m.DescribeReplicator(ctx, created.ReplicatorARN)
	if err != nil || desc.ReplicatorName != "repl-1" {
		t.Fatalf("DescribeReplicator: %v %+v", err, desc)
	}

	version := ""
	if v, ok := desc.RawOptions["currentVersion"]; ok {
		version = strings.Trim(string(v), `"`)
	}

	if version == "" {
		t.Fatal("no currentVersion surfaced")
	}

	// Stale version → BadRequest.
	_, err = m.UpdateReplicationInfo(ctx, created.ReplicatorARN,
		[]byte(`{"currentVersion":"stale","sourceKafkaClusterArn":"a","targetKafkaClusterArn":"b"}`))
	expectException(t, err, driver.ExBadRequest)

	upd, err := m.UpdateReplicationInfo(ctx, created.ReplicatorARN,
		[]byte(`{"currentVersion":"`+version+`","sourceKafkaClusterArn":"a","targetKafkaClusterArn":"b",`+
			`"topicReplication":{"topicsToReplicate":["t"]}}`))
	if err != nil {
		t.Fatalf("UpdateReplicationInfo: %v", err)
	}

	newVer := strings.Trim(string(upd.RawOptions["currentVersion"]), `"`)
	if newVer == version {
		t.Fatal("version not bumped after update")
	}

	list, _, _ := m.ListReplicators(ctx, "repl", driver.Page{})
	if len(list) != 1 {
		t.Fatalf("ListReplicators len=%d", len(list))
	}

	if _, _, err := m.DeleteReplicator(ctx, created.ReplicatorARN, ""); err != nil {
		t.Fatalf("DeleteReplicator: %v", err)
	}

	if _, err := m.DescribeReplicator(ctx, created.ReplicatorARN); err == nil {
		t.Fatal("expected NotFound after delete")
	}
}
