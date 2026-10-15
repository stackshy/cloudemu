package alloydb_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	alloydb "google.golang.org/api/alloydb/v1"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu/v2/config"
	alloyprov "github.com/stackshy/cloudemu/v2/providers/gcp/alloydb"
	alloysrv "github.com/stackshy/cloudemu/v2/server/gcp/alloydb"
)

// newRegionClient serves AlloyDB from a mock whose default region differs from
// the request location, so a name built from the default region shows up.
func newRegionClient(t *testing.T, region string) *alloydb.Service {
	t.Helper()

	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion(region), config.WithProjectID(testProject))

	ts := httptest.NewServer(alloysrv.New(alloyprov.New(opts)))
	t.Cleanup(ts.Close)

	svc, err := alloydb.NewService(context.Background(),
		option.WithEndpoint(ts.URL), option.WithoutAuthentication(), option.WithHTTPClient(ts.Client()))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	return svc
}

func opTarget(t *testing.T, op *alloydb.Operation) string {
	t.Helper()

	var meta struct {
		Target string `json:"target"`
	}

	if err := json.Unmarshal(op.Metadata, &meta); err != nil {
		t.Fatalf("decode metadata: %v", err)
	}

	return meta.Target
}

// TestAlloyDBUsesRequestLocation: resources live in the request location, a
// ZONAL instance keeps its gceZone, allocatedIpRange is echoed and each create
// operation targets the created resource, not its collection.
func TestAlloyDBUsesRequestLocation(t *testing.T) {
	svc := newRegionClient(t, "us-east-1")
	ctx := context.Background()
	cl := svc.Projects.Locations.Clusters
	clusterName := parent() + "/clusters/c1"

	op, err := cl.Create(parent(), &alloydb.Cluster{
		NetworkConfig: &alloydb.NetworkConfig{Network: "default", AllocatedIpRange: "alloy-range"},
	}).ClusterId("c1").Context(ctx).Do()
	if err != nil {
		t.Fatalf("create cluster: %v", err)
	}

	if got := opTarget(t, op); got != clusterName {
		t.Errorf("create-cluster target = %q, want %q", got, clusterName)
	}

	c, err := cl.Get(clusterName).Context(ctx).Do()
	if err != nil {
		t.Fatalf("get cluster: %v", err)
	}

	if c.Name != clusterName || c.NetworkConfig == nil || c.NetworkConfig.AllocatedIpRange != "alloy-range" {
		t.Errorf("cluster name=%q networkConfig=%+v", c.Name, c.NetworkConfig)
	}

	tests := []struct {
		id, instType, avail, zone, wantZone string
	}{
		{"zonal-set", "PRIMARY", "ZONAL", "us-central1-f", "us-central1-f"},
		{"zonal-default", "READ_POOL", "ZONAL", "", "us-central1-a"},
		{"regional", "READ_POOL", "REGIONAL", "", ""},
	}

	for _, tc := range tests {
		var pool *alloydb.ReadPoolConfig
		if tc.instType == "READ_POOL" {
			pool = &alloydb.ReadPoolConfig{NodeCount: 1}
		}

		op, err := cl.Instances.Create(clusterName, &alloydb.Instance{
			InstanceType: tc.instType, AvailabilityType: tc.avail, GceZone: tc.zone, ReadPoolConfig: pool,
		}).InstanceId(tc.id).Context(ctx).Do()
		if err != nil {
			t.Fatalf("%s: create instance: %v", tc.id, err)
		}

		want := clusterName + "/instances/" + tc.id
		if got := opTarget(t, op); got != want {
			t.Errorf("%s: create-instance target = %q, want %q", tc.id, got, want)
		}

		inst, err := cl.Instances.Get(want).Context(ctx).Do()
		if err != nil {
			t.Fatalf("%s: get instance: %v", tc.id, err)
		}

		if inst.Name != want || inst.GceZone != tc.wantZone {
			t.Errorf("%s: name=%q gceZone=%q, want %q %q", tc.id, inst.Name, inst.GceZone, want, tc.wantZone)
		}
	}

	op, err = svc.Projects.Locations.Backups.Create(parent(), &alloydb.Backup{ClusterName: clusterName}).
		BackupId("b1").Context(ctx).Do()
	if err != nil {
		t.Fatalf("create backup: %v", err)
	}

	backupName := parent() + "/backups/b1"
	if got := opTarget(t, op); got != backupName {
		t.Errorf("create-backup target = %q, want %q", got, backupName)
	}

	b, err := svc.Projects.Locations.Backups.Get(backupName).Context(ctx).Do()
	if err != nil {
		t.Fatalf("get backup: %v", err)
	}

	if b.Name != backupName {
		t.Errorf("backup name = %q, want %q", b.Name, backupName)
	}
}
