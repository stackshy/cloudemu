package bigtableadmin_test

import (
	"context"
	"net"
	"testing"
	"time"

	adminpb "cloud.google.com/go/bigtable/admin/apiv2/adminpb"
	iampb "cloud.google.com/go/iam/apiv1/iampb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/stackshy/cloudemu/v2/config"
	btprovider "github.com/stackshy/cloudemu/v2/providers/gcp/bigtable"
	cgrpc "github.com/stackshy/cloudemu/v2/server/grpc"
	bigtableadmin "github.com/stackshy/cloudemu/v2/server/grpc/bigtableadmin"
	btdriver "github.com/stackshy/cloudemu/v2/services/bigtable/driver"
)

const (
	project    = "projects/p1"
	instanceID = "inst1"
)

// harness wires the gRPC BigtableAdmin servers onto the transport foundation,
// backed by a real provider Mock, and dials it over an in-memory bufconn — a
// full gRPC round trip exercising the proto<->driver conversions and the store.
type harness struct {
	instances adminpb.BigtableInstanceAdminClient
	tables    adminpb.BigtableTableAdminClient
	store     *btprovider.Mock
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	store := btprovider.New(config.NewOptions(config.WithClock(fc), config.WithRegion("us-central1"), config.WithAccountID("p1")))

	gs := cgrpc.New()
	bigtableadmin.Register(gs, func() btdriver.Admin { return store })

	lis := bufconn.Listen(1 << 20)

	go func() { _ = gs.Serve(lis) }()

	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	t.Cleanup(func() {
		_ = conn.Close()
		_ = gs.Shutdown(context.Background())
	})

	return &harness{
		instances: adminpb.NewBigtableInstanceAdminClient(conn),
		tables:    adminpb.NewBigtableTableAdminClient(conn),
		store:     store,
	}
}

func (h *harness) createInstance(t *testing.T, ctx context.Context) *adminpb.Instance {
	t.Helper()

	op, err := h.instances.CreateInstance(ctx, &adminpb.CreateInstanceRequest{
		Parent:     project,
		InstanceId: instanceID,
		Instance:   &adminpb.Instance{DisplayName: "Inst One", Type: adminpb.Instance_PRODUCTION},
		Clusters: map[string]*adminpb.Cluster{
			"c1": {Location: project + "/locations/us-central1-a", ServeNodes: 3, DefaultStorageType: adminpb.StorageType_SSD},
		},
	})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	if !op.GetDone() {
		t.Fatalf("CreateInstance operation not done synchronously")
	}

	var inst adminpb.Instance
	if err := op.GetResponse().UnmarshalTo(&inst); err != nil {
		t.Fatalf("unmarshal instance from LRO response: %v", err)
	}

	return &inst
}

func TestInstanceLifecycle(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	inst := h.createInstance(t, ctx)
	if inst.GetName() != project+"/instances/"+instanceID {
		t.Fatalf("instance name = %q", inst.GetName())
	}

	if inst.GetType() != adminpb.Instance_PRODUCTION {
		t.Fatalf("instance type = %v, want PRODUCTION", inst.GetType())
	}

	got, err := h.instances.GetInstance(ctx, &adminpb.GetInstanceRequest{Name: inst.GetName()})
	if err != nil {
		t.Fatalf("GetInstance: %v", err)
	}

	if got.GetDisplayName() != "Inst One" {
		t.Fatalf("display name = %q", got.GetDisplayName())
	}

	list, err := h.instances.ListInstances(ctx, &adminpb.ListInstancesRequest{Parent: project})
	if err != nil {
		t.Fatalf("ListInstances: %v", err)
	}

	if len(list.GetInstances()) != 1 {
		t.Fatalf("ListInstances returned %d, want 1", len(list.GetInstances()))
	}

	// PartialUpdateInstance (LRO) with a field mask.
	upOp, err := h.instances.PartialUpdateInstance(ctx, &adminpb.PartialUpdateInstanceRequest{
		Instance:   &adminpb.Instance{Name: inst.GetName(), DisplayName: "Renamed"},
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"display_name"}},
	})
	if err != nil {
		t.Fatalf("PartialUpdateInstance: %v", err)
	}

	if !upOp.GetDone() {
		t.Fatalf("PartialUpdateInstance not done")
	}

	after, err := h.instances.GetInstance(ctx, &adminpb.GetInstanceRequest{Name: inst.GetName()})
	if err != nil {
		t.Fatalf("GetInstance after update: %v", err)
	}

	if after.GetDisplayName() != "Renamed" {
		t.Fatalf("display name after update = %q, want Renamed", after.GetDisplayName())
	}

	if _, err := h.instances.DeleteInstance(ctx, &adminpb.DeleteInstanceRequest{Name: inst.GetName()}); err != nil {
		t.Fatalf("DeleteInstance: %v", err)
	}

	if _, err := h.instances.GetInstance(ctx, &adminpb.GetInstanceRequest{Name: inst.GetName()}); status.Code(err) != codes.NotFound {
		t.Fatalf("GetInstance after delete: code = %v, want NotFound", status.Code(err))
	}
}

func TestClusterAndTableLifecycle(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	inst := h.createInstance(t, ctx)

	// CreateCluster (LRO).
	cOp, err := h.instances.CreateCluster(ctx, &adminpb.CreateClusterRequest{
		Parent:    inst.GetName(),
		ClusterId: "c2",
		Cluster:   &adminpb.Cluster{Location: project + "/locations/us-central1-b", ServeNodes: 3, DefaultStorageType: adminpb.StorageType_SSD},
	})
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	if !cOp.GetDone() {
		t.Fatalf("CreateCluster not done")
	}

	clusters, err := h.instances.ListClusters(ctx, &adminpb.ListClustersRequest{Parent: inst.GetName()})
	if err != nil {
		t.Fatalf("ListClusters: %v", err)
	}

	if len(clusters.GetClusters()) != 2 {
		t.Fatalf("ListClusters = %d, want 2", len(clusters.GetClusters()))
	}

	// PartialUpdateCluster (LRO) with a mask: change c1's node count to 5. This
	// is the RPC the terraform provider uses to resize a cluster.
	puOp, err := h.instances.PartialUpdateCluster(ctx, &adminpb.PartialUpdateClusterRequest{
		Cluster:    &adminpb.Cluster{Name: inst.GetName() + "/clusters/c1", ServeNodes: 5},
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"serve_nodes"}},
	})
	if err != nil {
		t.Fatalf("PartialUpdateCluster: %v", err)
	}

	if !puOp.GetDone() {
		t.Fatalf("PartialUpdateCluster not done synchronously")
	}

	c1, err := h.instances.GetCluster(ctx, &adminpb.GetClusterRequest{Name: inst.GetName() + "/clusters/c1"})
	if err != nil {
		t.Fatalf("GetCluster: %v", err)
	}

	if c1.GetServeNodes() != 5 {
		t.Fatalf("serve nodes after PartialUpdateCluster = %d, want 5", c1.GetServeNodes())
	}

	// CreateTable is synchronous (returns the Table directly).
	tbl, err := h.tables.CreateTable(ctx, &adminpb.CreateTableRequest{
		Parent:  inst.GetName(),
		TableId: "t1",
		Table:   &adminpb.Table{ColumnFamilies: map[string]*adminpb.ColumnFamily{"cf1": {}}},
	})
	if err != nil {
		t.Fatalf("CreateTable: %v", err)
	}

	if tbl.GetName() != inst.GetName()+"/tables/t1" {
		t.Fatalf("table name = %q", tbl.GetName())
	}

	if _, ok := tbl.GetColumnFamilies()["cf1"]; !ok {
		t.Fatalf("table missing cf1: %v", tbl.GetColumnFamilies())
	}

	// ModifyColumnFamilies: add cf2.
	modded, err := h.tables.ModifyColumnFamilies(ctx, &adminpb.ModifyColumnFamiliesRequest{
		Name: tbl.GetName(),
		Modifications: []*adminpb.ModifyColumnFamiliesRequest_Modification{{
			Id:  "cf2",
			Mod: &adminpb.ModifyColumnFamiliesRequest_Modification_Create{Create: &adminpb.ColumnFamily{}},
		}},
	})
	if err != nil {
		t.Fatalf("ModifyColumnFamilies: %v", err)
	}

	if _, ok := modded.GetColumnFamilies()["cf2"]; !ok {
		t.Fatalf("cf2 not created: %v", modded.GetColumnFamilies())
	}

	got, err := h.tables.GetTable(ctx, &adminpb.GetTableRequest{Name: tbl.GetName()})
	if err != nil {
		t.Fatalf("GetTable: %v", err)
	}

	if len(got.GetColumnFamilies()) != 2 {
		t.Fatalf("GetTable families = %d, want 2", len(got.GetColumnFamilies()))
	}

	tables, err := h.tables.ListTables(ctx, &adminpb.ListTablesRequest{Parent: inst.GetName()})
	if err != nil {
		t.Fatalf("ListTables: %v", err)
	}

	if len(tables.GetTables()) != 1 {
		t.Fatalf("ListTables = %d, want 1", len(tables.GetTables()))
	}

	if _, err := h.tables.DeleteTable(ctx, &adminpb.DeleteTableRequest{Name: tbl.GetName()}); err != nil {
		t.Fatalf("DeleteTable: %v", err)
	}

	if _, err := h.tables.GetTable(ctx, &adminpb.GetTableRequest{Name: tbl.GetName()}); status.Code(err) != codes.NotFound {
		t.Fatalf("GetTable after delete: code = %v, want NotFound", status.Code(err))
	}
}

func TestAppProfileLifecycle(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	inst := h.createInstance(t, ctx)

	// Create with a single-cluster routing policy + allow_transactional_writes.
	created, err := h.instances.CreateAppProfile(ctx, &adminpb.CreateAppProfileRequest{
		Parent:       inst.GetName(),
		AppProfileId: "ap1",
		AppProfile: &adminpb.AppProfile{
			Description: "primary",
			RoutingPolicy: &adminpb.AppProfile_SingleClusterRouting_{
				SingleClusterRouting: &adminpb.AppProfile_SingleClusterRouting{
					ClusterId: "c1", AllowTransactionalWrites: true,
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateAppProfile: %v", err)
	}

	if created.GetName() != inst.GetName()+"/appProfiles/ap1" {
		t.Fatalf("app profile name = %q", created.GetName())
	}

	if scr := created.GetSingleClusterRouting(); scr == nil || scr.GetClusterId() != "c1" || !scr.GetAllowTransactionalWrites() {
		t.Fatalf("single-cluster routing round-trip failed: %v", created.GetRoutingPolicy())
	}

	// Get round-trips description + routing.
	got, err := h.instances.GetAppProfile(ctx, &adminpb.GetAppProfileRequest{Name: created.GetName()})
	if err != nil {
		t.Fatalf("GetAppProfile: %v", err)
	}

	if got.GetDescription() != "primary" {
		t.Fatalf("description = %q, want primary", got.GetDescription())
	}

	list, err := h.instances.ListAppProfiles(ctx, &adminpb.ListAppProfilesRequest{Parent: inst.GetName()})
	if err != nil {
		t.Fatalf("ListAppProfiles: %v", err)
	}

	if len(list.GetAppProfiles()) != 1 {
		t.Fatalf("ListAppProfiles = %d, want 1", len(list.GetAppProfiles()))
	}

	// Update (LRO) with a mask: switch to multi-cluster routing, keep description.
	upOp, err := h.instances.UpdateAppProfile(ctx, &adminpb.UpdateAppProfileRequest{
		AppProfile: &adminpb.AppProfile{
			Name: created.GetName(),
			RoutingPolicy: &adminpb.AppProfile_MultiClusterRoutingUseAny_{
				MultiClusterRoutingUseAny: &adminpb.AppProfile_MultiClusterRoutingUseAny{},
			},
		},
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"multi_cluster_routing_use_any"}},
	})
	if err != nil {
		t.Fatalf("UpdateAppProfile: %v", err)
	}

	if !upOp.GetDone() {
		t.Fatalf("UpdateAppProfile operation not done synchronously")
	}

	var updated adminpb.AppProfile
	if err := upOp.GetResponse().UnmarshalTo(&updated); err != nil {
		t.Fatalf("unmarshal app profile from LRO response: %v", err)
	}

	if updated.GetMultiClusterRoutingUseAny() == nil {
		t.Fatalf("update did not switch to multi-cluster routing: %v", updated.GetRoutingPolicy())
	}

	// The unmasked description must be preserved, not wiped.
	if updated.GetDescription() != "primary" {
		t.Fatalf("masked update wiped description: %q", updated.GetDescription())
	}

	if _, err := h.instances.DeleteAppProfile(ctx, &adminpb.DeleteAppProfileRequest{Name: created.GetName()}); err != nil {
		t.Fatalf("DeleteAppProfile: %v", err)
	}

	if _, err := h.instances.GetAppProfile(
		ctx, &adminpb.GetAppProfileRequest{Name: created.GetName()},
	); status.Code(err) != codes.NotFound {
		t.Fatalf("GetAppProfile after delete: code = %v, want NotFound", status.Code(err))
	}
}

func TestIamPolicyRoundTrip(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	inst := h.createInstance(t, ctx)

	set, err := h.instances.SetIamPolicy(ctx, &iampb.SetIamPolicyRequest{
		Resource: inst.GetName(),
		Policy: &iampb.Policy{
			Bindings: []*iampb.Binding{{Role: "roles/bigtable.admin", Members: []string{"user:a@example.com"}}},
		},
	})
	if err != nil {
		t.Fatalf("SetIamPolicy: %v", err)
	}

	if len(set.GetBindings()) != 1 || set.GetBindings()[0].GetRole() != "roles/bigtable.admin" {
		t.Fatalf("SetIamPolicy bindings = %v", set.GetBindings())
	}

	got, err := h.instances.GetIamPolicy(ctx, &iampb.GetIamPolicyRequest{Resource: inst.GetName()})
	if err != nil {
		t.Fatalf("GetIamPolicy: %v", err)
	}

	if len(got.GetBindings()) != 1 {
		t.Fatalf("GetIamPolicy bindings = %d, want 1", len(got.GetBindings()))
	}

	perms, err := h.instances.TestIamPermissions(ctx, &iampb.TestIamPermissionsRequest{
		Resource: inst.GetName(), Permissions: []string{"bigtable.tables.get"},
	})
	if err != nil {
		t.Fatalf("TestIamPermissions: %v", err)
	}

	if len(perms.GetPermissions()) != 1 {
		t.Fatalf("TestIamPermissions = %v, want [bigtable.tables.get]", perms.GetPermissions())
	}
}

func TestAlreadyExistsMapsToStatus(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	h.createInstance(t, ctx)

	_, err := h.instances.CreateInstance(ctx, &adminpb.CreateInstanceRequest{
		Parent:     project,
		InstanceId: instanceID,
		Instance:   &adminpb.Instance{DisplayName: "dup"},
		Clusters: map[string]*adminpb.Cluster{
			"c1": {Location: project + "/locations/us-central1-a", ServeNodes: 3},
		},
	})
	if status.Code(err) != codes.AlreadyExists {
		t.Fatalf("duplicate CreateInstance: code = %v, want AlreadyExists", status.Code(err))
	}
}
