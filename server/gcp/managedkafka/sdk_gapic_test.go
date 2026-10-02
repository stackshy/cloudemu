package managedkafka_test

import (
	"context"
	"net/http/httptest"
	"testing"

	mkapi "cloud.google.com/go/managedkafka/apiv1"
	"cloud.google.com/go/managedkafka/apiv1/managedkafkapb"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

// newGAPICClient returns the official cloud.google.com/go/managedkafka REST
// client against a full assembled server. The GAPIC REST transport marshals
// enums as numbers ($alt=json;enum-encoding=int, protojson UseEnumNumbers), the
// shape the discovery client never sends.
func newGAPICClient(t *testing.T) *mkapi.Client {
	t.Helper()

	srv := gcpserver.NewFromProvider(cloudemu.NewGCP(config.WithClock(config.NewFakeClock(epoch)),
		config.WithProjectID(project)))

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	c, err := mkapi.NewRESTClient(context.Background(),
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
		option.WithHTTPClient(ts.Client()),
	)
	if err != nil {
		t.Fatalf("managedkafka.NewRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = c.Close() })

	return c
}

func gapicCluster(mode managedkafkapb.RebalanceConfig_Mode) *managedkafkapb.Cluster {
	return &managedkafkapb.Cluster{
		CapacityConfig: &managedkafkapb.CapacityConfig{VcpuCount: 3, MemoryBytes: 3 * gib},
		PlatformConfig: &managedkafkapb.Cluster_GcpConfig{GcpConfig: &managedkafkapb.GcpConfig{
			AccessConfig: &managedkafkapb.AccessConfig{
				NetworkConfigs: []*managedkafkapb.NetworkConfig{{Subnet: subnet}},
			},
		}},
		RebalanceConfig: &managedkafkapb.RebalanceConfig{Mode: mode},
		Labels:          map[string]string{"env": "gapic"},
	}
}

// TestGAPICClusterWithNumericEnums drives the official GAPIC REST client
// through create (Wait), get, list, update of a fetched cluster (which sends
// its output-only state back as a number) and delete (Wait). On the unfixed
// wire, CreateCluster with a RebalanceConfig 400s "cannot unmarshal number into
// ... rebalanceConfig.mode".
func TestGAPICClusterWithNumericEnums(t *testing.T) {
	ctx := context.Background()
	c := newGAPICClient(t)

	op, err := c.CreateCluster(ctx, &managedkafkapb.CreateClusterRequest{
		Parent:    parent,
		ClusterId: "gapic1",
		Cluster:   gapicCluster(managedkafkapb.RebalanceConfig_AUTO_REBALANCE_ON_SCALE_UP),
	})
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	created, err := op.Wait(ctx)
	if err != nil {
		t.Fatalf("CreateCluster Wait: %v", err)
	}

	if created.GetRebalanceConfig().GetMode() != managedkafkapb.RebalanceConfig_AUTO_REBALANCE_ON_SCALE_UP ||
		created.GetState() != managedkafkapb.Cluster_ACTIVE || created.GetName() != parent+"/clusters/gapic1" {
		t.Fatalf("created = %v", created)
	}

	meta, err := op.Metadata()
	if err != nil || meta.GetVerb() != "create" || meta.GetTarget() != parent+"/clusters/gapic1" ||
		meta.GetApiVersion() != "v1" || meta.GetCreateTime() == nil || meta.GetEndTime() == nil {
		t.Fatalf("create metadata = %v, %v", meta, err)
	}

	got, err := c.GetCluster(ctx, &managedkafkapb.GetClusterRequest{Name: created.GetName()})
	if err != nil {
		t.Fatalf("GetCluster: %v", err)
	}

	// A cluster created without a rebalanceConfig reports the NO_REBALANCE
	// default; MODE_UNSPECIFIED (0) is omitted on the wire, so it defaults too.
	defOp, err := c.CreateCluster(ctx, &managedkafkapb.CreateClusterRequest{
		Parent: parent, ClusterId: "gapic2",
		Cluster: gapicCluster(managedkafkapb.RebalanceConfig_MODE_UNSPECIFIED),
	})
	if err != nil {
		t.Fatalf("CreateCluster default mode: %v", err)
	}

	def, err := defOp.Wait(ctx)
	if err != nil || def.GetRebalanceConfig().GetMode() != managedkafkapb.RebalanceConfig_NO_REBALANCE {
		t.Fatalf("default mode = %v, %v", def.GetRebalanceConfig(), err)
	}

	it := c.ListClusters(ctx, &managedkafkapb.ListClustersRequest{Parent: parent})

	var names []string

	for {
		cl, err := it.Next()
		if err == iterator.Done {
			break
		}

		if err != nil {
			t.Fatalf("ListClusters: %v", err)
		}

		names = append(names, cl.GetName())
	}

	if len(names) != 2 {
		t.Fatalf("ListClusters = %v, want 2", names)
	}

	// Round-trip the fetched cluster (state is set, sent as a number) with a
	// numeric rebalance mode change.
	got.RebalanceConfig.Mode = managedkafkapb.RebalanceConfig_NO_REBALANCE
	got.Labels = map[string]string{"env": "updated"}

	upOp, err := c.UpdateCluster(ctx, &managedkafkapb.UpdateClusterRequest{
		Cluster:    got,
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"rebalance_config", "labels"}},
	})
	if err != nil {
		t.Fatalf("UpdateCluster: %v", err)
	}

	updated, err := upOp.Wait(ctx)
	if err != nil || updated.GetRebalanceConfig().GetMode() != managedkafkapb.RebalanceConfig_NO_REBALANCE ||
		updated.GetLabels()["env"] != "updated" {
		t.Fatalf("updated = %v, %v", updated, err)
	}

	delOp, err := c.DeleteCluster(ctx, &managedkafkapb.DeleteClusterRequest{Name: created.GetName()})
	if err != nil {
		t.Fatalf("DeleteCluster: %v", err)
	}

	if err := delOp.Wait(ctx); err != nil {
		t.Fatalf("DeleteCluster Wait: %v", err)
	}
}
