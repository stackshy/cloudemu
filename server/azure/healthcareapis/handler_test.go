package healthcareapis_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/azure/healthcareapis"
	healthcareapissrv "github.com/stackshy/cloudemu/v2/server/azure/healthcareapis"
)

const (
	apiVer   = "?api-version=2022-06-01"
	wsBase   = "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.HealthcareApis/workspaces/"
	wsBody   = `{"location": "West US", "tags": {"env": "dev"}}`
	fhirBody = `{"location": "West US", "kind": "fhir-R4", "identity": {"type": "SystemAssigned"},` +
		` "properties": {"corsConfiguration": {"origins": ["*"], "maxAge": 60, "allowCredentials": false}}}`
	dicomBody = `{"location": "West US", "identity": {"type": "SystemAssigned"}}`
)

func newServer(t *testing.T) *httptest.Server {
	t.Helper()

	mock := healthcareapis.New(config.NewOptions())
	h := healthcareapissrv.New(mock)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	return srv
}

func do(t *testing.T, srv *httptest.Server, method, path, body string) (int, []byte) {
	t.Helper()

	var rdr io.Reader
	if body != "" {
		rdr = bytes.NewBufferString(body)
	}

	req, err := http.NewRequestWithContext(context.Background(), method, srv.URL+path, rdr)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, _ := io.ReadAll(resp.Body)

	return resp.StatusCode, raw
}

func createWorkspace(t *testing.T, srv *httptest.Server) {
	t.Helper()

	code, _ := do(t, srv, http.MethodPut, wsBase+"ws1"+apiVer, wsBody)
	if code != http.StatusCreated {
		t.Fatalf("create workspace: status=%d, want 201", code)
	}
}

func TestWorkspaceCreateGetByteStable(t *testing.T) {
	srv := newServer(t)
	createWorkspace(t, srv)

	_, g1 := do(t, srv, http.MethodGet, wsBase+"ws1"+apiVer, "")
	_, g2 := do(t, srv, http.MethodGet, wsBase+"ws1"+apiVer, "")

	if !bytes.Equal(g1, g2) {
		t.Errorf("workspace GET not byte-stable:\n%s\n%s", g1, g2)
	}

	var ws struct {
		Type       string `json:"type"`
		Properties struct {
			ProvisioningState   string `json:"provisioningState"`
			PublicNetworkAccess string `json:"publicNetworkAccess"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(g1, &ws); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if ws.Type != "Microsoft.HealthcareApis/workspaces" {
		t.Errorf("type = %q", ws.Type)
	}

	if ws.Properties.ProvisioningState != "Succeeded" || ws.Properties.PublicNetworkAccess != "Enabled" {
		t.Errorf("properties = %+v", ws.Properties)
	}
}

func TestWorkspacePatchTagsReplace(t *testing.T) {
	srv := newServer(t)
	createWorkspace(t, srv)

	code, body := do(t, srv, http.MethodPatch, wsBase+"ws1"+apiVer, `{"tags": {"team": "ops"}}`)
	if code != http.StatusOK {
		t.Fatalf("patch: status=%d body=%s", code, body)
	}

	var ws struct {
		Tags map[string]string `json:"tags"`
	}
	_ = json.Unmarshal(body, &ws)

	if _, ok := ws.Tags["env"]; ok {
		t.Errorf("tags = %v, want env replaced", ws.Tags)
	}

	if ws.Tags["team"] != "ops" {
		t.Errorf("tags = %v, want team=ops", ws.Tags)
	}
}

func TestFhirIdentityAndServiceHostStable(t *testing.T) {
	srv := newServer(t)
	createWorkspace(t, srv)

	code, _ := do(t, srv, http.MethodPut, wsBase+"ws1/fhirservices/fhir1"+apiVer, fhirBody)
	if code != http.StatusCreated {
		t.Fatalf("create fhir: status=%d", code)
	}

	_, g1 := do(t, srv, http.MethodGet, wsBase+"ws1/fhirservices/fhir1"+apiVer, "")
	_, g2 := do(t, srv, http.MethodGet, wsBase+"ws1/fhirservices/fhir1"+apiVer, "")
	if !bytes.Equal(g1, g2) {
		t.Errorf("fhir GET not byte-stable:\n%s\n%s", g1, g2)
	}

	var f struct {
		Type     string `json:"type"`
		Identity struct {
			Type        string `json:"type"`
			PrincipalID string `json:"principalId"`
			TenantID    string `json:"tenantId"`
		} `json:"identity"`
		Properties struct {
			Auth struct {
				Authority string `json:"authority"`
				Audience  string `json:"audience"`
			} `json:"authenticationConfiguration"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(g1, &f); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if f.Type != "Microsoft.HealthcareApis/workspaces/fhirservices" {
		t.Errorf("type = %q", f.Type)
	}

	if f.Identity.PrincipalID == "" || f.Identity.TenantID == "" {
		t.Errorf("identity ids not present: %+v", f.Identity)
	}

	if f.Properties.Auth.Audience != "https://ws1-fhir1.fhir.azurehealthcareapis.com" {
		t.Errorf("audience = %q", f.Properties.Auth.Audience)
	}
}

func TestDicomServiceURLAndAuthComputed(t *testing.T) {
	srv := newServer(t)
	createWorkspace(t, srv)

	code, _ := do(t, srv, http.MethodPut, wsBase+"ws1/dicomservices/dicom1"+apiVer, dicomBody)
	if code != http.StatusCreated {
		t.Fatalf("create dicom: status=%d", code)
	}

	_, body := do(t, srv, http.MethodGet, wsBase+"ws1/dicomservices/dicom1"+apiVer, "")

	var d struct {
		Type       string `json:"type"`
		Properties struct {
			ServiceURL string `json:"serviceUrl"`
			Auth       struct {
				Authority string   `json:"authority"`
				Audiences []string `json:"audiences"`
			} `json:"authenticationConfiguration"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(body, &d); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if d.Type != "Microsoft.HealthcareApis/workspaces/dicomservices" {
		t.Errorf("type = %q", d.Type)
	}

	if d.Properties.ServiceURL != "https://ws1-dicom1.dicom.azurehealthcareapis.com" {
		t.Errorf("serviceUrl = %q", d.Properties.ServiceURL)
	}

	if d.Properties.Auth.Authority == "" || len(d.Properties.Auth.Audiences) == 0 {
		t.Errorf("computed auth missing: %+v", d.Properties.Auth)
	}
}

func TestChildUnderMissingWorkspaceIs404(t *testing.T) {
	srv := newServer(t)

	code, _ := do(t, srv, http.MethodPut, wsBase+"ghost/fhirservices/fhir1"+apiVer, fhirBody)
	if code != http.StatusNotFound {
		t.Errorf("create under missing workspace: status=%d, want 404", code)
	}
}

func TestListChildren(t *testing.T) {
	srv := newServer(t)
	createWorkspace(t, srv)

	_, _ = do(t, srv, http.MethodPut, wsBase+"ws1/fhirservices/fhir1"+apiVer, fhirBody)
	_, _ = do(t, srv, http.MethodPut, wsBase+"ws1/dicomservices/dicom1"+apiVer, dicomBody)

	code, body := do(t, srv, http.MethodGet, wsBase+"ws1/fhirservices"+apiVer, "")
	if code != http.StatusOK {
		t.Fatalf("list fhir: status=%d", code)
	}

	var list struct {
		Value []struct {
			Name string `json:"name"`
		} `json:"value"`
	}
	_ = json.Unmarshal(body, &list)

	if len(list.Value) != 1 || list.Value[0].Name != "fhir1" {
		t.Errorf("fhir list = %+v", list.Value)
	}
}

func TestDeleteWorkspaceCascadesAndIsIdempotent(t *testing.T) {
	srv := newServer(t)
	createWorkspace(t, srv)
	_, _ = do(t, srv, http.MethodPut, wsBase+"ws1/fhirservices/fhir1"+apiVer, fhirBody)

	code, _ := do(t, srv, http.MethodDelete, wsBase+"ws1"+apiVer, "")
	if code != http.StatusOK {
		t.Fatalf("delete workspace: status=%d, want 200", code)
	}

	code, _ = do(t, srv, http.MethodGet, wsBase+"ws1/fhirservices/fhir1"+apiVer, "")
	if code != http.StatusNotFound {
		t.Errorf("fhir survived workspace delete: status=%d", code)
	}

	code, _ = do(t, srv, http.MethodGet, wsBase+"ws1"+apiVer, "")
	if code != http.StatusNotFound {
		t.Errorf("workspace GET after delete: status=%d, want 404", code)
	}

	// Idempotent delete of a now-missing workspace returns 204.
	code, _ = do(t, srv, http.MethodDelete, wsBase+"ws1"+apiVer, "")
	if code != http.StatusNoContent {
		t.Errorf("second delete: status=%d, want 204", code)
	}
}

func TestPurgeResourceGroupCascades(t *testing.T) {
	mock := healthcareapis.New(config.NewOptions())
	h := healthcareapissrv.New(mock)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	if code, _ := do(t, srv, http.MethodPut, wsBase+"ws1"+apiVer, wsBody); code != http.StatusCreated {
		t.Fatalf("create workspace: status=%d", code)
	}

	_, _ = do(t, srv, http.MethodPut, wsBase+"ws1/dicomservices/dicom1"+apiVer, dicomBody)

	if err := h.PurgeResourceGroup(context.Background(), "sub1", "rg1"); err != nil {
		t.Fatalf("purge: %v", err)
	}

	if code, _ := do(t, srv, http.MethodGet, wsBase+"ws1"+apiVer, ""); code != http.StatusNotFound {
		t.Errorf("workspace survived purge: status=%d", code)
	}

	if code, _ := do(t, srv, http.MethodGet, wsBase+"ws1/dicomservices/dicom1"+apiVer, ""); code != http.StatusNotFound {
		t.Errorf("dicom survived purge: status=%d", code)
	}
}
