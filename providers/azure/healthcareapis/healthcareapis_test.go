package healthcareapis_test

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/azure/healthcareapis"
)

func newMock() *healthcareapis.Mock {
	return healthcareapis.New(config.NewOptions())
}

func sptr(v string) *string { return &v }
func bptr(v bool) *bool     { return &v }

func createWorkspace(t *testing.T, m *healthcareapis.Mock) healthcareapis.Workspace {
	t.Helper()

	w, isNew, err := m.CreateOrUpdateWorkspace(context.Background(), "sub", "rg", "ws1", "West US",
		&healthcareapis.WorkspaceInput{Tags: map[string]string{"env": "dev"}})
	if err != nil || !isNew {
		t.Fatalf("create workspace: err=%v isNew=%v", err, isNew)
	}

	return w
}

func TestWorkspaceComputedFields(t *testing.T) {
	m := newMock()
	w := createWorkspace(t, m)

	if w.ProvisioningState != "Succeeded" {
		t.Errorf("provisioningState = %q, want Succeeded", w.ProvisioningState)
	}

	if w.PublicNetworkAccess != "Enabled" {
		t.Errorf("publicNetworkAccess = %q, want Enabled", w.PublicNetworkAccess)
	}

	if w.Etag == "" {
		t.Errorf("etag is empty, want a value")
	}

	want := "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.HealthcareApis/workspaces/ws1"
	if w.ARMID() != want {
		t.Errorf("ARMID = %q, want %q", w.ARMID(), want)
	}
}

func TestWorkspaceEtagStableAcrossReads(t *testing.T) {
	m := newMock()
	createWorkspace(t, m)

	first, _ := m.GetWorkspace(context.Background(), "sub", "rg", "ws1")
	second, _ := m.GetWorkspace(context.Background(), "sub", "rg", "ws1")

	if first.Etag != second.Etag || first.ProvisioningState != second.ProvisioningState {
		t.Errorf("workspace computed fields drifted: %+v vs %+v", first, second)
	}
}

func TestWorkspacePatchTagsReplace(t *testing.T) {
	m := newMock()
	createWorkspace(t, m)

	// A resource-level update REPLACES tags, not merges.
	w, _, err := m.CreateOrUpdateWorkspace(context.Background(), "sub", "rg", "ws1", "West US",
		&healthcareapis.WorkspaceInput{Tags: map[string]string{"team": "ops"}})
	if err != nil {
		t.Fatalf("patch: %v", err)
	}

	if _, ok := w.Tags["env"]; ok {
		t.Errorf("tags = %v, want old key 'env' replaced", w.Tags)
	}

	if w.Tags["team"] != "ops" {
		t.Errorf("tags = %v, want team=ops", w.Tags)
	}
}

func TestWorkspacePatchPreservesEtag(t *testing.T) {
	m := newMock()
	orig := createWorkspace(t, m)

	w, _, _ := m.CreateOrUpdateWorkspace(context.Background(), "sub", "rg", "ws1", "West US",
		&healthcareapis.WorkspaceInput{PublicNetworkAccess: sptr("Disabled")})

	if w.Etag != orig.Etag {
		t.Errorf("etag changed on update: %q -> %q", orig.Etag, w.Etag)
	}

	if w.PublicNetworkAccess != "Disabled" {
		t.Errorf("publicNetworkAccess = %q, want Disabled", w.PublicNetworkAccess)
	}
}

func TestFhirComputedFieldsAndAuthDefaults(t *testing.T) {
	m := newMock()
	createWorkspace(t, m)

	f, isNew, err := m.CreateOrUpdateFhir(context.Background(), "sub", "rg", "ws1", "fhir1", "West US",
		&healthcareapis.FhirInput{Identity: &healthcareapis.Identity{Type: "SystemAssigned"}})
	if err != nil || !isNew {
		t.Fatalf("create fhir: err=%v isNew=%v", err, isNew)
	}

	if f.Kind != "fhir-R4" {
		t.Errorf("kind = %q, want fhir-R4", f.Kind)
	}

	if f.Audience != "https://ws1-fhir1.fhir.azurehealthcareapis.com" {
		t.Errorf("audience = %q, want deterministic service host", f.Audience)
	}

	if f.Authority == "" || f.Authority[:8] != "https://" {
		t.Errorf("authority = %q, want an https authority", f.Authority)
	}

	if f.Identity == nil || f.Identity.PrincipalID == "" || f.Identity.TenantID == "" {
		t.Fatalf("identity ids not minted: %+v", f.Identity)
	}
}

func TestFhirIdentityPrincipalIDStable(t *testing.T) {
	m := newMock()
	createWorkspace(t, m)

	_, _, _ = m.CreateOrUpdateFhir(context.Background(), "sub", "rg", "ws1", "fhir1", "West US",
		&healthcareapis.FhirInput{Identity: &healthcareapis.Identity{Type: "SystemAssigned"}})

	first, _ := m.GetFhir(context.Background(), "sub", "rg", "ws1", "fhir1")
	second, _ := m.GetFhir(context.Background(), "sub", "rg", "ws1", "fhir1")

	if first.Identity.PrincipalID != second.Identity.PrincipalID ||
		first.Identity.TenantID != second.Identity.TenantID ||
		first.Audience != second.Audience || first.Etag != second.Etag {
		t.Errorf("fhir computed fields drifted: %+v vs %+v", first.Identity, second.Identity)
	}

	// A tags-only PATCH must not wipe the minted identity or auth.
	patched, _, _ := m.CreateOrUpdateFhir(context.Background(), "sub", "rg", "ws1", "fhir1", "West US",
		&healthcareapis.FhirInput{Tags: map[string]string{"k": "v"}})
	if patched.Identity == nil || patched.Identity.PrincipalID != first.Identity.PrincipalID {
		t.Errorf("identity lost on tags-only patch: %+v", patched.Identity)
	}

	if patched.Audience != first.Audience {
		t.Errorf("audience changed on tags-only patch: %q -> %q", first.Audience, patched.Audience)
	}
}

func TestFhirParentMustExist(t *testing.T) {
	m := newMock()

	_, _, err := m.CreateOrUpdateFhir(context.Background(), "sub", "rg", "ghost", "fhir1", "West US",
		&healthcareapis.FhirInput{})
	if !cerrors.IsNotFound(err) {
		t.Errorf("create under missing workspace: err=%v, want NotFound", err)
	}
}

func TestDicomComputedServiceURLAndAuth(t *testing.T) {
	m := newMock()
	createWorkspace(t, m)

	d, isNew, err := m.CreateOrUpdateDicom(context.Background(), "sub", "rg", "ws1", "dicom1", "West US",
		&healthcareapis.DicomInput{Identity: &healthcareapis.Identity{Type: "SystemAssigned"}})
	if err != nil || !isNew {
		t.Fatalf("create dicom: err=%v isNew=%v", err, isNew)
	}

	if d.ServiceURL != "https://ws1-dicom1.dicom.azurehealthcareapis.com" {
		t.Errorf("serviceUrl = %q, want deterministic host", d.ServiceURL)
	}

	if len(d.Audiences) == 0 || d.Authority == "" {
		t.Errorf("read-only auth not minted: authority=%q audiences=%v", d.Authority, d.Audiences)
	}

	first, _ := m.GetDicom(context.Background(), "sub", "rg", "ws1", "dicom1")
	second, _ := m.GetDicom(context.Background(), "sub", "rg", "ws1", "dicom1")
	if first.ServiceURL != second.ServiceURL || first.Etag != second.Etag ||
		first.Identity.PrincipalID != second.Identity.PrincipalID {
		t.Errorf("dicom computed fields drifted: %+v vs %+v", first, second)
	}
}

func TestDicomCorsRoundTrip(t *testing.T) {
	m := newMock()
	createWorkspace(t, m)

	cors := &healthcareapis.Cors{
		Origins:          []string{"*"},
		Methods:          []string{"GET", "POST"},
		MaxAge:           new(int),
		AllowCredentials: bptr(false),
	}

	d, _, err := m.CreateOrUpdateDicom(context.Background(), "sub", "rg", "ws1", "dicom1", "West US",
		&healthcareapis.DicomInput{Cors: cors})
	if err != nil {
		t.Fatalf("create dicom: %v", err)
	}

	if d.Cors == nil || d.Cors.MaxAge == nil || *d.Cors.MaxAge != 0 ||
		d.Cors.AllowCredentials == nil || *d.Cors.AllowCredentials {
		t.Errorf("cors pointer fields did not round-trip 0/false: %+v", d.Cors)
	}
}

func TestDeleteWorkspaceCascadesChildren(t *testing.T) {
	m := newMock()
	createWorkspace(t, m)

	_, _, _ = m.CreateOrUpdateFhir(context.Background(), "sub", "rg", "ws1", "fhir1", "West US", &healthcareapis.FhirInput{})
	_, _, _ = m.CreateOrUpdateDicom(context.Background(), "sub", "rg", "ws1", "dicom1", "West US", &healthcareapis.DicomInput{})

	existed, _ := m.DeleteWorkspace(context.Background(), "sub", "rg", "ws1")
	if !existed {
		t.Fatalf("delete workspace: existed=false")
	}

	if _, err := m.GetFhir(context.Background(), "sub", "rg", "ws1", "fhir1"); !cerrors.IsNotFound(err) {
		t.Errorf("fhir survived workspace delete: err=%v", err)
	}

	if _, err := m.GetDicom(context.Background(), "sub", "rg", "ws1", "dicom1"); !cerrors.IsNotFound(err) {
		t.Errorf("dicom survived workspace delete: err=%v", err)
	}
}

func TestPurgeResourceGroupKeepsSiblings(t *testing.T) {
	m := newMock()
	createWorkspace(t, m)

	_, _, _ = m.CreateOrUpdateWorkspace(context.Background(), "sub", "other", "ws2", "West US", &healthcareapis.WorkspaceInput{})
	_, _, _ = m.CreateOrUpdateFhir(context.Background(), "sub", "rg", "ws1", "fhir1", "West US", &healthcareapis.FhirInput{})

	if err := m.PurgeResourceGroup(context.Background(), "sub", "rg"); err != nil {
		t.Fatalf("purge: %v", err)
	}

	if _, err := m.GetWorkspace(context.Background(), "sub", "rg", "ws1"); !cerrors.IsNotFound(err) {
		t.Errorf("purged workspace still present: err=%v", err)
	}

	if _, err := m.GetFhir(context.Background(), "sub", "rg", "ws1", "fhir1"); !cerrors.IsNotFound(err) {
		t.Errorf("purged fhir still present: err=%v", err)
	}

	if _, err := m.GetWorkspace(context.Background(), "sub", "other", "ws2"); err != nil {
		t.Errorf("sibling workspace in other RG was purged: err=%v", err)
	}
}

func TestListChildrenSortedAndScoped(t *testing.T) {
	m := newMock()
	createWorkspace(t, m)

	for _, n := range []string{"beta", "alpha"} {
		if _, _, err := m.CreateOrUpdateFhir(context.Background(), "sub", "rg", "ws1", n, "West US",
			&healthcareapis.FhirInput{}); err != nil {
			t.Fatalf("create fhir %s: %v", n, err)
		}
	}

	list, err := m.ListFhirByWorkspace(context.Background(), "sub", "rg", "ws1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	if len(list) != 2 || list[0].Name != "alpha" || list[1].Name != "beta" {
		t.Errorf("list = %+v, want sorted [alpha beta]", list)
	}
}

func TestReadsDoNotAliasStore(t *testing.T) {
	m := newMock()
	createWorkspace(t, m)

	_, _, _ = m.CreateOrUpdateFhir(context.Background(), "sub", "rg", "ws1", "fhir1", "West US",
		&healthcareapis.FhirInput{Tags: map[string]string{"k": "v"}, Identity: &healthcareapis.Identity{Type: "SystemAssigned"}})

	got, _ := m.GetFhir(context.Background(), "sub", "rg", "ws1", "fhir1")
	got.Tags["k"] = "mutated"
	if got.Identity != nil {
		got.Identity.PrincipalID = "mutated"
	}

	again, _ := m.GetFhir(context.Background(), "sub", "rg", "ws1", "fhir1")
	if again.Tags["k"] != "v" {
		t.Errorf("tag mutation leaked into store: %v", again.Tags)
	}

	if again.Identity.PrincipalID == "mutated" {
		t.Errorf("identity mutation leaked into store")
	}
}
