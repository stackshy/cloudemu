package managedkafka_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"google.golang.org/api/googleapi"
	mk "google.golang.org/api/managedkafka/v1"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

const (
	project  = "mock-project"
	location = "us-central1"
	parent   = "projects/" + project + "/locations/" + location
	subnet   = "projects/" + project + "/regions/" + location + "/subnetworks/default"
	gib      = int64(1) << 30

	maxPolls = 5
)

//nolint:gochecknoglobals // fixed test clock origin
var epoch = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

func newSDKClient(t *testing.T) (*mk.Service, *config.FakeClock) {
	t.Helper()

	clock := config.NewFakeClock(epoch)
	srv := gcpserver.NewFromProvider(cloudemu.NewGCP(config.WithClock(clock), config.WithProjectID(project)))

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	svc, err := mk.NewService(context.Background(),
		option.WithEndpoint(ts.URL+"/"),
		option.WithoutAuthentication(),
		option.WithHTTPClient(ts.Client()),
	)
	if err != nil {
		t.Fatalf("managedkafka.NewService: %v", err)
	}

	return svc, clock
}

func validCluster() *mk.Cluster {
	return &mk.Cluster{
		CapacityConfig: &mk.CapacityConfig{VcpuCount: 3, MemoryBytes: 3 * gib},
		GcpConfig: &mk.GcpConfig{
			AccessConfig: &mk.AccessConfig{NetworkConfigs: []*mk.NetworkConfig{{Subnet: subnet}}},
			KmsKey:       "projects/" + project + "/locations/" + location + "/keyRings/kr/cryptoKeys/k",
		},
		RebalanceConfig: &mk.RebalanceConfig{Mode: "AUTO_REBALANCE_ON_SCALE_UP"},
		Labels:          map[string]string{"env": "test"},
	}
}

// waitOp polls an operation through the shared LRO route until done, as a real
// client does, and fails if it never completes.
func waitOp(t *testing.T, svc *mk.Service, op *mk.Operation) *mk.Operation {
	t.Helper()

	for range maxPolls {
		got, err := svc.Projects.Locations.Operations.Get(op.Name).Do()
		if err != nil {
			t.Fatalf("Operations.Get(%s): %v", op.Name, err)
		}

		if got.Done {
			if got.Error != nil {
				t.Fatalf("operation %s failed: %+v", op.Name, got.Error)
			}

			return got
		}
	}

	t.Fatalf("operation %s never completed", op.Name)

	return nil
}

func wantCode(t *testing.T, err error, code int, what string) {
	t.Helper()

	var gerr *googleapi.Error
	if !errors.As(err, &gerr) {
		t.Fatalf("%s: want HTTP %d, got %v", what, code, err)
	}

	if gerr.Code != code {
		t.Fatalf("%s: HTTP %d (%s), want %d", what, gerr.Code, gerr.Message, code)
	}
}

func createCluster(t *testing.T, svc *mk.Service, id string) {
	t.Helper()

	op, err := svc.Projects.Locations.Clusters.Create(parent, validCluster()).ClusterId(id).Do()
	if err != nil {
		t.Fatalf("Clusters.Create(%s): %v", id, err)
	}

	waitOp(t, svc, op)
}

func TestSDKClusterLifecycle(t *testing.T) {
	svc, clock := newSDKClient(t)
	name := parent + "/clusters/kc"

	op, err := svc.Projects.Locations.Clusters.Create(parent, validCluster()).ClusterId("kc").Do()
	if err != nil {
		t.Fatalf("Clusters.Create: %v", err)
	}

	done := waitOp(t, svc, op)
	if len(done.Response) == 0 {
		t.Fatalf("create operation carries no response")
	}

	got, err := svc.Projects.Locations.Clusters.Get(name).Do()
	if err != nil {
		t.Fatalf("Clusters.Get: %v", err)
	}

	if got.Name != name || got.State != "ACTIVE" {
		t.Fatalf("name/state = %q/%q", got.Name, got.State)
	}

	if got.CapacityConfig.VcpuCount != 3 || got.CapacityConfig.MemoryBytes != 3*gib {
		t.Fatalf("capacity = %+v", got.CapacityConfig)
	}

	if got.GcpConfig.AccessConfig.NetworkConfigs[0].Subnet != subnet || got.GcpConfig.KmsKey == "" {
		t.Fatalf("gcpConfig = %+v", got.GcpConfig)
	}

	if got.RebalanceConfig.Mode != "AUTO_REBALANCE_ON_SCALE_UP" || got.Labels["env"] != "test" {
		t.Fatalf("rebalance/labels = %+v %+v", got.RebalanceConfig, got.Labels)
	}

	wantTime := epoch.Format(time.RFC3339Nano)
	if got.CreateTime != wantTime || got.UpdateTime != wantTime {
		t.Fatalf("times = %s/%s, want %s", got.CreateTime, got.UpdateTime, wantTime)
	}

	// Masked patch: only memoryBytes changes; vcpuCount, labels, subnet untouched.
	clock.Advance(time.Hour)

	patch := &mk.Cluster{
		CapacityConfig: &mk.CapacityConfig{VcpuCount: 99, MemoryBytes: 12 * gib},
		Labels:         map[string]string{"ignored": "yes"},
	}

	pop, err := svc.Projects.Locations.Clusters.Patch(name, patch).UpdateMask("capacityConfig.memoryBytes").Do()
	if err != nil {
		t.Fatalf("Clusters.Patch: %v", err)
	}

	waitOp(t, svc, pop)

	updated, err := svc.Projects.Locations.Clusters.Get(name).Do()
	if err != nil {
		t.Fatalf("Get after patch: %v", err)
	}

	if updated.CapacityConfig.VcpuCount != 3 || updated.CapacityConfig.MemoryBytes != 12*gib {
		t.Fatalf("capacity after masked patch = %+v", updated.CapacityConfig)
	}

	if updated.Labels["env"] != "test" || updated.Labels["ignored"] != "" {
		t.Fatalf("unmasked labels changed: %+v", updated.Labels)
	}

	if updated.CreateTime != wantTime || updated.UpdateTime != epoch.Add(time.Hour).Format(time.RFC3339Nano) {
		t.Fatalf("times after patch = %s/%s", updated.CreateTime, updated.UpdateTime)
	}

	// Labels patch.
	lop, err := svc.Projects.Locations.Clusters.Patch(name, &mk.Cluster{Labels: map[string]string{"team": "data"}}).
		UpdateMask("labels").Do()
	if err != nil {
		t.Fatalf("labels patch: %v", err)
	}

	waitOp(t, svc, lop)

	relabeled, err := svc.Projects.Locations.Clusters.Get(name).Do()
	if err != nil {
		t.Fatalf("Get after labels patch: %v", err)
	}

	if len(relabeled.Labels) != 1 || relabeled.Labels["team"] != "data" {
		t.Fatalf("labels after patch = %+v", relabeled.Labels)
	}

	dop, err := svc.Projects.Locations.Clusters.Delete(name).Do()
	if err != nil {
		t.Fatalf("Clusters.Delete: %v", err)
	}

	waitOp(t, svc, dop)

	_, err = svc.Projects.Locations.Clusters.Get(name).Do()
	wantCode(t, err, http.StatusNotFound, "Get after delete")
}

func TestSDKClusterListPagination(t *testing.T) {
	svc, _ := newSDKClient(t)

	for _, id := range []string{"c-a", "c-b", "c-c"} {
		createCluster(t, svc, id)
	}

	first, err := svc.Projects.Locations.Clusters.List(parent).PageSize(2).Do()
	if err != nil {
		t.Fatalf("List page 1: %v", err)
	}

	if len(first.Clusters) != 2 || first.NextPageToken == "" {
		t.Fatalf("page 1 = %d clusters, token %q", len(first.Clusters), first.NextPageToken)
	}

	second, err := svc.Projects.Locations.Clusters.List(parent).PageSize(2).PageToken(first.NextPageToken).Do()
	if err != nil {
		t.Fatalf("List page 2: %v", err)
	}

	if len(second.Clusters) != 1 || second.NextPageToken != "" || second.Clusters[0].Name != parent+"/clusters/c-c" {
		t.Fatalf("page 2 = %+v token %q", second.Clusters, second.NextPageToken)
	}
}

func TestSDKClusterValidation(t *testing.T) {
	svc, _ := newSDKClient(t)

	mutate := func(fn func(c *mk.Cluster)) *mk.Cluster {
		c := validCluster()
		fn(c)

		return c
	}

	cases := []struct {
		name string
		id   string
		body *mk.Cluster
	}{
		{"vcpu below 3", "v1", mutate(func(c *mk.Cluster) { c.CapacityConfig = &mk.CapacityConfig{VcpuCount: 2, MemoryBytes: 2 * gib} })},
		{"memory below 1GiB per vcpu", "v2", mutate(func(c *mk.Cluster) { c.CapacityConfig.MemoryBytes = 3*gib - 1 })},
		{"memory above 8GiB per vcpu", "v3", mutate(func(c *mk.Cluster) { c.CapacityConfig.MemoryBytes = 24*gib + 1 })},
		{"no network configs", "v4", mutate(func(c *mk.Cluster) { c.GcpConfig.AccessConfig.NetworkConfigs = nil })},
		{"empty subnet", "v5", mutate(func(c *mk.Cluster) { c.GcpConfig.AccessConfig.NetworkConfigs[0].Subnet = "" })},
		{"missing clusterId", "", validCluster()},
		{"bad clusterId", "Bad_ID", validCluster()},
		{"bad rebalance mode", "v6", mutate(func(c *mk.Cluster) { c.RebalanceConfig.Mode = "SOMETIMES" })},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			call := svc.Projects.Locations.Clusters.Create(parent, tc.body)
			if tc.id != "" {
				call = call.ClusterId(tc.id)
			}

			_, err := call.Do()
			wantCode(t, err, http.StatusBadRequest, tc.name)
		})
	}

	// Inclusive bounds are accepted: 1 GiB and 8 GiB per vCPU.
	for id, mem := range map[string]int64{"lo": 3 * gib, "hi": 24 * gib} {
		c := validCluster()
		c.CapacityConfig.MemoryBytes = mem

		op, err := svc.Projects.Locations.Clusters.Create(parent, c).ClusterId(id).Do()
		if err != nil {
			t.Fatalf("boundary memory %d rejected: %v", mem, err)
		}

		waitOp(t, svc, op)
	}
}

func TestSDKClusterConflictsAndMasks(t *testing.T) {
	svc, _ := newSDKClient(t)
	name := parent + "/clusters/dup"

	createCluster(t, svc, "dup")

	_, err := svc.Projects.Locations.Clusters.Create(parent, validCluster()).ClusterId("dup").Do()
	wantCode(t, err, http.StatusConflict, "duplicate create")

	_, err = svc.Projects.Locations.Clusters.Get(parent + "/clusters/ghost").Do()
	wantCode(t, err, http.StatusNotFound, "get missing")

	_, err = svc.Projects.Locations.Clusters.Delete(parent + "/clusters/ghost").Do()
	wantCode(t, err, http.StatusNotFound, "delete missing")

	badMasks := []string{"", "bogusField", "state", "createTime", "gcpConfig.kmsKey", "name"}
	for _, mask := range badMasks {
		call := svc.Projects.Locations.Clusters.Patch(name, validCluster())
		if mask != "" {
			call = call.UpdateMask(mask)
		}

		_, err := call.Do()
		wantCode(t, err, http.StatusBadRequest, "patch mask "+mask)
	}

	// A masked patch that breaks the capacity rule is rejected and leaves the
	// cluster unchanged.
	_, err = svc.Projects.Locations.Clusters.Patch(name, &mk.Cluster{
		CapacityConfig: &mk.CapacityConfig{MemoryBytes: 100 * gib},
	}).UpdateMask("capacityConfig.memoryBytes").Do()
	wantCode(t, err, http.StatusBadRequest, "patch memory out of range")

	got, err := svc.Projects.Locations.Clusters.Get(name).Do()
	if err != nil || got.CapacityConfig.MemoryBytes != 3*gib {
		t.Fatalf("cluster changed by rejected patch: %+v %v", got, err)
	}
}

func TestSDKTopicLifecycle(t *testing.T) {
	svc, _ := newSDKClient(t)
	clusterName := parent + "/clusters/tc"
	topics := svc.Projects.Locations.Clusters.Topics

	createCluster(t, svc, "tc")

	created, err := topics.Create(clusterName, &mk.Topic{
		PartitionCount: 3, ReplicationFactor: 3, Configs: map[string]string{"cleanup.policy": "compact"},
	}).TopicId("orders").Do()
	if err != nil {
		t.Fatalf("Topics.Create: %v", err)
	}

	topicName := clusterName + "/topics/orders"
	if created.Name != topicName || created.PartitionCount != 3 || created.ReplicationFactor != 3 {
		t.Fatalf("created topic = %+v", created)
	}

	_, err = topics.Create(clusterName, &mk.Topic{PartitionCount: 1, ReplicationFactor: 1}).TopicId("orders").Do()
	wantCode(t, err, http.StatusConflict, "duplicate topic")

	for _, bad := range []*mk.Topic{{PartitionCount: 0, ReplicationFactor: 3}, {PartitionCount: 3, ReplicationFactor: 0}} {
		_, err = topics.Create(clusterName, bad).TopicId("bad").Do()
		wantCode(t, err, http.StatusBadRequest, "invalid topic counts")
	}

	if _, err = topics.Create(clusterName, &mk.Topic{PartitionCount: 1, ReplicationFactor: 1}).TopicId("audit").Do(); err != nil {
		t.Fatalf("second topic: %v", err)
	}

	list, err := topics.List(clusterName).PageSize(1).Do()
	if err != nil || len(list.Topics) != 1 || list.NextPageToken == "" || list.Topics[0].Name != clusterName+"/topics/audit" {
		t.Fatalf("topic list page 1 = %+v %v", list, err)
	}

	// partitionCount may only increase; replicationFactor is immutable.
	grown, err := topics.Patch(topicName, &mk.Topic{PartitionCount: 6, ReplicationFactor: 9}).UpdateMask("partitionCount").Do()
	if err != nil {
		t.Fatalf("grow partitions: %v", err)
	}

	if grown.PartitionCount != 6 || grown.ReplicationFactor != 3 || grown.Configs["cleanup.policy"] != "compact" {
		t.Fatalf("topic after masked patch = %+v", grown)
	}

	_, err = topics.Patch(topicName, &mk.Topic{PartitionCount: 2}).UpdateMask("partitionCount").Do()
	wantCode(t, err, http.StatusBadRequest, "shrink partitions")

	_, err = topics.Patch(topicName, &mk.Topic{ReplicationFactor: 1}).UpdateMask("replicationFactor").Do()
	wantCode(t, err, http.StatusBadRequest, "patch immutable replicationFactor")

	if _, err = topics.Delete(topicName).Do(); err != nil {
		t.Fatalf("Topics.Delete: %v", err)
	}

	_, err = topics.Get(topicName).Do()
	wantCode(t, err, http.StatusNotFound, "topic get after delete")
}

func TestSDKTopicsFollowCluster(t *testing.T) {
	svc, _ := newSDKClient(t)
	clusterName := parent + "/clusters/gone"
	topics := svc.Projects.Locations.Clusters.Topics

	_, err := topics.Create(clusterName, &mk.Topic{PartitionCount: 1, ReplicationFactor: 1}).TopicId("t").Do()
	wantCode(t, err, http.StatusNotFound, "topic create under missing cluster")

	_, err = topics.List(clusterName).Do()
	wantCode(t, err, http.StatusNotFound, "topic list under missing cluster")

	createCluster(t, svc, "gone")

	if _, err = topics.Create(clusterName, &mk.Topic{PartitionCount: 1, ReplicationFactor: 1}).TopicId("t").Do(); err != nil {
		t.Fatalf("topic create: %v", err)
	}

	dop, err := svc.Projects.Locations.Clusters.Delete(clusterName).Do()
	if err != nil {
		t.Fatalf("cluster delete: %v", err)
	}

	waitOp(t, svc, dop)

	// Recreate the cluster: the old topic must not resurface.
	createCluster(t, svc, "gone")

	_, err = topics.Get(clusterName + "/topics/t").Do()
	wantCode(t, err, http.StatusNotFound, "topic after cluster delete")
}
