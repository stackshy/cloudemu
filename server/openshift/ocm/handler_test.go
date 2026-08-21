package ocm_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	ocmprovider "github.com/stackshy/cloudemu/v2/providers/openshift/ocm"
	ocmserver "github.com/stackshy/cloudemu/v2/server/openshift/ocm"
	"github.com/stackshy/cloudemu/v2/services/kubernetes"
)

// TestOCMHandler_RosaLifecycle drives the OCM REST API the way `rosa` does:
// login token -> create cluster -> describe -> list -> credentials (whose
// kubeconfig reaches a live OpenShift data plane) -> delete.
func TestOCMHandler_RosaLifecycle(t *testing.T) {
	api := kubernetes.NewAPIServer()
	dp := httptest.NewServer(api)
	t.Cleanup(dp.Close)
	api.SetBaseURL(dp.URL)

	mock := ocmprovider.New(config.NewOptions())
	mock.SetK8sAPI(api)
	ocm := httptest.NewServer(ocmserver.New(mock))
	t.Cleanup(ocm.Close)

	// rosa login -> SSO token.
	tok := do(t, http.MethodPost,
		ocm.URL+"/auth/realms/redhat-external/protocol/openid-connect/token", "grant_type=client_credentials")
	if tok.status != http.StatusOK {
		t.Fatalf("token: status %d, want 200\n%s", tok.status, tok.body)
	}

	var token struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
	}

	mustJSON(t, tok.body, &token)

	if token.AccessToken == "" || token.TokenType != "Bearer" {
		t.Fatalf("bad token response: %s", tok.body)
	}

	// rosa create cluster.
	create := do(t, http.MethodPost, ocm.URL+"/api/clusters_mgmt/v1/clusters",
		`{"name":"rosa1","region":{"id":"us-east-1"},"cloud_provider":{"id":"aws"},"product":{"id":"rosa"}}`)
	if create.status != http.StatusCreated {
		t.Fatalf("create: status %d, want 201\n%s", create.status, create.body)
	}

	var cluster struct {
		Kind  string `json:"kind"`
		ID    string `json:"id"`
		State string `json:"state"`
		API   struct {
			URL string `json:"url"`
		} `json:"api"`
	}

	mustJSON(t, create.body, &cluster)

	if cluster.Kind != "Cluster" || cluster.State != "ready" || cluster.ID == "" {
		t.Fatalf("unexpected cluster: %s", create.body)
	}

	clusterURL := ocm.URL + "/api/clusters_mgmt/v1/clusters/" + cluster.ID

	// rosa describe.
	if g := do(t, http.MethodGet, clusterURL, ""); g.status != http.StatusOK {
		t.Fatalf("describe: status %d, want 200\n%s", g.status, g.body)
	}

	// rosa list.
	list := do(t, http.MethodGet, ocm.URL+"/api/clusters_mgmt/v1/clusters", "")

	var cl struct {
		Kind  string `json:"kind"`
		Total int    `json:"total"`
	}

	mustJSON(t, list.body, &cl)

	if cl.Kind != "ClusterList" || cl.Total != 1 {
		t.Fatalf("list: kind=%q total=%d, want ClusterList/1", cl.Kind, cl.Total)
	}

	// credentials -> kubeconfig -> reach the OpenShift data plane.
	cred := do(t, http.MethodGet, clusterURL+"/credentials", "")
	if cred.status != http.StatusOK {
		t.Fatalf("credentials: status %d, want 200\n%s", cred.status, cred.body)
	}

	var creds struct {
		Kubeconfig string `json:"kubeconfig"`
	}

	mustJSON(t, cred.body, &creds)

	server := serverURL(t, []byte(creds.Kubeconfig))

	cv, err := http.Get(server + "/apis/config.openshift.io/v1/clusterversions/version") //nolint:noctx // test.
	if err != nil {
		t.Fatalf("GET clusterversion via rosa kubeconfig: %v", err)
	}

	cv.Body.Close()

	if cv.StatusCode != http.StatusOK {
		t.Fatalf("clusterversion via rosa kubeconfig: status %d, want 200", cv.StatusCode)
	}

	// rosa delete.
	if d := do(t, http.MethodDelete, clusterURL, ""); d.status != http.StatusNoContent {
		t.Fatalf("delete: status %d, want 204\n%s", d.status, d.body)
	}

	if g := do(t, http.MethodGet, clusterURL, ""); g.status != http.StatusNotFound {
		t.Errorf("get after delete: status %d, want 404", g.status)
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

func mustJSON(t *testing.T, body string, v any) {
	t.Helper()

	if err := json.Unmarshal([]byte(body), v); err != nil {
		t.Fatalf("decode %T: %v\n%s", v, err, body)
	}
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
