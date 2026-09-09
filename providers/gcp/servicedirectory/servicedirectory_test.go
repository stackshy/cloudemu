package servicedirectory_test

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/gcp/servicedirectory"
	sddriver "github.com/stackshy/cloudemu/v2/services/servicedirectory/driver"
)

const (
	project  = "demo"
	location = "us-central1"
)

func newMock() *servicedirectory.Mock { return servicedirectory.New(config.NewOptions()) }

func ctx() context.Context { return context.Background() }

func mustNamespace(t *testing.T, m *servicedirectory.Mock, id string, labels map[string]string) *sddriver.Namespace {
	t.Helper()

	ns, err := m.CreateNamespace(ctx(), &sddriver.NamespaceConfig{
		Project: project, Location: location, ID: id, Labels: labels,
	})
	if err != nil {
		t.Fatalf("CreateNamespace: %v", err)
	}

	return ns
}

func mustService(t *testing.T, m *servicedirectory.Mock, ns, id string, ann map[string]string) *sddriver.Service {
	t.Helper()

	svc, err := m.CreateService(ctx(), &sddriver.ServiceConfig{
		Project: project, Location: location, Namespace: ns, ID: id, Annotations: ann,
	})
	if err != nil {
		t.Fatalf("CreateService: %v", err)
	}

	return svc
}

func mustEndpoint(t *testing.T, m *servicedirectory.Mock, ns, svc, id, addr string, port int) *sddriver.Endpoint {
	t.Helper()

	ep, err := m.CreateEndpoint(ctx(), &sddriver.EndpointConfig{
		Project: project, Location: location, Namespace: ns, Service: svc, ID: id, Address: addr, Port: port,
	})
	if err != nil {
		t.Fatalf("CreateEndpoint: %v", err)
	}

	return ep
}

func TestNamespaceUIDStableAcrossReads(t *testing.T) {
	m := newMock()
	ns := mustNamespace(t, m, "ns1", map[string]string{"env": "prod"})

	if ns.UID == "" {
		t.Fatal("uid must be minted at create")
	}

	got, err := m.GetNamespace(ctx(), project, location, "ns1")
	if err != nil {
		t.Fatalf("GetNamespace: %v", err)
	}

	if got.UID != ns.UID {
		t.Fatalf("uid drifted: create=%q get=%q", ns.UID, got.UID)
	}

	// Patch must not regenerate uid.
	patched, err := m.PatchNamespace(ctx(), &sddriver.NamespaceConfig{
		Project: project, Location: location, ID: "ns1", Labels: map[string]string{"env": "dev"},
	}, []string{"labels"})
	if err != nil {
		t.Fatalf("PatchNamespace: %v", err)
	}

	if patched.UID != ns.UID {
		t.Fatalf("uid changed on patch: %q -> %q", ns.UID, patched.UID)
	}

	if patched.Labels["env"] != "dev" {
		t.Fatalf("labels not updated: %+v", patched.Labels)
	}
}

func TestServiceRequiresParentNamespace(t *testing.T) {
	m := newMock()

	_, err := m.CreateService(ctx(), &sddriver.ServiceConfig{
		Project: project, Location: location, Namespace: "missing", ID: "svc",
	})
	if !cerrors.IsNotFound(err) {
		t.Fatalf("want NotFound for missing parent namespace, got %v", err)
	}
}

func TestEndpointRequiresParentService(t *testing.T) {
	m := newMock()
	mustNamespace(t, m, "ns1", nil)

	_, err := m.CreateEndpoint(ctx(), &sddriver.EndpointConfig{
		Project: project, Location: location, Namespace: "ns1", Service: "missing", ID: "ep",
	})
	if !cerrors.IsNotFound(err) {
		t.Fatalf("want NotFound for missing parent service, got %v", err)
	}
}

func TestDuplicateCreateConflicts(t *testing.T) {
	m := newMock()
	mustNamespace(t, m, "ns1", nil)

	_, err := m.CreateNamespace(ctx(), &sddriver.NamespaceConfig{Project: project, Location: location, ID: "ns1"})
	if !cerrors.IsAlreadyExists(err) {
		t.Fatalf("want AlreadyExists, got %v", err)
	}
}

func TestMissingIDInvalid(t *testing.T) {
	m := newMock()

	_, err := m.CreateNamespace(ctx(), &sddriver.NamespaceConfig{Project: project, Location: location})
	if !cerrors.IsInvalidArgument(err) {
		t.Fatalf("want InvalidArgument for empty id, got %v", err)
	}
}

func TestEndpointPortValidation(t *testing.T) {
	m := newMock()
	mustNamespace(t, m, "ns1", nil)
	mustService(t, m, "ns1", "svc1", nil)

	_, err := m.CreateEndpoint(ctx(), &sddriver.EndpointConfig{
		Project: project, Location: location, Namespace: "ns1", Service: "svc1", ID: "ep", Port: 70000,
	})
	if !cerrors.IsInvalidArgument(err) {
		t.Fatalf("want InvalidArgument for out-of-range port, got %v", err)
	}
}

func TestEndpointPatchMaskedFieldsOnly(t *testing.T) {
	m := newMock()
	mustNamespace(t, m, "ns1", nil)
	mustService(t, m, "ns1", "svc1", nil)
	ep := mustEndpoint(t, m, "ns1", "svc1", "ep1", "10.0.0.1", 8080)

	// Patch only the port; address and uid must survive untouched.
	patched, err := m.PatchEndpoint(ctx(), &sddriver.EndpointConfig{
		Project: project, Location: location, Namespace: "ns1", Service: "svc1", ID: "ep1", Port: 9090,
	}, []string{"port"})
	if err != nil {
		t.Fatalf("PatchEndpoint: %v", err)
	}

	if patched.Port != 9090 {
		t.Fatalf("port not updated: %d", patched.Port)
	}

	if patched.Address != "10.0.0.1" {
		t.Fatalf("unmasked address must survive, got %q", patched.Address)
	}

	if patched.UID != ep.UID {
		t.Fatalf("uid changed on patch: %q -> %q", ep.UID, patched.UID)
	}
}

func TestDeleteNamespaceCascades(t *testing.T) {
	m := newMock()
	mustNamespace(t, m, "ns1", nil)
	mustService(t, m, "ns1", "svc1", nil)
	mustEndpoint(t, m, "ns1", "svc1", "ep1", "10.0.0.1", 8080)

	if err := m.DeleteNamespace(ctx(), project, location, "ns1"); err != nil {
		t.Fatalf("DeleteNamespace: %v", err)
	}

	if _, err := m.GetService(ctx(), project, location, "ns1", "svc1"); !cerrors.IsNotFound(err) {
		t.Fatalf("service must be cascade-deleted, got %v", err)
	}

	if _, err := m.GetEndpoint(ctx(), project, location, "ns1", "svc1", "ep1"); !cerrors.IsNotFound(err) {
		t.Fatalf("endpoint must be cascade-deleted, got %v", err)
	}
}

func TestDeleteServiceCascadesEndpoints(t *testing.T) {
	m := newMock()
	mustNamespace(t, m, "ns1", nil)
	mustService(t, m, "ns1", "svc1", nil)
	mustEndpoint(t, m, "ns1", "svc1", "ep1", "10.0.0.1", 8080)

	if err := m.DeleteService(ctx(), project, location, "ns1", "svc1"); err != nil {
		t.Fatalf("DeleteService: %v", err)
	}

	if _, err := m.GetEndpoint(ctx(), project, location, "ns1", "svc1", "ep1"); !cerrors.IsNotFound(err) {
		t.Fatalf("endpoint must be cascade-deleted, got %v", err)
	}

	// Sibling namespace's resources are untouched by a scoped delete.
	if _, err := m.GetNamespace(ctx(), project, location, "ns1"); err != nil {
		t.Fatalf("parent namespace must survive service delete: %v", err)
	}
}

func TestListScoping(t *testing.T) {
	m := newMock()
	mustNamespace(t, m, "ns1", nil)
	mustNamespace(t, m, "ns2", nil)
	mustService(t, m, "ns1", "a", nil)
	mustService(t, m, "ns1", "b", nil)
	mustService(t, m, "ns2", "c", nil)

	svcs, err := m.ListServices(ctx(), project, location, "ns1")
	if err != nil {
		t.Fatalf("ListServices: %v", err)
	}

	if len(svcs) != 2 {
		t.Fatalf("want 2 services under ns1, got %d", len(svcs))
	}

	if svcs[0].ID != "a" || svcs[1].ID != "b" {
		t.Fatalf("services not id-ordered/scoped: %+v", svcs)
	}
}
