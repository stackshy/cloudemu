package serverkit

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/persist"
)

// newK8sPersistApp builds an admin-enabled, persisting App with the shared
// Kubernetes data plane wired (a k8s backend) and a ticker that never fires
// during the test.
func newK8sPersistApp(t *testing.T, stateFile string) *App {
	t.Helper()

	return newTestApp(t, Config{
		Providers:       []string{"aws"},
		Host:            "127.0.0.1",
		Ports:           map[string]string{"aws": "0"},
		K8sPort:         "0",
		Admin:           true,
		Persist:         true,
		StateFile:       stateFile,
		PersistStrategy: StrategyScheduled,
		PersistInterval: time.Hour,
		Out:             io.Discard,
	})
}

const parityPodBody = `{"apiVersion":"v1","kind":"Pod",` +
	`"metadata":{"name":"parity-pod","namespace":"default"},` +
	`"spec":{"containers":[{"name":"c","image":"nginx"}]}}`

// seedK8sCluster registers a cluster on the app's data plane and creates one Pod
// in it through the real HTTP surface (public API only), returning the UID.
func seedK8sCluster(t *testing.T, app *App) string {
	t.Helper()

	if app.k8s == nil {
		t.Fatal("precondition: k8s data plane not wired")
	}

	uid, _ := app.k8s.RegisterCluster()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost,
		"/k8s/"+uid+"/api/v1/namespaces/default/pods", strings.NewReader(parityPodBody))
	app.k8s.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("create pod via data plane = %d, want 201: %s", rec.Code, rec.Body.String())
	}

	return uid
}

// TestK8sExportParity guards the two independent export call sites (the flusher
// save via snapshotState, and the admin GET /_cloudemu/snapshot via App.snapshot)
// from drifting: both must produce byte-identical `kubernetes` payloads for the
// same state. Both funnel through exportSnapshot, so this pins that contract.
func TestK8sExportParity(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "state.json")
	app := newK8sPersistApp(t, stateFile)

	uid := seedK8sCluster(t, app)

	// Admin snapshot path.
	adminBytes, err := app.snapshot()
	if err != nil {
		t.Fatalf("app.snapshot: %v", err)
	}

	adminK8s := extractKubernetes(t, adminBytes)

	// Flusher save path: write to disk, read it back.
	if err := snapshotState(context.Background(), stateFile, true, app.snapTargets, app.k8s); err != nil {
		t.Fatalf("snapshotState: %v", err)
	}

	fileBytes, err := os.ReadFile(stateFile)
	if err != nil {
		t.Fatalf("read state file: %v", err)
	}

	fileK8s := extractKubernetes(t, fileBytes)

	if !bytes.Equal(adminK8s, fileK8s) {
		t.Fatalf("kubernetes payloads differ between export paths:\n admin=%s\n file =%s", adminK8s, fileK8s)
	}

	if !bytes.Contains(adminK8s, []byte(uid)) {
		t.Fatalf("kubernetes payload does not mention the registered cluster UID %s", uid)
	}

	if !bytes.Contains(adminK8s, []byte("parity-pod")) {
		t.Fatal("kubernetes payload does not mention the seeded pod")
	}
}

// TestK8sRestoreRoundTripThroughApp proves the full serverkit wiring: a snapshot
// captured from one App's data plane, restored into a fresh App via the admin
// restore path, reinstates the cluster under the SAME UID with its Pod intact —
// so a restored kubeconfig's /k8s/<uid> endpoint still answers.
func TestK8sRestoreRoundTripThroughApp(t *testing.T) {
	src := newK8sPersistApp(t, filepath.Join(t.TempDir(), "src.json"))
	uid := seedK8sCluster(t, src)

	snapBytes, err := src.snapshot()
	if err != nil {
		t.Fatalf("src.snapshot: %v", err)
	}

	dst := newK8sPersistApp(t, filepath.Join(t.TempDir(), "dst.json"))
	if err := dst.restore(snapBytes); err != nil {
		t.Fatalf("dst.restore: %v", err)
	}

	// The restored /k8s/<uid> endpoint must still serve the persisted pod.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet,
		"/k8s/"+uid+"/api/v1/namespaces/default/pods/parity-pod", nil)
	dst.k8s.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET restored pod = %d, want 200 (restored kubeconfig endpoint broke): %s",
			rec.Code, rec.Body.String())
	}
}

// extractKubernetes parses a whole-emulator snapshot document and returns its raw
// `kubernetes` field.
func extractKubernetes(t *testing.T, doc []byte) []byte {
	t.Helper()

	var snap persist.Snapshot
	if err := json.Unmarshal(doc, &snap); err != nil {
		t.Fatalf("unmarshal snapshot doc: %v", err)
	}

	if len(snap.Kubernetes) == 0 {
		t.Fatal("snapshot document has no kubernetes payload")
	}

	return snap.Kubernetes
}
