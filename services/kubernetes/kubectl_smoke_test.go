//go:build kubectl

// This file is gated behind the "kubectl" build tag, so it never runs as
// part of the normal `go test ./...` gate — it needs a real kubectl binary
// on PATH and is meant to be run explicitly:
//
//	go test -tags kubectl ./services/kubernetes/... -run TestKubectlSmoke -v
//
// CI WIRING NOTE (not implemented here — this file intentionally does not
// touch .github/workflows): a workflow job that wants this test needs to
// install kubectl before invoking go test, e.g.:
//
//   - uses: azure/setup-kubectl@v4
//     with:
//     version: 'v1.29.0'
//   - run: go test -tags kubectl ./services/kubernetes/... -run TestKubectlSmoke -v
//
// COVERAGE: the emulator here is served over plain HTTP via httptest.NewServer
// (no TLS), so this test drives real kubectl against it directly — discovery
// negotiation (kubectl refuses to proceed without a working /api, /apis,
// /version, and OpenAPI, all exercised implicitly by every command below),
// `kubectl get namespaces`, and a create+read round trip via `kubectl apply
// -f` / `kubectl get deploy` (client-side apply: GET, then POST-if-missing or
// PATCH-if-present, going through the same createDeployment/patchDeployment
// handlers the Go-client tests cover).
//
// NOT COVERED: TLS/certificate trust (the SDK-compat `serve` entrypoint's
// kubeconfigs point at an HTTPS endpoint backed by internal/k8spki — this
// harness uses a plain-HTTP kubeconfig instead, since kubectl doesn't need
// TLS to talk to a plain httptest server); ?watch=true streaming; and
// RBAC/NetworkPolicy evaluation (covered by rbac_test.go / networkpolicy_test.go).
package kubernetes_test

import (
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/kubernetes"
)

func TestKubectlSmoke(t *testing.T) {
	kubectlPath, err := exec.LookPath("kubectl")
	if err != nil {
		t.Skip("kubectl not found on PATH; skipping kubectl CI smoke test")
	}

	api := kubernetes.NewAPIServer()
	ts := httptest.NewServer(api)
	t.Cleanup(ts.Close)
	api.SetBaseURL(ts.URL)

	uid, _ := api.RegisterCluster()

	kubeconfig := writeSmokeKubeconfig(t, ts.URL, uid)

	runKubectl(t, kubectlPath, kubeconfig, "version", "--client")
	runKubectl(t, kubectlPath, kubeconfig, "get", "namespaces")

	manifest := writeSmokeDeploymentManifest(t)
	runKubectl(t, kubectlPath, kubeconfig, "apply", "-f", manifest)
	runKubectl(t, kubectlPath, kubeconfig, "get", "deploy", "-n", "default")
}

// writeSmokeKubeconfig writes a minimal kubeconfig pointing at the emulator's
// plain-HTTP test server — no certificate-authority-data is needed since
// there's no TLS handshake to validate.
func writeSmokeKubeconfig(t *testing.T, baseURL, uid string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "kubeconfig.yaml")
	yaml := "apiVersion: v1\n" +
		"kind: Config\n" +
		"clusters:\n" +
		"- name: cloudemu\n" +
		"  cluster:\n" +
		"    server: " + baseURL + "/k8s/" + uid + "\n" +
		"users:\n" +
		"- name: cloudemu\n" +
		"  user:\n" +
		"    token: " + kubernetes.StubToken + "\n" +
		"contexts:\n" +
		"- name: cloudemu\n" +
		"  context:\n" +
		"    cluster: cloudemu\n" +
		"    user: cloudemu\n" +
		"    namespace: default\n" +
		"current-context: cloudemu\n"

	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write kubeconfig: %v", err)
	}

	return path
}

// writeSmokeDeploymentManifest writes a minimal Deployment manifest for
// `kubectl apply -f`.
func writeSmokeDeploymentManifest(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "deploy.yaml")
	manifest := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: smoke
  namespace: default
spec:
  replicas: 1
  selector:
    matchLabels:
      app: smoke
  template:
    metadata:
      labels:
        app: smoke
    spec:
      containers:
      - name: smoke
        image: nginx:latest
`

	if err := os.WriteFile(path, []byte(manifest), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	return path
}

// runKubectl runs kubectl with --kubeconfig plus args, failing the test with
// combined output on error.
func runKubectl(t *testing.T, kubectlPath, kubeconfig string, args ...string) {
	t.Helper()

	cmdArgs := append([]string{"--kubeconfig=" + kubeconfig}, args...)

	out, err := exec.Command(kubectlPath, cmdArgs...).CombinedOutput()
	if err != nil {
		t.Fatalf("kubectl %v: %v\n%s", args, err, out)
	}
}
