package snapshots_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

const wireAPIVersion = "?api-version=2023-09-01"

func newSnapshotServer(t *testing.T) *httptest.Server {
	t.Helper()

	cloud := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{
		Disks:     cloud.VirtualMachines,
		Snapshots: cloud.VirtualMachines,
	})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	return ts
}

func putRaw(t *testing.T, ts *httptest.Server, path, body string) {
	t.Helper()

	req, _ := http.NewRequest(http.MethodPut, ts.URL+path+wireAPIVersion, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		dump, _ := io.ReadAll(resp.Body)
		t.Fatalf("PUT %s: status %d body=%s", path, resp.StatusCode, dump)
	}
}

// TestSnapshotPreservesSubmittedSource verifies the snapshot echoes the exact
// source resource id the client submitted, not the driver-internal disk path.
func TestSnapshotPreservesSubmittedSource(t *testing.T) {
	ts := newSnapshotServer(t)

	diskID := "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Compute/disks/disk-src"

	putRaw(t, ts, diskID, `{
		"location":"eastus","sku":{"name":"Premium_LRS"},
		"properties":{"creationData":{"createOption":"Empty"},"diskSizeGB":50}
	}`)

	snapID := "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Compute/snapshots/snap-1"

	putRaw(t, ts, snapID, `{
		"location":"eastus",
		"properties":{"creationData":{"createOption":"Copy","sourceResourceId":"`+diskID+`"}}
	}`)

	resp, err := ts.Client().Get(ts.URL + snapID + wireAPIVersion)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		dump, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET snapshot status=%d body=%s", resp.StatusCode, dump)
	}

	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}

	props, _ := got["properties"].(map[string]any)
	cd, _ := props["creationData"].(map[string]any)

	if cd["createOption"] != "Copy" {
		t.Errorf("createOption=%v want Copy", cd["createOption"])
	}

	if cd["sourceResourceId"] != diskID {
		t.Errorf("sourceResourceId=%v want the submitted disk id %s", cd["sourceResourceId"], diskID)
	}

	if props["diskSizeGB"].(float64) != 50 {
		t.Errorf("diskSizeGB=%v want 50 (from source)", props["diskSizeGB"])
	}
}
