package compute_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu"
	gcpserver "github.com/stackshy/cloudemu/server/gcp"
)

const (
	testProject = "p-1"
	testZone    = "us-central1-a"
)

func zonesPath(suffix string) string {
	return "/compute/v1/projects/" + testProject + "/zones/" + testZone + suffix
}

func newGCPTestServer(t *testing.T) *httptest.Server {
	t.Helper()

	cloud := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.Drivers{Compute: cloud.GCE})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	return ts
}

func insertInstance(t *testing.T, ts *httptest.Server, name string) map[string]any {
	t.Helper()

	body := strings.NewReader(`{
		"name": "` + name + `",
		"machineType": "https://www.googleapis.com/compute/v1/projects/p-1/zones/us-central1-a/machineTypes/n1-standard-1",
		"disks": [{
			"boot": true,
			"autoDelete": true,
			"initializeParams": {
				"sourceImage": "projects/debian-cloud/global/images/family/debian-12"
			}
		}],
		"networkInterfaces": [{
			"network": "global/networks/default"
		}]
	}`)

	resp, err := ts.Client().Post(ts.URL+zonesPath("/instances"), "application/json", body)
	if err != nil {
		t.Fatalf("insert %s: %v", name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		dump, _ := io.ReadAll(resp.Body)
		t.Fatalf("insert %s: status %d body=%s", name, resp.StatusCode, dump)
	}

	var op map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&op); err != nil {
		t.Fatalf("decode: %v", err)
	}

	return op
}

func TestInsertReturnsDoneOperation(t *testing.T) {
	ts := newGCPTestServer(t)
	op := insertInstance(t, ts, "vm-1")

	if op["kind"] != "compute#operation" {
		t.Errorf("kind=%v", op["kind"])
	}

	if op["status"] != "DONE" {
		t.Errorf("status=%v want DONE", op["status"])
	}

	if op["operationType"] != "insert" {
		t.Errorf("operationType=%v", op["operationType"])
	}

	target, _ := op["targetLink"].(string)
	if !strings.HasSuffix(target, "/instances/vm-1") {
		t.Errorf("targetLink=%s", target)
	}
}

func TestGetInstance(t *testing.T) {
	ts := newGCPTestServer(t)
	_ = insertInstance(t, ts, "vm-get")

	resp, err := ts.Client().Get(ts.URL + zonesPath("/instances/vm-get"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}

	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}

	if got["kind"] != "compute#instance" {
		t.Errorf("kind=%v", got["kind"])
	}

	if got["name"] != "vm-get" {
		t.Errorf("name=%v", got["name"])
	}

	machineType, _ := got["machineType"].(string)
	if !strings.HasSuffix(machineType, "/machineTypes/n1-standard-1") {
		t.Errorf("machineType=%s", machineType)
	}
}

func TestGetInstanceNotFound(t *testing.T) {
	ts := newGCPTestServer(t)

	resp, err := ts.Client().Get(ts.URL + zonesPath("/instances/missing"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status=%d want 404", resp.StatusCode)
	}

	var env map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&env)

	body, _ := env["error"].(map[string]any)
	if body["status"] != "notFound" {
		t.Errorf("status=%v want notFound", body["status"])
	}
}

func TestListInstances(t *testing.T) {
	ts := newGCPTestServer(t)
	_ = insertInstance(t, ts, "vm-a")
	_ = insertInstance(t, ts, "vm-b")

	resp, err := ts.Client().Get(ts.URL + zonesPath("/instances"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}

	var got struct {
		Kind  string           `json:"kind"`
		Items []map[string]any `json:"items"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}

	if got.Kind != "compute#instanceList" {
		t.Errorf("kind=%s", got.Kind)
	}

	if len(got.Items) != 2 {
		t.Errorf("got %d items", len(got.Items))
	}
}

func TestDeleteInstance(t *testing.T) {
	ts := newGCPTestServer(t)
	_ = insertInstance(t, ts, "vm-del")

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+zonesPath("/instances/vm-del"), http.NoBody)

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}

	var op map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&op)

	if op["operationType"] != "delete" {
		t.Errorf("operationType=%v", op["operationType"])
	}
}

func TestLifecycleActions(t *testing.T) {
	ts := newGCPTestServer(t)
	_ = insertInstance(t, ts, "vm-life")

	for _, action := range []string{"stop", "start", "reset"} {
		t.Run(action, func(t *testing.T) {
			resp, err := ts.Client().Post(
				ts.URL+zonesPath("/instances/vm-life/"+action),
				"application/json",
				http.NoBody,
			)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				dump, _ := io.ReadAll(resp.Body)
				t.Fatalf("%s: status=%d body=%s", action, resp.StatusCode, dump)
			}

			var op map[string]any
			_ = json.NewDecoder(resp.Body).Decode(&op)

			if op["status"] != "DONE" {
				t.Errorf("%s: status=%v want DONE", action, op["status"])
			}

			if op["operationType"] != action {
				t.Errorf("%s: operationType=%v", action, op["operationType"])
			}
		})
	}
}

func TestGetOperationAlwaysDone(t *testing.T) {
	ts := newGCPTestServer(t)
	op := insertInstance(t, ts, "vm-op")

	selfLink, _ := op["selfLink"].(string)
	if selfLink == "" {
		t.Fatal("missing selfLink")
	}

	// SDK clients poll the operation by GETting selfLink. Since our mock is
	// synchronous, the poll must immediately return DONE.
	resp, err := ts.Client().Get(selfLink)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		dump, _ := io.ReadAll(resp.Body)
		t.Fatalf("operation poll: status=%d body=%s", resp.StatusCode, dump)
	}

	var got map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&got)

	if got["status"] != "DONE" {
		t.Errorf("operation status=%v want DONE", got["status"])
	}
}

func TestRejectsNonComputePaths(t *testing.T) {
	ts := newGCPTestServer(t)

	// Storage path — handler should not claim it; server returns 501.
	resp, err := ts.Client().Get(ts.URL + "/storage/v1/b/my-bucket")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotImplemented {
		t.Errorf("status=%d want 501", resp.StatusCode)
	}
}

func TestInsertInvalidJSON(t *testing.T) {
	ts := newGCPTestServer(t)

	resp, err := ts.Client().Post(ts.URL+zonesPath("/instances"),
		"application/json", bytes.NewBufferString(`{not json`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d want 400", resp.StatusCode)
	}
}

func TestInsertGlobalScopeRejected(t *testing.T) {
	ts := newGCPTestServer(t)

	body := strings.NewReader(`{"name":"x","machineType":"n1-standard-1"}`)

	resp, err := ts.Client().Post(
		ts.URL+"/compute/v1/projects/p-1/global/instances",
		"application/json", body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d want 400", resp.StatusCode)
	}
}
