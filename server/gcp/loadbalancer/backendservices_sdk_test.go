package loadbalancer_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	gcpcompute "cloud.google.com/go/compute/apiv1"
	computepb "cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

func ptrI32(v int32) *int32 { return &v }

func ptrF32(v float32) *float32 { return &v }

func ptrBool(v bool) *bool { return &v }

func newBackendServicesClient(t *testing.T, url string, httpc option.ClientOption) *gcpcompute.BackendServicesClient {
	t.Helper()

	client, err := gcpcompute.NewBackendServicesRESTClient(context.Background(),
		option.WithEndpoint(url), option.WithoutAuthentication(), httpc)
	if err != nil {
		t.Fatalf("NewBackendServicesRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	return client
}

// TestSDKGCPBackendServicePatch reproduces the [BLOCKER] finding that
// compute.backendServices.patch returned 405 (no update path), leaving the
// resource read-only after create.
func TestSDKGCPBackendServicePatch(t *testing.T) {
	ts := newGCPLBServer(t)
	ctx := context.Background()

	client, err := gcpcompute.NewBackendServicesRESTClient(ctx,
		option.WithEndpoint(ts.URL), option.WithoutAuthentication(), option.WithHTTPClient(ts.Client()))
	if err != nil {
		t.Fatalf("client: %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	insertOp, err := client.Insert(ctx, &computepb.InsertBackendServiceRequest{
		Project: testProject,
		BackendServiceResource: &computepb.BackendService{
			Name:                ptrStr("web-bs"),
			Protocol:            ptrStr("HTTP"),
			TimeoutSec:          ptrI32(30),
			LoadBalancingScheme: ptrStr("EXTERNAL_MANAGED"),
			SessionAffinity:     ptrStr("NONE"),
		},
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	if err := insertOp.Wait(ctx); err != nil {
		t.Fatalf("Insert wait: %v", err)
	}

	// Patch must succeed (previously 405) and mutate the resource.
	patchOp, err := client.Patch(ctx, &computepb.PatchBackendServiceRequest{
		Project:        testProject,
		BackendService: "web-bs",
		BackendServiceResource: &computepb.BackendService{
			TimeoutSec:      ptrI32(45),
			SessionAffinity: ptrStr("CLIENT_IP"),
		},
	})
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}

	if err := patchOp.Wait(ctx); err != nil {
		t.Fatalf("Patch wait: %v", err)
	}

	got, err := client.Get(ctx, &computepb.GetBackendServiceRequest{Project: testProject, BackendService: "web-bs"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.GetTimeoutSec() != 45 {
		t.Errorf("timeoutSec = %d, want 45 (patch not applied)", got.GetTimeoutSec())
	}

	if got.GetSessionAffinity() != "CLIENT_IP" {
		t.Errorf("sessionAffinity = %q, want CLIENT_IP", got.GetSessionAffinity())
	}

	// Protocol was not in the patch body, so it must be preserved.
	if got.GetProtocol() != "HTTP" {
		t.Errorf("protocol = %q, want HTTP (patch clobbered an omitted field)", got.GetProtocol())
	}
}

// TestSDKGCPBackendServiceFields reproduces the [HIGH] finding that
// loadBalancingScheme/timeoutSec/sessionAffinity/fingerprint/creationTimestamp
// were all dropped on get.
func TestSDKGCPBackendServiceFields(t *testing.T) {
	ts := newGCPLBServer(t)
	ctx := context.Background()

	client, err := gcpcompute.NewBackendServicesRESTClient(ctx,
		option.WithEndpoint(ts.URL), option.WithoutAuthentication(), option.WithHTTPClient(ts.Client()))
	if err != nil {
		t.Fatalf("client: %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	op, err := client.Insert(ctx, &computepb.InsertBackendServiceRequest{
		Project: testProject,
		BackendServiceResource: &computepb.BackendService{
			Name:                ptrStr("full-bs"),
			Protocol:            ptrStr("HTTP"),
			LoadBalancingScheme: ptrStr("INTERNAL_MANAGED"),
			SessionAffinity:     ptrStr("CLIENT_IP"),
			TimeoutSec:          ptrI32(25),
		},
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	if err := op.Wait(ctx); err != nil {
		t.Fatalf("Insert wait: %v", err)
	}

	got, err := client.Get(ctx, &computepb.GetBackendServiceRequest{Project: testProject, BackendService: "full-bs"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.GetLoadBalancingScheme() != "INTERNAL_MANAGED" {
		t.Errorf("loadBalancingScheme = %q, want INTERNAL_MANAGED", got.GetLoadBalancingScheme())
	}

	if got.GetSessionAffinity() != "CLIENT_IP" {
		t.Errorf("sessionAffinity = %q, want CLIENT_IP", got.GetSessionAffinity())
	}

	if got.GetTimeoutSec() != 25 {
		t.Errorf("timeoutSec = %d, want 25", got.GetTimeoutSec())
	}

	if got.GetFingerprint() == "" {
		t.Error("fingerprint empty; blocks all future patches")
	}

	if got.GetCreationTimestamp() == "" {
		t.Error("creationTimestamp empty")
	}
}

// TestSDKGCPBackendServiceDuplicate reproduces the #643-deferred portion: a
// duplicate-name insert must fail with 409 alreadyExists, not silently succeed.
func TestSDKGCPBackendServiceDuplicate(t *testing.T) {
	ts := newGCPLBServer(t)
	ctx := context.Background()

	client, err := gcpcompute.NewBackendServicesRESTClient(ctx,
		option.WithEndpoint(ts.URL), option.WithoutAuthentication(), option.WithHTTPClient(ts.Client()))
	if err != nil {
		t.Fatalf("client: %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	insert := func() error {
		op, err := client.Insert(ctx, &computepb.InsertBackendServiceRequest{
			Project:                testProject,
			BackendServiceResource: &computepb.BackendService{Name: ptrStr("dup-bs"), Protocol: ptrStr("TCP")},
		})
		if err != nil {
			return err
		}

		return op.Wait(ctx)
	}

	if err := insert(); err != nil {
		t.Fatalf("first insert: %v", err)
	}

	if err := insert(); err == nil {
		t.Fatal("second insert of duplicate name: want error, got nil")
	}
}

// TestSDKGCPBackendServiceBackendsRoundTrip covers the round-trip drops for a
// google_compute_backend_service: backends[] (group/balancingMode/
// capacityScaler), connectionDraining, cdnPolicy and enableCDN were all dropped
// on get, guaranteeing perpetual Terraform drift.
func TestSDKGCPBackendServiceBackendsRoundTrip(t *testing.T) {
	ts := newGCPLBServer(t)
	ctx := context.Background()

	client := newBackendServicesClient(t, ts.URL, option.WithHTTPClient(ts.Client()))

	group := "projects/" + testProject + "/zones/us-central1-a/instanceGroups/ig-1"

	op, err := client.Insert(ctx, &computepb.InsertBackendServiceRequest{
		Project: testProject,
		BackendServiceResource: &computepb.BackendService{
			Name:                ptrStr("cdn-bs"),
			Protocol:            ptrStr("HTTP"),
			LoadBalancingScheme: ptrStr("EXTERNAL_MANAGED"),
			Backends: []*computepb.Backend{{
				Group:          ptrStr(group),
				BalancingMode:  ptrStr("UTILIZATION"),
				CapacityScaler: ptrF32(0.5),
			}},
			ConnectionDraining: &computepb.ConnectionDraining{DrainingTimeoutSec: ptrI32(45)},
			EnableCDN:          ptrBool(true),
			CdnPolicy: &computepb.BackendServiceCdnPolicy{
				CacheMode:  ptrStr("CACHE_ALL_STATIC"),
				DefaultTtl: ptrI32(3600),
			},
		},
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	if err := op.Wait(ctx); err != nil {
		t.Fatalf("Insert wait: %v", err)
	}

	got, err := client.Get(ctx, &computepb.GetBackendServiceRequest{Project: testProject, BackendService: "cdn-bs"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	backends := got.GetBackends()
	if len(backends) != 1 {
		t.Fatalf("backends len = %d, want 1 (backends[] dropped)", len(backends))
	}

	if backends[0].GetGroup() != group {
		t.Errorf("backend group = %q, want %q", backends[0].GetGroup(), group)
	}

	if backends[0].GetBalancingMode() != "UTILIZATION" {
		t.Errorf("balancingMode = %q, want UTILIZATION", backends[0].GetBalancingMode())
	}

	if backends[0].GetCapacityScaler() != 0.5 {
		t.Errorf("capacityScaler = %v, want 0.5", backends[0].GetCapacityScaler())
	}

	if got.GetConnectionDraining().GetDrainingTimeoutSec() != 45 {
		t.Errorf("drainingTimeoutSec = %d, want 45", got.GetConnectionDraining().GetDrainingTimeoutSec())
	}

	if !got.GetEnableCDN() {
		t.Error("enableCDN = false, want true")
	}

	if got.GetCdnPolicy().GetCacheMode() != "CACHE_ALL_STATIC" {
		t.Errorf("cdnPolicy.cacheMode = %q, want CACHE_ALL_STATIC", got.GetCdnPolicy().GetCacheMode())
	}

	if got.GetCdnPolicy().GetDefaultTtl() != 3600 {
		t.Errorf("cdnPolicy.defaultTtl = %d, want 3600", got.GetCdnPolicy().GetDefaultTtl())
	}
}

// insertBS is a small create helper for the list tests below.
func insertBS(ctx context.Context, t *testing.T, client *gcpcompute.BackendServicesClient, name string) {
	t.Helper()

	op, err := client.Insert(ctx, &computepb.InsertBackendServiceRequest{
		Project:                testProject,
		BackendServiceResource: &computepb.BackendService{Name: ptrStr(name), Protocol: ptrStr("TCP")},
	})
	if err != nil {
		t.Fatalf("Insert %s: %v", name, err)
	}

	if err := op.Wait(ctx); err != nil {
		t.Fatalf("Insert %s wait: %v", name, err)
	}
}

func listBSNames(t *testing.T, it *gcpcompute.BackendServiceIterator) []string {
	t.Helper()

	var names []string

	for {
		bs, err := it.Next()
		if err == iterator.Done {
			break
		}

		if err != nil {
			t.Fatalf("List: %v", err)
		}

		names = append(names, bs.GetName())
	}

	return names
}

// TestSDKGCPBackendServiceListFilter covers the list filter defect: List with a
// "name eq" filter must return only the matching backend service.
func TestSDKGCPBackendServiceListFilter(t *testing.T) {
	ts := newGCPLBServer(t)
	ctx := context.Background()

	client := newBackendServicesClient(t, ts.URL, option.WithHTTPClient(ts.Client()))

	for _, n := range []string{"bs-a", "bs-b", "bs-c"} {
		insertBS(ctx, t, client, n)
	}

	it := client.List(ctx, &computepb.ListBackendServicesRequest{
		Project: testProject,
		Filter:  ptrStr(`name eq "bs-b"`),
	})

	names := listBSNames(t, it)
	if len(names) != 1 || names[0] != "bs-b" {
		t.Fatalf("filtered list = %v, want [bs-b]", names)
	}
}

// TestSDKGCPBackendServiceListPagination covers the pagination defect: a page
// size of 1 must walk every backend service via nextPageToken, returning them
// all exactly once.
func TestSDKGCPBackendServiceListPagination(t *testing.T) {
	ts := newGCPLBServer(t)
	ctx := context.Background()

	client := newBackendServicesClient(t, ts.URL, option.WithHTTPClient(ts.Client()))

	want := []string{"pg-a", "pg-b", "pg-c"}
	for _, n := range want {
		insertBS(ctx, t, client, n)
	}

	it := client.List(ctx, &computepb.ListBackendServicesRequest{
		Project:    testProject,
		MaxResults: func() *uint32 { v := uint32(1); return &v }(),
	})

	names := listBSNames(t, it)
	if len(names) != len(want) {
		t.Fatalf("paginated list = %v, want %v (page token not followed)", names, want)
	}

	seen := map[string]int{}
	for _, n := range names {
		seen[n]++
	}

	for _, n := range want {
		if seen[n] != 1 {
			t.Errorf("%s seen %d times, want exactly 1", n, seen[n])
		}
	}
}

// TestSDKGCPBackendServiceRegionalScope covers the scope-collision defect: a
// global and a regional backend service of the same name must coexist, each
// re-emitted at its own scope, and a regional insert's Operation must carry the
// region self-link.
func TestSDKGCPBackendServiceRegionalScope(t *testing.T) {
	ts := newGCPLBServer(t)
	ctx := context.Background()

	const region = "us-central1"

	global := newBackendServicesClient(t, ts.URL, option.WithHTTPClient(ts.Client()))
	insertBS(ctx, t, global, "shared-bs")

	regional, err := gcpcompute.NewRegionBackendServicesRESTClient(ctx,
		option.WithEndpoint(ts.URL), option.WithoutAuthentication(), option.WithHTTPClient(ts.Client()))
	if err != nil {
		t.Fatalf("NewRegionBackendServicesRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = regional.Close() })

	rOp, err := regional.Insert(ctx, &computepb.InsertRegionBackendServiceRequest{
		Project:                testProject,
		Region:                 region,
		BackendServiceResource: &computepb.BackendService{Name: ptrStr("shared-bs"), Protocol: ptrStr("TCP")},
	})
	if err != nil {
		t.Fatalf("regional Insert: %v", err)
	}

	if err := rOp.Wait(ctx); err != nil {
		t.Fatalf("regional Insert wait: %v", err)
	}

	// Both records must survive: the global create was not clobbered, and the
	// regional read resolves to its own scope.
	gGot, err := global.Get(ctx, &computepb.GetBackendServiceRequest{Project: testProject, BackendService: "shared-bs"})
	if err != nil {
		t.Fatalf("global Get: %v", err)
	}

	if !strings.Contains(gGot.GetSelfLink(), "/global/backendServices/shared-bs") {
		t.Errorf("global selfLink = %q, want a /global/ link", gGot.GetSelfLink())
	}

	rGot, err := regional.Get(ctx, &computepb.GetRegionBackendServiceRequest{
		Project: testProject, Region: region, BackendService: "shared-bs",
	})
	if err != nil {
		t.Fatalf("regional Get: %v", err)
	}

	if !strings.Contains(rGot.GetSelfLink(), "/regions/"+region+"/backendServices/shared-bs") {
		t.Errorf("regional selfLink = %q, want a /regions/%s/ link", rGot.GetSelfLink(), region)
	}

	assertRegionalOpCarriesRegion(t, ts.Client(), ts.URL, region)
}

// assertRegionalOpCarriesRegion issues a raw regional insert and checks the
// returned Operation is DONE and carries the region self-link, which the async
// SDK poller needs to resolve a regional operation.
func assertRegionalOpCarriesRegion(t *testing.T, httpc *http.Client, base, region string) {
	t.Helper()

	url := base + "/compute/v1/projects/" + testProject + "/regions/" + region + "/backendServices"

	resp, err := httpc.Post(url, "application/json", strings.NewReader(`{"name":"raw-rbs","protocol":"TCP"}`))
	if err != nil {
		t.Fatalf("raw regional insert: %v", err)
	}

	defer func() { _ = resp.Body.Close() }()

	var op struct {
		Status string `json:"status"`
		Region string `json:"region"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&op); err != nil {
		t.Fatalf("decode operation: %v", err)
	}

	if op.Status != "DONE" {
		t.Errorf("operation status = %q, want DONE", op.Status)
	}

	if !strings.Contains(op.Region, "/regions/"+region) {
		t.Errorf("operation region = %q, want a /regions/%s link", op.Region, region)
	}
}
