package filestore_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	file "google.golang.org/api/file/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
	redis "google.golang.org/api/redis/v1"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

const (
	testProject  = "demo"
	testLocation = "us-central1"
)

// newServer assembles a GCP server with BOTH Filestore (always registered) and
// Memorystore wired, so every test exercises the dispatch disambiguation
// between the two on their shared /instances path grammar.
func newServer(t *testing.T) *httptest.Server {
	t.Helper()

	cloud := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.Drivers{
		Memorystore: cloud.Memorystore,
		Firestore:   cloud.Firestore,
		Clock:       config.NewFakeClock(time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)),
	})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	return ts
}

func newFileService(t *testing.T, ts *httptest.Server) *file.Service {
	t.Helper()

	svc, err := file.NewService(context.Background(),
		option.WithEndpoint(ts.URL), option.WithoutAuthentication())
	if err != nil {
		t.Fatalf("file.NewService: %v", err)
	}

	return svc
}

func parent() string { return "projects/" + testProject + "/locations/" + testLocation }

func fsName(id string) string { return parent() + "/instances/" + id }

// TestSDKFilestoreLifecycle drives create -> get -> update -> delete via the
// real google.golang.org/api/file/v1 client, asserting the round-trip of tier,
// fileShares, networks, computed ipAddresses, state, createTime, and the
// connectMode / nfsExportOptions defaults.
func TestSDKFilestoreLifecycle(t *testing.T) {
	ts := newServer(t)
	svc := newFileService(t, ts)
	ctx := context.Background()

	create := &file.Instance{
		Tier:   "BASIC_HDD",
		Labels: map[string]string{"env": "test"},
		FileShares: []*file.FileShareConfig{{
			Name:       "share1",
			CapacityGb: 1024,
			NfsExportOptions: []*file.NfsExportOptions{{
				IpRanges: []string{"10.0.0.0/24"},
			}},
		}},
		Networks: []*file.NetworkConfig{{
			Network:         "default",
			Modes:           []string{"MODE_IPV4"},
			ReservedIpRange: "10.9.0.0/29",
		}},
	}

	op, err := svc.Projects.Locations.Instances.Create(parent(), create).
		InstanceId("nfs1").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if !op.Done {
		t.Fatalf("create operation not done: %+v", op)
	}

	got, err := svc.Projects.Locations.Instances.Get(fsName("nfs1")).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	assertInstance(t, got)

	// Update capacity + labels via PATCH with an updateMask.
	got.FileShares[0].CapacityGb = 2048
	patchBody := &file.Instance{
		FileShares: got.FileShares,
		Labels:     map[string]string{"env": "prod"},
	}

	if _, err := svc.Projects.Locations.Instances.Patch(fsName("nfs1"), patchBody).
		UpdateMask("fileShares,labels").Context(ctx).Do(); err != nil {
		t.Fatalf("Patch: %v", err)
	}

	after, err := svc.Projects.Locations.Instances.Get(fsName("nfs1")).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get after patch: %v", err)
	}

	if after.FileShares[0].CapacityGb != 2048 {
		t.Errorf("after patch capacityGb = %d, want 2048", after.FileShares[0].CapacityGb)
	}

	if after.Labels["env"] != "prod" {
		t.Errorf("after patch labels = %v, want env=prod", after.Labels)
	}

	// List returns the instance.
	list, err := svc.Projects.Locations.Instances.List(parent()).Context(ctx).Do()
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(list.Instances) != 1 || list.Instances[0].Name != fsName("nfs1") {
		t.Fatalf("List = %+v, want one instance %q", list.Instances, fsName("nfs1"))
	}

	// Delete, then a Get 404s.
	delOp, err := svc.Projects.Locations.Instances.Delete(fsName("nfs1")).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if !delOp.Done {
		t.Fatalf("delete operation not done: %+v", delOp)
	}

	_, err = svc.Projects.Locations.Instances.Get(fsName("nfs1")).Context(ctx).Do()

	var gerr *googleapi.Error
	if !errors.As(err, &gerr) || gerr.Code != 404 {
		t.Fatalf("Get after delete: got %v, want 404", err)
	}
}

// TestSDKFilestoreOperationPollReturnsInstance guards that a client which POLLS
// the create operation (rather than reading the inline response) receives the
// Instance as a JSON object, not a base64-encoded string. The shared LRO handler
// marshals a registered []byte as base64, so the response must be registered as
// a json.RawMessage.
func TestSDKFilestoreOperationPollReturnsInstance(t *testing.T) {
	ts := newServer(t)
	svc := newFileService(t, ts)
	ctx := context.Background()

	create := &file.Instance{
		Tier:       "BASIC_HDD",
		FileShares: []*file.FileShareConfig{{Name: "share1", CapacityGb: 1024}},
		Networks:   []*file.NetworkConfig{{Network: "default", Modes: []string{"MODE_IPV4"}}},
	}

	op, err := svc.Projects.Locations.Instances.Create(parent(), create).
		InstanceId("poll1").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	polled, err := svc.Projects.Locations.Operations.Get(op.Name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Operations.Get: %v", err)
	}

	if !polled.Done {
		t.Fatalf("polled operation not done: %+v", polled)
	}

	// The polled response must decode into an Instance — a base64 string would
	// fail here (json: cannot unmarshal string into ...Instance).
	var inst file.Instance
	if err := json.Unmarshal(polled.Response, &inst); err != nil {
		t.Fatalf("polled response is not an Instance object (base64-garbled?): %v; raw=%s", err, polled.Response)
	}

	if inst.Name != fsName("poll1") || inst.Tier != "BASIC_HDD" {
		t.Fatalf("polled instance = %q/%q, want %q/BASIC_HDD", inst.Name, inst.Tier, fsName("poll1"))
	}
}

// TestFilestoreConcurrentGetPatch guards that reads return deep copies: under
// -race, concurrent Get and Patch on the same instance must not race on the
// stored model's maps/slices. Pre-fix (reads returned shared pointers) this
// reproduced a DATA RACE.
func TestFilestoreConcurrentGetPatch(t *testing.T) {
	ts := newServer(t)
	svc := newFileService(t, ts)
	ctx := context.Background()

	create := &file.Instance{
		Tier:       "BASIC_HDD",
		Labels:     map[string]string{"env": "test"},
		FileShares: []*file.FileShareConfig{{Name: "share1", CapacityGb: 1024}},
		Networks:   []*file.NetworkConfig{{Network: "default", Modes: []string{"MODE_IPV4"}}},
	}

	if _, err := svc.Projects.Locations.Instances.Create(parent(), create).
		InstanceId("race1").Context(ctx).Do(); err != nil {
		t.Fatalf("Create: %v", err)
	}

	name := fsName("race1")

	var wg sync.WaitGroup

	for range 4 {
		wg.Add(2)

		go func() {
			defer wg.Done()

			for range 20 {
				if _, err := svc.Projects.Locations.Instances.Get(name).Context(ctx).Do(); err != nil {
					t.Errorf("Get: %v", err)

					return
				}
			}
		}()

		go func() {
			defer wg.Done()

			for range 20 {
				patch := &file.Instance{Labels: map[string]string{"env": "prod"}}
				if _, err := svc.Projects.Locations.Instances.Patch(name, patch).
					UpdateMask("labels").Context(ctx).Do(); err != nil {
					t.Errorf("Patch: %v", err)

					return
				}
			}
		}()
	}

	wg.Wait()
}

func assertInstance(t *testing.T, got *file.Instance) {
	t.Helper()

	if got.Name != fsName("nfs1") {
		t.Errorf("name = %q, want %q", got.Name, fsName("nfs1"))
	}

	if got.Tier != "BASIC_HDD" {
		t.Errorf("tier = %q, want BASIC_HDD", got.Tier)
	}

	if got.State != "READY" {
		t.Errorf("state = %q, want READY", got.State)
	}

	if got.CreateTime == "" {
		t.Errorf("createTime is empty, want a timestamp")
	}

	if len(got.FileShares) != 1 || got.FileShares[0].Name != "share1" ||
		got.FileShares[0].CapacityGb != 1024 {
		t.Fatalf("fileShares = %+v, want share1/1024", got.FileShares)
	}

	// nfsExportOptions defaults filled in.
	opts := got.FileShares[0].NfsExportOptions
	if len(opts) != 1 || opts[0].AccessMode != "READ_WRITE" || opts[0].SquashMode != "NO_ROOT_SQUASH" {
		t.Errorf("nfsExportOptions = %+v, want READ_WRITE/NO_ROOT_SQUASH defaults", opts)
	}

	if len(got.Networks) != 1 {
		t.Fatalf("networks = %+v, want one", got.Networks)
	}

	n := got.Networks[0]
	if len(n.Modes) != 1 || n.Modes[0] != "MODE_IPV4" {
		t.Errorf("network modes = %v, want [MODE_IPV4]", n.Modes)
	}

	if n.ConnectMode != "DIRECT_PEERING" {
		t.Errorf("connectMode = %q, want DIRECT_PEERING default", n.ConnectMode)
	}

	// ipAddresses is output-only and assigned on create, inside the reserved
	// /29 CIDR.
	if len(n.IpAddresses) != 1 || n.IpAddresses[0] != "10.9.0.2" {
		t.Errorf("ipAddresses = %v, want [10.9.0.2] from reservedIpRange", n.IpAddresses)
	}
}

// TestMemorystoreStillRoutes is the make-or-break dispatch check: on the SAME
// server that serves Filestore, a Memorystore (redis.googleapis.com) request on
// the identical /instances path must still reach Memorystore, and the two
// stores stay independent.
func TestMemorystoreStillRoutes(t *testing.T) {
	ts := newServer(t)
	ctx := context.Background()

	fileSvc := newFileService(t, ts)

	redisSvc, err := redis.NewService(ctx, option.WithEndpoint(ts.URL), option.WithoutAuthentication())
	if err != nil {
		t.Fatalf("redis.NewService: %v", err)
	}

	// A Filestore instance (fileShares body -> Filestore) and a Redis instance
	// (memorySizeGb body -> Memorystore) coexist in the same (project, location)
	// on the same server.
	if _, err := fileSvc.Projects.Locations.Instances.Create(parent(), &file.Instance{
		Tier:       "BASIC_HDD",
		FileShares: []*file.FileShareConfig{{Name: "s", CapacityGb: 1024}},
		Networks:   []*file.NetworkConfig{{Network: "default", Modes: []string{"MODE_IPV4"}}},
	}).InstanceId("nfs").Context(ctx).Do(); err != nil {
		t.Fatalf("Filestore Create: %v", err)
	}

	if _, err := redisSvc.Projects.Locations.Instances.Create(parent(), &redis.Instance{
		Tier:         "BASIC",
		MemorySizeGb: 1,
	}).InstanceId("cache").Context(ctx).Do(); err != nil {
		t.Fatalf("Redis Create (must route to Memorystore, not Filestore): %v", err)
	}

	// Redis Get routes to Memorystore: Filestore does not own "cache", so the
	// request falls through to the Memorystore handler.
	rGot, err := redisSvc.Projects.Locations.Instances.Get(fsName("cache")).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Redis Get: %v", err)
	}

	if rGot.Host == "" || rGot.MemorySizeGb != 1 {
		t.Errorf("redis instance = %+v, want host set + memorySizeGb=1", rGot)
	}

	// Filestore Get routes to Filestore: it owns "nfs".
	fGot, err := fileSvc.Projects.Locations.Instances.Get(fsName("nfs")).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Filestore Get: %v", err)
	}

	if len(fGot.FileShares) != 1 || fGot.FileShares[0].CapacityGb != 1024 {
		t.Errorf("filestore instance = %+v, want one fileShare of 1024", fGot.FileShares)
	}
}

// TestSDKFilestoreNotFound confirms an unknown instance 404s (and is not
// swallowed by Memorystore).
func TestSDKFilestoreNotFound(t *testing.T) {
	ts := newServer(t)
	svc := newFileService(t, ts)

	_, err := svc.Projects.Locations.Instances.Get(fsName("missing")).
		Context(context.Background()).Do()

	var gerr *googleapi.Error
	if !errors.As(err, &gerr) || gerr.Code != 404 {
		t.Fatalf("Get(missing): got %v, want 404", err)
	}
}
