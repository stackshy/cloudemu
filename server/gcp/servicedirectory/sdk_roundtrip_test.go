package servicedirectory_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
	servicedirectory "google.golang.org/api/servicedirectory/v1"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

const (
	project  = "mock-project"
	location = "us-central1"
)

func newSDKClient(t *testing.T) *servicedirectory.APIService {
	t.Helper()

	cloud := cloudemu.NewGCP()
	srv := gcpserver.NewFromProvider(cloud)

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	svc, err := servicedirectory.NewService(context.Background(),
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("servicedirectory.NewService: %v", err)
	}

	return svc
}

func locParent() string { return "projects/" + project + "/locations/" + location }

// TestSDKHierarchyLifecycle drives the real google.golang.org/api Service
// Directory client through the full three-level hierarchy: create namespace →
// service → endpoint (with labels/annotations/address/port), verify computed
// fields (name/uid) are stable across a re-Get, patch, then delete the namespace
// and confirm the cascade removed the service and endpoint.
func TestSDKHierarchyLifecycle(t *testing.T) {
	svc := newSDKClient(t)
	ctx := context.Background()

	// Create namespace.
	ns, err := svc.Projects.Locations.Namespaces.Create(locParent(),
		&servicedirectory.Namespace{Labels: map[string]string{"env": "prod"}}).
		NamespaceId("ns1").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Namespaces.Create: %v", err)
	}

	wantNsName := locParent() + "/namespaces/ns1"
	if ns.Name != wantNsName {
		t.Fatalf("namespace name = %q, want %q", ns.Name, wantNsName)
	}

	if ns.Uid == "" {
		t.Fatal("namespace uid must be set")
	}

	// Create service under the namespace.
	sd, err := svc.Projects.Locations.Namespaces.Services.Create(wantNsName,
		&servicedirectory.Service{Annotations: map[string]string{"team": "core"}}).
		ServiceId("svc1").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Services.Create: %v", err)
	}

	wantSvcName := wantNsName + "/services/svc1"
	if sd.Name != wantSvcName || sd.Uid == "" {
		t.Fatalf("service name/uid unexpected: %q %q", sd.Name, sd.Uid)
	}

	// Create endpoint under the service.
	ep, err := svc.Projects.Locations.Namespaces.Services.Endpoints.Create(wantSvcName,
		&servicedirectory.Endpoint{Address: "10.0.0.1", Port: 8080, Annotations: map[string]string{"proto": "grpc"}}).
		EndpointId("ep1").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Endpoints.Create: %v", err)
	}

	wantEpName := wantSvcName + "/endpoints/ep1"
	if ep.Name != wantEpName || ep.Uid == "" || ep.Address != "10.0.0.1" || ep.Port != 8080 {
		t.Fatalf("endpoint unexpected: %+v", ep)
	}

	assertComputedStable(t, ctx, svc, ns.Uid, sd.Uid, ep.Uid)
	assertPatch(t, ctx, svc, wantNsName, wantSvcName, wantEpName)
	assertCascadeDelete(t, ctx, svc, wantNsName, wantSvcName, wantEpName)
}

// assertComputedStable re-Gets every resource and asserts the computed name/uid
// fields did not drift — a per-read regeneration would perpetually drift a
// Terraform refresh.
func assertComputedStable(t *testing.T, ctx context.Context, svc *servicedirectory.APIService, nsUID, svcUID, epUID string) {
	t.Helper()

	nsName := locParent() + "/namespaces/ns1"
	svcName := nsName + "/services/svc1"
	epName := svcName + "/endpoints/ep1"

	got, err := svc.Projects.Locations.Namespaces.Get(nsName).Context(ctx).Do()
	if err != nil || got.Uid != nsUID {
		t.Fatalf("namespace uid drift: err=%v uid=%q want=%q", err, safeUID(got), nsUID)
	}

	gs, err := svc.Projects.Locations.Namespaces.Services.Get(svcName).Context(ctx).Do()
	if err != nil || gs.Uid != svcUID {
		t.Fatalf("service uid drift: err=%v", err)
	}

	ge, err := svc.Projects.Locations.Namespaces.Services.Endpoints.Get(epName).Context(ctx).Do()
	if err != nil || ge.Uid != epUID || ge.Address != "10.0.0.1" || ge.Port != 8080 {
		t.Fatalf("endpoint drift: err=%v ep=%+v", err, ge)
	}
}

func safeUID(ns *servicedirectory.Namespace) string {
	if ns == nil {
		return ""
	}

	return ns.Uid
}

// assertPatch updates labels/annotations/port and confirms the masked writes
// applied.
func assertPatch(t *testing.T, ctx context.Context, svc *servicedirectory.APIService, nsName, svcName, epName string) {
	t.Helper()

	if _, err := svc.Projects.Locations.Namespaces.Patch(nsName,
		&servicedirectory.Namespace{Labels: map[string]string{"env": "dev"}}).
		UpdateMask("labels").Context(ctx).Do(); err != nil {
		t.Fatalf("Namespaces.Patch: %v", err)
	}

	gotNs, err := svc.Projects.Locations.Namespaces.Get(nsName).Context(ctx).Do()
	if err != nil || gotNs.Labels["env"] != "dev" {
		t.Fatalf("namespace label patch not applied: %+v (%v)", gotNs, err)
	}

	if _, err := svc.Projects.Locations.Namespaces.Services.Endpoints.Patch(epName,
		&servicedirectory.Endpoint{Port: 9090}).UpdateMask("port").Context(ctx).Do(); err != nil {
		t.Fatalf("Endpoints.Patch: %v", err)
	}

	gotEp, err := svc.Projects.Locations.Namespaces.Services.Endpoints.Get(epName).Context(ctx).Do()
	if err != nil || gotEp.Port != 9090 || gotEp.Address != "10.0.0.1" {
		t.Fatalf("endpoint port patch/address survival failed: %+v (%v)", gotEp, err)
	}
}

// assertCascadeDelete deletes the namespace and confirms the service and
// endpoint were cascade-removed (a subsequent Get returns 404).
func assertCascadeDelete(t *testing.T, ctx context.Context, svc *servicedirectory.APIService, nsName, svcName, epName string) {
	t.Helper()

	if _, err := svc.Projects.Locations.Namespaces.Delete(nsName).Context(ctx).Do(); err != nil {
		t.Fatalf("Namespaces.Delete: %v", err)
	}

	if _, err := svc.Projects.Locations.Namespaces.Get(nsName).Context(ctx).Do(); !is404(err) {
		t.Fatalf("namespace must be gone, got %v", err)
	}

	if _, err := svc.Projects.Locations.Namespaces.Services.Get(svcName).Context(ctx).Do(); !is404(err) {
		t.Fatalf("service must be cascade-deleted, got %v", err)
	}

	if _, err := svc.Projects.Locations.Namespaces.Services.Endpoints.Get(epName).Context(ctx).Do(); !is404(err) {
		t.Fatalf("endpoint must be cascade-deleted, got %v", err)
	}
}

func is404(err error) bool {
	var gerr *googleapi.Error
	if errors.As(err, &gerr) {
		return gerr.Code == http.StatusNotFound
	}

	return false
}
