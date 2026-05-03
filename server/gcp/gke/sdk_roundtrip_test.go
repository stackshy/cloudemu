package gke_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/api/container/v1"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu"
	gkeprov "github.com/stackshy/cloudemu/providers/gcp/gke"
	gcpserver "github.com/stackshy/cloudemu/server/gcp"
)

func newSDKClient(t *testing.T) (*container.Service, string) {
	t.Helper()

	cloud := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.Drivers{
		GKE: cloud.GKE,
	})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	svc, err := container.NewService(context.Background(),
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("container.NewService: %v", err)
	}

	return svc, "mock-project"
}

func parent(project, location string) string {
	return "projects/" + project + "/locations/" + location
}

func clusterName(project, location, cluster string) string {
	return parent(project, location) + "/clusters/" + cluster
}

func nodePoolName(project, location, cluster, pool string) string {
	return clusterName(project, location, cluster) + "/nodePools/" + pool
}

func TestSDKGKECreateGetList(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()
	loc := "us-central1"

	op, err := svc.Projects.Locations.Clusters.Create(parent(project, loc), &container.CreateClusterRequest{
		Cluster: &container.Cluster{
			Name:             "prod",
			InitialNodeCount: 3,
		},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Clusters.Create: %v", err)
	}

	if op.Status != "DONE" {
		t.Fatalf("got op status %q, want DONE", op.Status)
	}

	got, err := svc.Projects.Locations.Clusters.Get(clusterName(project, loc, "prod")).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Clusters.Get: %v", err)
	}

	if got.Name != "prod" {
		t.Fatalf("got name %q, want prod", got.Name)
	}

	if got.Status != "RUNNING" {
		t.Fatalf("got status %q, want RUNNING", got.Status)
	}

	if !strings.Contains(got.Endpoint, gkeprov.StubEndpoint) {
		t.Fatalf("expected Wave-2 stub endpoint, got %q", got.Endpoint)
	}

	if got.MasterAuth == nil || got.MasterAuth.ClusterCaCertificate == "" {
		t.Fatal("expected non-empty stub clusterCaCertificate")
	}

	if len(got.NodePools) == 0 {
		t.Fatal("expected default node pool to be present")
	}

	list, err := svc.Projects.Locations.Clusters.List(parent(project, loc)).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Clusters.List: %v", err)
	}

	if len(list.Clusters) != 1 {
		t.Fatalf("got %d clusters, want 1", len(list.Clusters))
	}
}

func TestSDKGKEUpdateAndDelete(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()
	loc := "us-central1"

	if _, err := svc.Projects.Locations.Clusters.Create(parent(project, loc), &container.CreateClusterRequest{
		Cluster: &container.Cluster{Name: "alpha"},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := svc.Projects.Locations.Clusters.Update(clusterName(project, loc, "alpha"),
		&container.UpdateClusterRequest{
			Update: &container.ClusterUpdate{
				DesiredLoggingService:    "none",
				DesiredMonitoringService: "none",
			},
		}).Context(ctx).Do(); err != nil {
		t.Fatalf("update: %v", err)
	}

	if _, err := svc.Projects.Locations.Clusters.SetResourceLabels(clusterName(project, loc, "alpha"),
		&container.SetLabelsRequest{ResourceLabels: map[string]string{"env": "test"}}).
		Context(ctx).Do(); err != nil {
		t.Fatalf("setResourceLabels: %v", err)
	}

	got, err := svc.Projects.Locations.Clusters.Get(clusterName(project, loc, "alpha")).Context(ctx).Do()
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}

	if got.LoggingService != "none" {
		t.Fatalf("got logging %q, want none", got.LoggingService)
	}

	if got.ResourceLabels["env"] != "test" {
		t.Fatalf("got label env=%q, want test", got.ResourceLabels["env"])
	}

	if _, err := svc.Projects.Locations.Clusters.Delete(clusterName(project, loc, "alpha")).Context(ctx).Do(); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if _, err := svc.Projects.Locations.Clusters.Get(clusterName(project, loc, "alpha")).Context(ctx).Do(); err == nil {
		t.Fatal("expected NotFound after delete")
	}
}

//nolint:funlen // exercises every cluster-level :setX endpoint.
func TestSDKGKEClusterSetters(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()
	loc := "us-central1"
	cname := clusterName(project, loc, "tweaks")

	if _, err := svc.Projects.Locations.Clusters.Create(parent(project, loc), &container.CreateClusterRequest{
		Cluster: &container.Cluster{Name: "tweaks"},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("create: %v", err)
	}

	calls := []struct {
		name string
		fn   func() error
	}{
		{"setLogging", func() error {
			_, err := svc.Projects.Locations.Clusters.SetLogging(cname, &container.SetLoggingServiceRequest{
				LoggingService: "logging.googleapis.com/kubernetes",
			}).Context(ctx).Do()
			return err
		}},
		{"setMonitoring", func() error {
			_, err := svc.Projects.Locations.Clusters.SetMonitoring(cname, &container.SetMonitoringServiceRequest{
				MonitoringService: "monitoring.googleapis.com/kubernetes",
			}).Context(ctx).Do()
			return err
		}},
		{"setMasterAuth", func() error {
			_, err := svc.Projects.Locations.Clusters.SetMasterAuth(cname, &container.SetMasterAuthRequest{
				Action: "SET_USERNAME",
				Update: &container.MasterAuth{Username: "admin"},
			}).Context(ctx).Do()
			return err
		}},
		{"setLegacyAbac", func() error {
			_, err := svc.Projects.Locations.Clusters.SetLegacyAbac(cname, &container.SetLegacyAbacRequest{
				Enabled: true,
			}).Context(ctx).Do()
			return err
		}},
		{"setNetworkPolicy", func() error {
			_, err := svc.Projects.Locations.Clusters.SetNetworkPolicy(cname, &container.SetNetworkPolicyRequest{
				NetworkPolicy: &container.NetworkPolicy{Enabled: true},
			}).Context(ctx).Do()
			return err
		}},
		{"setMaintenancePolicy", func() error {
			_, err := svc.Projects.Locations.Clusters.SetMaintenancePolicy(cname, &container.SetMaintenancePolicyRequest{
				MaintenancePolicy: &container.MaintenancePolicy{
					Window: &container.MaintenanceWindow{
						DailyMaintenanceWindow: &container.DailyMaintenanceWindow{StartTime: "03:00"},
					},
				},
			}).Context(ctx).Do()
			return err
		}},
		{"setResourceLabels", func() error {
			_, err := svc.Projects.Locations.Clusters.SetResourceLabels(cname, &container.SetLabelsRequest{
				ResourceLabels: map[string]string{"team": "core"},
			}).Context(ctx).Do()
			return err
		}},
		{"startIpRotation", func() error {
			_, err := svc.Projects.Locations.Clusters.StartIpRotation(cname, &container.StartIPRotationRequest{}).Context(ctx).Do()
			return err
		}},
		{"completeIpRotation", func() error {
			_, err := svc.Projects.Locations.Clusters.CompleteIpRotation(cname, &container.CompleteIPRotationRequest{}).Context(ctx).Do()
			return err
		}},
	}

	for _, c := range calls {
		if err := c.fn(); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
	}
}

func TestSDKGKENodePools(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()
	loc := "us-central1"
	parentClus := clusterName(project, loc, "host")

	if _, err := svc.Projects.Locations.Clusters.Create(parent(project, loc), &container.CreateClusterRequest{
		Cluster: &container.Cluster{Name: "host"},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("create cluster: %v", err)
	}

	if _, err := svc.Projects.Locations.Clusters.NodePools.Create(parentClus, &container.CreateNodePoolRequest{
		NodePool: &container.NodePool{
			Name:             "extra",
			InitialNodeCount: 2,
			Config:           &container.NodeConfig{MachineType: "e2-standard-4", DiskSizeGb: 50},
		},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("nodepool create: %v", err)
	}

	got, err := svc.Projects.Locations.Clusters.NodePools.Get(nodePoolName(project, loc, "host", "extra")).
		Context(ctx).Do()
	if err != nil {
		t.Fatalf("nodepool get: %v", err)
	}

	if got.Name != "extra" {
		t.Fatalf("got %q, want extra", got.Name)
	}

	if got.InitialNodeCount != 2 {
		t.Fatalf("got count %d, want 2", got.InitialNodeCount)
	}

	list, err := svc.Projects.Locations.Clusters.NodePools.List(parentClus).Context(ctx).Do()
	if err != nil {
		t.Fatalf("nodepool list: %v", err)
	}

	if len(list.NodePools) != 2 { // default-pool + extra
		t.Fatalf("got %d pools, want 2", len(list.NodePools))
	}

	npFull := nodePoolName(project, loc, "host", "extra")

	if _, err := svc.Projects.Locations.Clusters.NodePools.SetSize(npFull, &container.SetNodePoolSizeRequest{
		NodeCount: 5,
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("setSize: %v", err)
	}

	if _, err := svc.Projects.Locations.Clusters.NodePools.SetAutoscaling(npFull, &container.SetNodePoolAutoscalingRequest{
		Autoscaling: &container.NodePoolAutoscaling{Enabled: true, MinNodeCount: 1, MaxNodeCount: 10},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("setAutoscaling: %v", err)
	}

	if _, err := svc.Projects.Locations.Clusters.NodePools.SetManagement(npFull, &container.SetNodePoolManagementRequest{
		Management: &container.NodeManagement{AutoUpgrade: false, AutoRepair: false},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("setManagement: %v", err)
	}

	if _, err := svc.Projects.Locations.Clusters.NodePools.Update(npFull, &container.UpdateNodePoolRequest{
		NodeVersion: "1.31.0-gke.0",
		MachineType: "e2-standard-8",
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("update: %v", err)
	}

	if _, err := svc.Projects.Locations.Clusters.NodePools.Rollback(npFull, &container.RollbackNodePoolUpgradeRequest{}).
		Context(ctx).Do(); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	if _, err := svc.Projects.Locations.Clusters.NodePools.Delete(npFull).Context(ctx).Do(); err != nil {
		t.Fatalf("delete: %v", err)
	}
}

func TestSDKGKEOperations(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()
	loc := "us-central1"

	op, err := svc.Projects.Locations.Clusters.Create(parent(project, loc), &container.CreateClusterRequest{
		Cluster: &container.Cluster{Name: "prod"},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := svc.Projects.Locations.Operations.Get(parent(project, loc) + "/operations/" + op.Name).
		Context(ctx).Do()
	if err != nil {
		t.Fatalf("operations.get: %v", err)
	}

	if got.Status != "DONE" {
		t.Fatalf("got status %q, want DONE", got.Status)
	}

	list, err := svc.Projects.Locations.Operations.List(parent(project, loc)).Context(ctx).Do()
	if err != nil {
		t.Fatalf("operations.list: %v", err)
	}

	if len(list.Operations) == 0 {
		t.Fatal("expected at least one operation")
	}

	if _, err := svc.Projects.Locations.Operations.Cancel(parent(project, loc)+"/operations/"+op.Name,
		&container.CancelOperationRequest{}).Context(ctx).Do(); err != nil {
		t.Fatalf("operations.cancel: %v", err)
	}
}
