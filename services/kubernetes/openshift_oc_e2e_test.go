package kubernetes_test

import (
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/kubernetes"
)

// ocBinary returns the path to a real `oc` CLI to exercise, or "" if none is
// configured. Set OC_BIN, else fall back to a conventional local path. When
// absent the E2E test skips, so this never breaks CI on machines without oc.
func ocBinary() string {
	if p := os.Getenv("OC_BIN"); p != "" {
		return p
	}

	const fallback = "/tmp/ostools/oc"
	if _, err := os.Stat(fallback); err == nil {
		return fallback
	}

	return ""
}

// TestOpenShift_OcLoginE2E drives the REAL `oc` CLI against the emulator: it
// runs `oc login -u developer -p ...` (the challenging-client OAuth flow) and
// then `oc whoami`, asserting the whole wire flow completes end-to-end from a
// user's perspective. Skips when no oc binary is available.
func TestOpenShift_OcLoginE2E(t *testing.T) {
	oc := ocBinary()
	if oc == "" {
		t.Skip("no oc binary (set OC_BIN or place one at /tmp/ostools/oc)")
	}

	api := kubernetes.NewAPIServer()
	uid, _ := api.RegisterClusterWithFlavor(kubernetes.FlavorOpenShift)
	// oc's OAuth challenge path assumes an HTTPS endpoint (real clusters always
	// are) and dereferences the TLS transport, so serve over TLS here.
	ts := httptest.NewTLSServer(api)
	t.Cleanup(ts.Close)

	server := ts.URL + "/k8s/" + uid
	kubeconfig := filepath.Join(t.TempDir(), "kubeconfig")

	env := append(os.Environ(), "KUBECONFIG="+kubeconfig)

	login := exec.Command(oc, "login", server, //nolint:gosec // test-controlled args.
		"-u", "developer", "-p", "anything", "--insecure-skip-tls-verify=true")
	login.Env = env

	if out, err := login.CombinedOutput(); err != nil {
		t.Fatalf("oc login failed: %v\n%s", err, out)
	}

	who := exec.Command(oc, "whoami") //nolint:gosec // test-controlled args.
	who.Env = env

	out, err := who.CombinedOutput()
	if err != nil {
		t.Fatalf("oc whoami failed: %v\n%s", err, out)
	}

	if got := strings.TrimSpace(string(out)); got != "developer" {
		t.Errorf("oc whoami: got %q, want developer", got)
	}
}
