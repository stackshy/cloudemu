package aro_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	aroprovider "github.com/stackshy/cloudemu/v2/providers/azure/aro"
	aroserver "github.com/stackshy/cloudemu/v2/server/azure/aro"
	"github.com/stackshy/cloudemu/v2/services/kubernetes"
)

// TestAROHandler_ARMLifecycle drives the ARO control plane over the real ARM
// JSON wire: PUT create -> GET -> listAdminCredentials (whose kubeconfig reaches
// a live OpenShift data plane) -> DELETE.
func TestAROHandler_ARMLifecycle(t *testing.T) {
	// Data plane.
	api := kubernetes.NewAPIServer()
	dp := httptest.NewServer(api)
	t.Cleanup(dp.Close)
	api.SetBaseURL(dp.URL)

	// ARO ARM control plane.
	mock := aroprovider.New(config.NewOptions())
	mock.SetK8sAPI(api)
	arm := httptest.NewServer(aroserver.New(mock))
	t.Cleanup(arm.Close)

	const clusterPath = "/subscriptions/sub-1/resourceGroups/rg-1/providers/" +
		"Microsoft.RedHatOpenShift/openShiftClusters/ocp1"
	base := arm.URL + clusterPath + "?api-version=2023-09-04"

	// PUT — create.
	putBody := `{"location":"eastus","properties":{"clusterProfile":{"version":"4.16.0"}}}`
	put := do(t, http.MethodPut, base, putBody)

	if put.status != http.StatusOK {
		t.Fatalf("PUT: status %d, want 200\n%s", put.status, put.body)
	}

	var cluster struct {
		Type       string `json:"type"`
		Properties struct {
			ProvisioningState string `json:"provisioningState"`
			ClusterProfile    struct {
				Version string `json:"version"`
			} `json:"clusterProfile"`
		} `json:"properties"`
	}

	if err := json.Unmarshal([]byte(put.body), &cluster); err != nil {
		t.Fatalf("decode PUT response: %v\n%s", err, put.body)
	}

	if cluster.Properties.ProvisioningState != "Succeeded" {
		t.Errorf("provisioningState: got %q, want Succeeded", cluster.Properties.ProvisioningState)
	}

	if cluster.Type != "Microsoft.RedHatOpenShift/openShiftClusters" {
		t.Errorf("type: got %q", cluster.Type)
	}

	// GET.
	if g := do(t, http.MethodGet, base, ""); g.status != http.StatusOK {
		t.Fatalf("GET: status %d, want 200\n%s", g.status, g.body)
	}

	// listAdminCredentials -> kubeconfig -> reach the OpenShift data plane.
	cred := do(t, http.MethodPost, arm.URL+clusterPath+"/listAdminCredentials?api-version=2023-09-04", "")
	if cred.status != http.StatusOK {
		t.Fatalf("listAdminCredentials: status %d, want 200\n%s", cred.status, cred.body)
	}

	var creds struct {
		Kubeconfig []byte `json:"kubeconfig"`
	}

	if err := json.Unmarshal([]byte(cred.body), &creds); err != nil {
		t.Fatalf("decode credentials: %v", err)
	}

	server := serverURL(t, creds.Kubeconfig)

	cv, err := http.Get(server + "/apis/config.openshift.io/v1/clusterversions/version") //nolint:noctx // test.
	if err != nil {
		t.Fatalf("GET clusterversion via ARO kubeconfig: %v", err)
	}

	cv.Body.Close()

	if cv.StatusCode != http.StatusOK {
		t.Fatalf("clusterversion via ARO kubeconfig: status %d, want 200", cv.StatusCode)
	}

	// DELETE.
	if d := do(t, http.MethodDelete, base, ""); d.status != http.StatusNoContent {
		t.Fatalf("DELETE: status %d, want 204\n%s", d.status, d.body)
	}

	if g := do(t, http.MethodGet, base, ""); g.status == http.StatusOK {
		t.Error("GET after delete returned 200, want error")
	}
}

type resp struct {
	status int
	body   string
}

func do(t *testing.T, method, url, body string) resp {
	t.Helper()

	var rdr io.Reader
	if body != "" {
		rdr = bytes.NewReader([]byte(body))
	}

	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}

	defer r.Body.Close()

	b, _ := io.ReadAll(r.Body)

	return resp{status: r.StatusCode, body: string(b)}
}

func serverURL(t *testing.T, kubeconfig []byte) string {
	t.Helper()

	for _, line := range strings.Split(string(kubeconfig), "\n") {
		if line = strings.TrimSpace(line); strings.HasPrefix(line, "server:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "server:"))
		}
	}

	t.Fatalf("no server URL in kubeconfig:\n%s", kubeconfig)

	return ""
}
