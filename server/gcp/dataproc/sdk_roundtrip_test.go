package dataproc_test

import (
	"context"
	"net/http/httptest"
	"testing"

	dp "google.golang.org/api/dataproc/v1"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

func newSDKClient(t *testing.T) (*dp.Service, string) {
	t.Helper()

	cloud := cloudemu.NewGCP()
	srv := gcpserver.NewFromProvider(cloud)

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	svc, err := dp.NewService(context.Background(),
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("dataproc.NewService: %v", err)
	}

	return svc, "mock-project"
}

func TestSDKDataprocLifecycle(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()
	region := "us-central1"

	want := &dp.Cluster{
		ClusterName: "analytics",
		Labels:      map[string]string{"env": "test"},
		Config: &dp.ClusterConfig{
			GceClusterConfig: &dp.GceClusterConfig{ZoneUri: "us-central1-a", InternalIpOnly: true},
			MasterConfig:     &dp.InstanceGroupConfig{NumInstances: 1, MachineTypeUri: "n1-standard-4"},
			WorkerConfig:     &dp.InstanceGroupConfig{NumInstances: 2, MachineTypeUri: "n1-standard-4"},
			SoftwareConfig:   &dp.SoftwareConfig{ImageVersion: "2.1-debian11"},
		},
	}

	// Create -> completed LRO.
	op, err := svc.Projects.Regions.Clusters.Create(project, region, want).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Clusters.Create: %v", err)
	}

	if !op.Done {
		t.Fatalf("create operation not done")
	}

	// Poll the region-scoped operation (own endpoint, not the shared LRO poller).
	polled, err := svc.Projects.Regions.Operations.Get(op.Name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Operations.Get: %v", err)
	}

	if !polled.Done {
		t.Fatalf("polled operation not done")
	}

	// Get -> RUNNING, uuid, deep config round-trip.
	got, err := svc.Projects.Regions.Clusters.Get(project, region, "analytics").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Clusters.Get: %v", err)
	}

	if got.Status == nil || got.Status.State != "RUNNING" {
		t.Fatalf("cluster not RUNNING: %+v", got.Status)
	}

	if got.ClusterUuid == "" {
		t.Fatalf("clusterUuid empty")
	}

	if got.Config.WorkerConfig.NumInstances != 2 {
		t.Fatalf("worker num = %d, want 2", got.Config.WorkerConfig.NumInstances)
	}

	if got.Config.SoftwareConfig.ImageVersion != "2.1-debian11" {
		t.Fatalf("imageVersion = %q", got.Config.SoftwareConfig.ImageVersion)
	}

	if !got.Config.GceClusterConfig.InternalIpOnly {
		t.Fatalf("internalIpOnly not round-tripped")
	}

	if got.Labels["env"] != "test" {
		t.Fatalf("labels not round-tripped")
	}

	// List.
	list, err := svc.Projects.Regions.Clusters.List(project, region).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Clusters.List: %v", err)
	}

	if len(list.Clusters) != 1 || list.Clusters[0].ClusterName != "analytics" {
		t.Fatalf("list = %+v", list.Clusters)
	}

	// Patch worker num_instances -> completed LRO, applied.
	patchOp, err := svc.Projects.Regions.Clusters.Patch(project, region, "analytics", &dp.Cluster{
		Config: &dp.ClusterConfig{WorkerConfig: &dp.InstanceGroupConfig{NumInstances: 4}},
	}).UpdateMask("config.worker_config.num_instances").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Clusters.Patch: %v", err)
	}

	if !patchOp.Done {
		t.Fatalf("patch operation not done")
	}

	updated, err := svc.Projects.Regions.Clusters.Get(project, region, "analytics").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Clusters.Get after patch: %v", err)
	}

	if updated.Config.WorkerConfig.NumInstances != 4 {
		t.Fatalf("worker num after patch = %d, want 4", updated.Config.WorkerConfig.NumInstances)
	}

	// Delete -> completed LRO, then a Get 404s.
	delOp, err := svc.Projects.Regions.Clusters.Delete(project, region, "analytics").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Clusters.Delete: %v", err)
	}

	if !delOp.Done {
		t.Fatalf("delete operation not done")
	}

	if _, err := svc.Projects.Regions.Clusters.Get(project, region, "analytics").Context(ctx).Do(); err == nil {
		t.Fatalf("expected 404 after delete")
	}
}

func TestSDKDataprocGetNotFound(t *testing.T) {
	svc, project := newSDKClient(t)

	_, err := svc.Projects.Regions.Clusters.Get(project, "us-central1", "ghost").Do()
	if err == nil {
		t.Fatalf("expected NOT_FOUND error")
	}
}
